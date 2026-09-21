package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"pdf-forge/internal/converters"
	"pdf-forge/internal/limits"
	"pdf-forge/internal/metrics"
	"pdf-forge/internal/middleware"
	"pdf-forge/internal/models"
	"pdf-forge/internal/services"
	"pdf-forge/internal/templates"
)

// maxConcurrentAsync caps how many /async jobs can be in flight at once.
// HTTP admission additionally bounds retained payloads through delivery.
const maxConcurrentAsync = 32

// ExtendedHandler adds template and manipulation handlers.
type ExtendedHandler struct {
	*Handler
	templateEngine *templates.TemplateEngine
	manipulator    *converters.PDFManipulator
	webhookSvc     *services.WebhookService
	storageSvc     *services.StorageService
	asyncSlots     chan struct{}
	deliverySlots  limits.Gate
	jobsCtx        context.Context
	cancelJobs     context.CancelFunc
	jobs           sync.WaitGroup
	jobsMu         sync.Mutex
	closing        bool
}

// NewExtendedHandler creates an extended handler with all features.
func NewExtendedHandler(h *Handler) (*ExtendedHandler, error) {
	manipulator, err := converters.NewPDFManipulator()
	if err != nil {
		return nil, fmt.Errorf("failed to create manipulator: %w", err)
	}

	jobsCtx, cancelJobs := context.WithCancel(context.Background())
	return &ExtendedHandler{
		Handler:        h,
		templateEngine: templates.NewTemplateEngine(),
		manipulator:    manipulator,
		webhookSvc:     services.NewWebhookService(h.logger),
		storageSvc:     services.NewStorageService(h.logger),
		asyncSlots:     make(chan struct{}, maxConcurrentAsync),
		deliverySlots:  limits.NewGate(4),
		jobsCtx:        jobsCtx, cancelJobs: cancelJobs,
	}, nil
}

// Close releases resources.
func (h *ExtendedHandler) Close() error {
	h.jobsMu.Lock()
	h.closing = true
	h.cancelJobs()
	h.jobsMu.Unlock()
	h.jobs.Wait()
	if h.manipulator != nil {
		return h.manipulator.Close()
	}
	return nil
}

// Template handles template-based PDF generation.
func (h *ExtendedHandler) Template(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())

	var req models.TemplateRequest
	if err := decodeJSON(r, &req); err != nil {
		h.handleDecodeError(w, err, requestID)
		return
	}

	if req.Template == "" {
		h.errorResponse(w, http.StatusBadRequest, "Template type is required", requestID)
		return
	}

	start := time.Now()
	var html string
	var err error

	if req.Template == "custom" {
		if req.CustomHTML == "" {
			h.errorResponse(w, http.StatusBadRequest, "Custom HTML is required for custom template", requestID)
			return
		}
		html, err = h.templateEngine.RenderCustom(req.CustomHTML, req.Data)
	} else {
		html, err = h.templateEngine.Render(templates.TemplateType(req.Template), req.Data)
	}

	if err != nil {
		h.logger.Error("Template rendering failed", "request_id", requestID, "template", req.Template, "error", err.Error())
		metrics.Record("template", "failure", time.Since(start).Seconds(), 0)
		h.errorResponse(w, http.StatusBadRequest, "Template rendering failed: "+err.Error(), requestID)
		return
	}

	pdfData, err := h.converter.ConvertHTML(r.Context(), html, req.Options)
	if err != nil {
		h.logger.Error("PDF conversion failed", "request_id", requestID, "error", err.Error())
		metrics.Record("template", "failure", time.Since(start).Seconds(), 0)
		h.errorResponse(w, http.StatusInternalServerError, "PDF conversion failed: "+err.Error(), requestID)
		return
	}

	if req.Options != nil && h.processor != nil {
		pdfData, err = h.process(r.Context(), pdfData, req.Options)
		if err != nil {
			metrics.Record("template", "failure", time.Since(start).Seconds(), 0)
			h.errorResponse(w, http.StatusInternalServerError, "Post-processing failed: "+err.Error(), requestID)
			return
		}
	}

	metrics.Record("template", "success", time.Since(start).Seconds(), len(pdfData))
	h.logger.Info("Template PDF generated", "request_id", requestID, "template", req.Template, "size_bytes", len(pdfData))

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(pdfData)))
	_, _ = w.Write(pdfData)
}

// Manipulate handles PDF manipulation operations.
func (h *ExtendedHandler) Manipulate(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())

	var req models.ManipulateRequest
	if err := decodeJSON(r, &req); err != nil {
		h.handleDecodeError(w, err, requestID)
		return
	}

	if req.Operation == "" {
		h.errorResponse(w, http.StatusBadRequest, "Operation is required", requestID)
		return
	}

	pdfData, err := base64.StdEncoding.DecodeString(req.PDF)
	if err != nil {
		h.errorResponse(w, http.StatusBadRequest, "Invalid Base64 PDF data", requestID)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), limits.ProcessingTimeout)
	defer cancel()
	if err := h.processing.Acquire(ctx); err != nil {
		h.errorResponse(w, http.StatusGatewayTimeout, err.Error(), requestID)
		return
	}
	defer h.processing.Release()
	result := &models.ManipulateResult{Operation: req.Operation, Success: true}
	start := time.Now()

	switch req.Operation {
	case "split":
		opts := req.Options
		if opts == nil {
			opts = &models.ManipulateOptions{}
		}
		splitReq := &converters.SplitRequest{
			PDF:       pdfData,
			SplitType: opts.SplitType,
			Pages:     opts.Pages,
			EveryN:    opts.EveryN,
		}
		if splitReq.SplitType == "" {
			splitReq.SplitType = "all"
		}
		splitResult, err := h.manipulator.Split(ctx, splitReq)
		if err != nil {
			result.Success = false
			result.Message = err.Error()
		} else {
			result.Count = splitResult.Count
			for _, page := range splitResult.Pages {
				result.Files = append(result.Files, base64.StdEncoding.EncodeToString(page))
			}
			result.Message = fmt.Sprintf("Split into %d parts", splitResult.Count)
		}

	case "extract":
		if req.Options == nil || req.Options.Pages == "" {
			h.errorResponse(w, http.StatusBadRequest, "Pages parameter is required for extract", requestID)
			return
		}
		extracted, err := h.manipulator.ExtractPages(ctx, pdfData, req.Options.Pages)
		if err != nil {
			result.Success = false
			result.Message = err.Error()
		} else {
			result.PDF = base64.StdEncoding.EncodeToString(extracted)
			result.Message = "Pages extracted successfully"
		}

	case "rotate":
		rotation := 90
		pages := "1-z"
		if req.Options != nil {
			if req.Options.Rotation != 0 {
				rotation = req.Options.Rotation
			}
			if req.Options.Pages != "" {
				pages = req.Options.Pages
			}
		}
		rotated, err := h.manipulator.RotatePages(ctx, pdfData, rotation, pages)
		if err != nil {
			result.Success = false
			result.Message = err.Error()
		} else {
			result.PDF = base64.StdEncoding.EncodeToString(rotated)
			result.Message = fmt.Sprintf("Rotated %d degrees", rotation)
		}

	case "compress":
		level := converters.CompressEbook
		if req.Options != nil && req.Options.CompressionLevel != "" {
			level = converters.CompressLevel(req.Options.CompressionLevel)
		}
		compressed, savings, err := h.manipulator.Compress(ctx, pdfData, level)
		if err != nil {
			result.Success = false
			result.Message = err.Error()
		} else {
			result.PDF = base64.StdEncoding.EncodeToString(compressed)
			result.OriginalSize = int64(len(pdfData))
			result.CompressedSize = int64(len(compressed))
			result.SavingsPercent = savings
			result.Message = fmt.Sprintf("Compressed by %d%%", savings)
		}

	case "info":
		info, err := h.manipulator.GetInfo(ctx, pdfData)
		if err != nil {
			result.Success = false
			result.Message = err.Error()
		} else {
			result.Info = info
			result.Message = "PDF info retrieved"
		}

	case "remove":
		if req.Options == nil || req.Options.Pages == "" {
			h.errorResponse(w, http.StatusBadRequest, "Pages parameter is required for remove", requestID)
			return
		}
		modified, err := h.manipulator.RemovePages(ctx, pdfData, req.Options.Pages)
		if err != nil {
			result.Success = false
			result.Message = err.Error()
		} else {
			result.PDF = base64.StdEncoding.EncodeToString(modified)
			result.Message = "Pages removed successfully"
		}

	case "reorder":
		if req.Options == nil || len(req.Options.NewOrder) == 0 {
			h.errorResponse(w, http.StatusBadRequest, "new_order parameter is required for reorder", requestID)
			return
		}
		reordered, err := h.manipulator.ReorderPages(ctx, pdfData, req.Options.NewOrder)
		if err != nil {
			result.Success = false
			result.Message = err.Error()
		} else {
			result.PDF = base64.StdEncoding.EncodeToString(reordered)
			result.Message = "Pages reordered successfully"
		}

	case "to_images":
		format := "jpeg"
		dpi := 150
		if req.Options != nil {
			if req.Options.ImageFormat != "" {
				format = req.Options.ImageFormat
			}
			if req.Options.DPI > 0 {
				dpi = req.Options.DPI
			}
		}
		images, err := h.manipulator.PDFToImages(ctx, pdfData, format, dpi)
		if err != nil {
			result.Success = false
			result.Message = err.Error()
		} else {
			result.Count = len(images)
			for _, img := range images {
				result.Files = append(result.Files, base64.StdEncoding.EncodeToString(img))
			}
			result.Message = fmt.Sprintf("Converted to %d images", len(images))
		}

	case "page_numbers":
		position := ""
		format := ""
		if req.Options != nil {
			position = req.Options.SplitType // reuse "split_type" field as position (or read from pages)
		}
		stamped, err := h.manipulator.AddPageNumbers(ctx, pdfData, position, format)
		if err != nil {
			result.Success = false
			result.Message = err.Error()
		} else {
			result.PDF = base64.StdEncoding.EncodeToString(stamped)
			result.Message = "Page numbers added"
		}

	default:
		h.errorResponse(w, http.StatusBadRequest, "Unknown operation: "+req.Operation, requestID)
		return
	}

	if ctx.Err() != nil {
		result.Success = false
		result.Message = ctx.Err().Error()
	}
	outcome := "success"
	if !result.Success {
		outcome = "failure"
	}
	size := 0
	if result.PDF != "" {
		size = len(result.PDF)
	}
	metrics.Record("manipulate."+req.Operation, outcome, time.Since(start).Seconds(), size)

	h.logger.Info("PDF manipulation completed", "request_id", requestID, "operation", req.Operation, "success", result.Success)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// Async handles async conversion with webhook callback.
func (h *ExtendedHandler) Async(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())

	var req models.AsyncRequest
	if err := decodeJSON(r, &req); err != nil {
		h.handleDecodeError(w, err, requestID)
		return
	}

	if req.Webhook == nil && req.Storage == nil {
		h.errorResponse(w, http.StatusBadRequest, "Either webhook or storage config is required", requestID)
		return
	}

	// Reject if the queue is full so callers get clear back-pressure.
	select {
	case h.asyncSlots <- struct{}{}:
	default:
		h.errorResponse(w, http.StatusServiceUnavailable, "Async queue is full, retry later", requestID)
		return
	}
	h.jobsMu.Lock()
	if h.closing {
		h.jobsMu.Unlock()
		<-h.asyncSlots
		h.errorResponse(w, http.StatusServiceUnavailable, "Server shutting down", requestID)
		return
	}
	h.jobs.Add(1)
	h.jobsMu.Unlock()
	metrics.AsyncQueueDepth.Inc()
	releaseInput := middleware.RetainAdmission(r.Context())
	var once sync.Once
	releaseConversion := func() { once.Do(func() { <-h.asyncSlots; metrics.AsyncQueueDepth.Dec() }) }
	go func() {
		defer h.jobs.Done()
		defer releaseInput()
		defer releaseConversion()
		defer func() {
			if v := recover(); v != nil {
				h.logger.Error("Async job panicked", "request_id", requestID, "error", v)
			}
		}()
		h.processAsync(requestID, &req, releaseConversion)
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"request_id": requestID,
		"status":     "queued",
		"message":    "Request accepted for processing",
	})
}

func (h *ExtendedHandler) processAsync(requestID string, req *models.AsyncRequest, releaseConversion func()) {
	ctx, cancel := context.WithTimeout(h.jobsCtx, 5*time.Minute)
	defer cancel()

	startTime := time.Now()
	convType := string(req.Request.Type)
	if convType == "" {
		convType = "html"
	}

	pdfData, err := h.convertItem(ctx, &req.Request)

	var storageResult *models.StorageResult
	if err == nil && req.Storage != nil {
		storageResult, err = h.storageSvc.Upload(ctx, req.Storage, pdfData, "application/pdf")
	}

	duration := time.Since(startTime)
	releaseConversion()
	defer func() { metrics.StageDuration.WithLabelValues("async_total").Observe(time.Since(startTime).Seconds()) }()

	outcome := "success"
	if err != nil {
		outcome = "failure"
	}
	metrics.Record("async."+convType, outcome, duration.Seconds(), len(pdfData))

	if req.Webhook != nil {
		var payload *services.WebhookPayload
		if err != nil {
			payload = services.CreateErrorPayload(requestID, convType, err, duration)
		} else {
			includePDF := req.Webhook.IncludePDF && req.Storage == nil
			payload = services.CreateSuccessPayload(requestID, convType, pdfData, duration, includePDF)
			payload.Storage = storageResult
		}

		deliveryStart := time.Now()
		if gateErr := h.deliverySlots.Acquire(ctx); gateErr != nil {
			h.logger.Error("Webhook admission failed", "request_id", requestID, "error", gateErr)
			return
		}
		defer h.deliverySlots.Release()
		defer func() { metrics.StageDuration.WithLabelValues("delivery").Observe(time.Since(deliveryStart).Seconds()) }()
		if webhookErr := h.webhookSvc.Send(ctx, req.Webhook, payload); webhookErr != nil {
			h.logger.Error("Webhook delivery failed", "request_id", requestID, "error", webhookErr.Error())
		}
	}

	h.logger.Info("Async conversion completed",
		"request_id", requestID,
		"type", convType,
		"success", err == nil,
		"duration_ms", duration.Milliseconds(),
	)
}

// Batch handles batch conversion requests.
func (h *ExtendedHandler) Batch(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())

	var req models.BatchRequest
	if err := decodeJSON(r, &req); err != nil {
		h.handleDecodeError(w, err, requestID)
		return
	}

	if len(req.Requests) == 0 {
		h.errorResponse(w, http.StatusBadRequest, "At least one request is required", requestID)
		return
	}

	if len(req.Requests) > limits.MaxBatchItems {
		h.errorResponse(w, http.StatusBadRequest, "Batch exceeds 32 items", requestID)
		return
	}
	if req.Merge && h.processor == nil {
		h.errorResponse(w, http.StatusServiceUnavailable, "PDF processor unavailable", requestID)
		return
	}
	start := time.Now()
	result := &models.BatchResult{RequestID: requestID, Total: len(req.Requests), Results: make([]models.BatchItemResult, len(req.Requests))}
	allPDFs := make([][]byte, len(req.Requests))
	var mu sync.Mutex
	var outputBytes int
	// Two items per batch prevents a single batch creating an unbounded fan-out.
	var wg sync.WaitGroup
	jobs := make(chan int)
	for worker := 0; worker < min(2, len(req.Requests)); worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				itemStart := time.Now()
				pdf, err := h.convertItem(r.Context(), &req.Requests[i])
				mu.Lock()
				if err == nil {
					if len(pdf) > limits.MaxOutputBytes-outputBytes {
						err = limits.ErrOutputLimit
					} else {
						outputBytes += len(pdf)
					}
				}
				mu.Unlock()
				item := models.BatchItemResult{Index: i}
				outcome := "failure"
				if err != nil {
					item.Error = err.Error()
				} else {
					item.Success = true
					item.Size = int64(len(pdf))
					outcome = "success"
					if req.Merge {
						allPDFs[i] = pdf
					} else {
						item.PDF = base64.StdEncoding.EncodeToString(pdf)
					}
				}
				result.Results[i] = item
				metrics.Record("batch.item", outcome, time.Since(itemStart).Seconds(), len(pdf))
			}
		}()
	}
	for i := range req.Requests {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	var mergePDFs [][]byte
	for i, item := range result.Results {
		if item.Success {
			result.Completed++
			if req.Merge {
				mergePDFs = append(mergePDFs, allPDFs[i])
			}
		} else {
			result.Failed++
		}
	}
	if req.Merge && len(mergePDFs) > 0 {
		merged, err := h.merge(r.Context(), mergePDFs)
		if err != nil {
			h.errorResponse(w, http.StatusInternalServerError, "Batch merge failed: "+err.Error(), requestID)
			return
		}
		result.MergedPDF = base64.StdEncoding.EncodeToString(merged)
	}
	metrics.StageDuration.WithLabelValues("batch").Observe(time.Since(start).Seconds())

	h.logger.Info("Batch conversion completed",
		"request_id", requestID,
		"total", result.Total,
		"completed", result.Completed,
		"failed", result.Failed,
	)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// TableToPDF converts table data (CSV/JSON) to PDF.
func (h *ExtendedHandler) TableToPDF(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	outcome := "failure"
	outputSize := 0
	defer func() { metrics.Record("table", outcome, time.Since(start).Seconds(), outputSize) }()
	requestID := middleware.GetRequestID(r.Context())

	var req struct {
		Data    models.TableData   `json:"data"`
		Options *models.PDFOptions `json:"options,omitempty"`
	}
	if err := decodeJSON(r, &req); err != nil {
		h.handleDecodeError(w, err, requestID)
		return
	}

	html := generateTableHTML(&req.Data)

	pdfData, err := h.converter.ConvertHTML(r.Context(), html, req.Options)
	if err != nil {
		h.errorResponse(w, http.StatusInternalServerError, "PDF conversion failed: "+err.Error(), requestID)
		return
	}

	if req.Options != nil && h.processor != nil {
		pdfData, err = h.process(r.Context(), pdfData, req.Options)
		if err != nil {
			h.errorResponse(w, http.StatusInternalServerError, "Processing failed: "+err.Error(), requestID)
			return
		}
	}

	outcome = "success"
	outputSize = len(pdfData)
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(pdfData)))
	_, _ = w.Write(pdfData)
}

func generateTableHTML(data *models.TableData) string {
	var b []byte
	b = append(b, []byte(`<!DOCTYPE html>
<html>
<head>
<meta charset="UTF-8">
<style>
body { font-family: Arial, sans-serif; padding: 40px; }
h1 { color: #333; margin-bottom: 20px; }
table { width: 100%; border-collapse: collapse; margin-bottom: 20px; }
th { background: #4a5568; color: white; padding: 12px; text-align: left; }
td { padding: 10px 12px; border-bottom: 1px solid #e2e8f0; }
tr:nth-child(even) { background: #f7fafc; }
.footer { color: #666; font-size: 12px; margin-top: 20px; }
</style>
</head>
<body>`)...)

	if data.Title != "" {
		b = append(b, fmt.Sprintf("<h1>%s</h1>", data.Title)...)
	}

	b = append(b, []byte("<table><thead><tr>")...)
	for _, header := range data.Headers {
		b = append(b, fmt.Sprintf("<th>%s</th>", header)...)
	}
	b = append(b, []byte("</tr></thead><tbody>")...)

	for _, row := range data.Rows {
		b = append(b, []byte("<tr>")...)
		for _, cell := range row {
			b = append(b, fmt.Sprintf("<td>%s</td>", cell)...)
		}
		b = append(b, []byte("</tr>")...)
	}

	b = append(b, []byte("</tbody></table>")...)

	if data.Footer != "" {
		b = append(b, fmt.Sprintf("<div class='footer'>%s</div>", data.Footer)...)
	}

	b = append(b, []byte("</body></html>")...)
	return string(b)
}

func (h *ExtendedHandler) convertItem(ctx context.Context, req *models.ConversionRequest) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var pdf []byte
	var err error
	switch req.Type {
	case models.ConvertHTML, "":
		html := req.HTML
		if req.IsBase64 {
			data, e := base64.StdEncoding.DecodeString(html)
			if e != nil {
				return nil, e
			}
			html = string(data)
		}
		pdf, err = h.converter.ConvertHTML(ctx, html, req.Options)
	case models.ConvertURL:
		pdf, err = h.converter.ConvertURL(ctx, req.URL, req.Options)
	case models.ConvertMarkdown:
		pdf, err = h.converter.ConvertMarkdown(ctx, req.Markdown, req.Options)
	case models.ConvertImage:
		pdf, err = h.converter.ConvertImage(ctx, req.Image, req.Options)
	case models.ConvertImages:
		pdf, err = h.converter.ConvertImages(ctx, req.Images, req.Options)
	default:
		return nil, fmt.Errorf("unsupported conversion type: %s", req.Type)
	}
	if err == nil && h.processor != nil {
		pdf, err = h.process(ctx, pdf, req.Options)
	}
	return pdf, err
}
