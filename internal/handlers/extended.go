package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"pdf-forge/internal/converters"
	"pdf-forge/internal/middleware"
	"pdf-forge/internal/models"
	"pdf-forge/internal/services"
	"pdf-forge/internal/templates"
)

// ExtendedHandler adds template and manipulation handlers
type ExtendedHandler struct {
	*Handler
	templateEngine *templates.TemplateEngine
	manipulator    *converters.PDFManipulator
	webhookSvc     *services.WebhookService
	storageSvc     *services.StorageService
}

// NewExtendedHandler creates an extended handler with all features
func NewExtendedHandler(h *Handler) (*ExtendedHandler, error) {
	manipulator, err := converters.NewPDFManipulator()
	if err != nil {
		return nil, fmt.Errorf("failed to create manipulator: %w", err)
	}

	return &ExtendedHandler{
		Handler:        h,
		templateEngine: templates.NewTemplateEngine(),
		manipulator:    manipulator,
		webhookSvc:     services.NewWebhookService(h.logger),
		storageSvc:     services.NewStorageService(h.logger),
	}, nil
}

// Close releases resources
func (h *ExtendedHandler) Close() error {
	if h.manipulator != nil {
		return h.manipulator.Close()
	}
	return nil
}

// Template handles template-based PDF generation
func (h *ExtendedHandler) Template(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())

	var req models.TemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.errorResponse(w, http.StatusBadRequest, "Invalid JSON payload", requestID)
		return
	}

	if req.Template == "" {
		h.errorResponse(w, http.StatusBadRequest, "Template type is required", requestID)
		return
	}

	var html string
	var err error

	// Render template
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
		h.logger.Error("Template rendering failed",
			"request_id", requestID,
			"template", req.Template,
			"error", err.Error(),
		)
		h.errorResponse(w, http.StatusBadRequest, "Template rendering failed: "+err.Error(), requestID)
		return
	}

	// Convert to PDF
	pdfData, err := h.converter.ConvertHTML(r.Context(), html, req.Options)
	if err != nil {
		h.logger.Error("PDF conversion failed",
			"request_id", requestID,
			"error", err.Error(),
		)
		h.errorResponse(w, http.StatusInternalServerError, "PDF conversion failed: "+err.Error(), requestID)
		return
	}

	// Apply post-processing
	if req.Options != nil && h.processor != nil {
		pdfData, err = h.processor.Process(pdfData, req.Options)
		if err != nil {
			h.errorResponse(w, http.StatusInternalServerError, "Post-processing failed: "+err.Error(), requestID)
			return
		}
	}

	h.logger.Info("Template PDF generated",
		"request_id", requestID,
		"template", req.Template,
		"size_bytes", len(pdfData),
	)

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(pdfData)))
	w.Write(pdfData)
}

// Manipulate handles PDF manipulation operations
func (h *ExtendedHandler) Manipulate(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())

	var req models.ManipulateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.errorResponse(w, http.StatusBadRequest, "Invalid JSON payload", requestID)
		return
	}

	if req.Operation == "" {
		h.errorResponse(w, http.StatusBadRequest, "Operation is required", requestID)
		return
	}

	// Decode PDF
	pdfData, err := base64.StdEncoding.DecodeString(req.PDF)
	if err != nil {
		h.errorResponse(w, http.StatusBadRequest, "Invalid Base64 PDF data", requestID)
		return
	}

	ctx := r.Context()
	result := &models.ManipulateResult{
		Operation: req.Operation,
		Success:   true,
	}

	switch req.Operation {
	case "split":
		splitReq := &converters.SplitRequest{
			PDF:       pdfData,
			SplitType: req.Options.SplitType,
			Pages:     req.Options.Pages,
			EveryN:    req.Options.EveryN,
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

	default:
		h.errorResponse(w, http.StatusBadRequest, "Unknown operation: "+req.Operation, requestID)
		return
	}

	h.logger.Info("PDF manipulation completed",
		"request_id", requestID,
		"operation", req.Operation,
		"success", result.Success,
	)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// Async handles async conversion with webhook callback
func (h *ExtendedHandler) Async(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())

	var req models.AsyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.errorResponse(w, http.StatusBadRequest, "Invalid JSON payload", requestID)
		return
	}

	if req.Webhook == nil && req.Storage == nil {
		h.errorResponse(w, http.StatusBadRequest, "Either webhook or storage config is required", requestID)
		return
	}

	// Process in background
	go h.processAsync(requestID, &req)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"request_id": requestID,
		"status":     "queued",
		"message":    "Request accepted for processing",
	})
}

func (h *ExtendedHandler) processAsync(requestID string, req *models.AsyncRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	startTime := time.Now()
	convType := string(req.Request.Type)

	// Perform conversion
	var pdfData []byte
	var err error

	switch req.Request.Type {
	case models.ConvertHTML:
		html := req.Request.HTML
		if req.Request.IsBase64 {
			decoded, decErr := base64.StdEncoding.DecodeString(html)
			if decErr != nil {
				err = decErr
			} else {
				html = string(decoded)
			}
		}
		if err == nil {
			pdfData, err = h.converter.ConvertHTML(ctx, html, req.Request.Options)
		}
	case models.ConvertURL:
		pdfData, err = h.converter.ConvertURL(ctx, req.Request.URL, req.Request.Options)
	case models.ConvertMarkdown:
		pdfData, err = h.converter.ConvertMarkdown(ctx, req.Request.Markdown, req.Request.Options)
	case models.ConvertImage:
		pdfData, err = h.converter.ConvertImage(ctx, req.Request.Image, req.Request.Options)
	case models.ConvertImages:
		pdfData, err = h.converter.ConvertImages(ctx, req.Request.Images, req.Request.Options)
	default:
		err = fmt.Errorf("unsupported conversion type: %s", req.Request.Type)
	}

	duration := time.Since(startTime)

	// Apply post-processing
	if err == nil && req.Request.Options != nil && h.processor != nil {
		pdfData, err = h.processor.Process(pdfData, req.Request.Options)
	}

	// Upload to storage if configured
	var storageResult *models.StorageResult
	if err == nil && req.Storage != nil {
		storageResult, err = h.storageSvc.Upload(ctx, req.Storage, pdfData, "application/pdf")
	}

	// Send webhook if configured
	if req.Webhook != nil {
		var payload *services.WebhookPayload
		if err != nil {
			payload = services.CreateErrorPayload(requestID, convType, err, duration)
		} else {
			includePDF := req.Webhook.IncludePDF && req.Storage == nil
			payload = services.CreateSuccessPayload(requestID, convType, pdfData, duration, includePDF)
			payload.Storage = storageResult
		}

		if webhookErr := h.webhookSvc.Send(ctx, req.Webhook, payload); webhookErr != nil {
			h.logger.Error("Webhook delivery failed",
				"request_id", requestID,
				"error", webhookErr.Error(),
			)
		}
	}

	h.logger.Info("Async conversion completed",
		"request_id", requestID,
		"type", convType,
		"success", err == nil,
		"duration_ms", duration.Milliseconds(),
	)
}

// Batch handles batch conversion requests
func (h *ExtendedHandler) Batch(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())

	var req models.BatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.errorResponse(w, http.StatusBadRequest, "Invalid JSON payload", requestID)
		return
	}

	if len(req.Requests) == 0 {
		h.errorResponse(w, http.StatusBadRequest, "At least one request is required", requestID)
		return
	}

	result := &models.BatchResult{
		RequestID: requestID,
		Total:     len(req.Requests),
		Results:   make([]models.BatchItemResult, 0, len(req.Requests)),
	}

	var allPDFs [][]byte

	for i, convReq := range req.Requests {
		itemResult := models.BatchItemResult{Index: i}

		var pdfData []byte
		var err error

		ctx := r.Context()

		switch convReq.Type {
		case models.ConvertHTML:
			html := convReq.HTML
			if convReq.IsBase64 {
				decoded, _ := base64.StdEncoding.DecodeString(html)
				html = string(decoded)
			}
			pdfData, err = h.converter.ConvertHTML(ctx, html, convReq.Options)
		case models.ConvertURL:
			pdfData, err = h.converter.ConvertURL(ctx, convReq.URL, convReq.Options)
		case models.ConvertMarkdown:
			pdfData, err = h.converter.ConvertMarkdown(ctx, convReq.Markdown, convReq.Options)
		case models.ConvertImage:
			pdfData, err = h.converter.ConvertImage(ctx, convReq.Image, convReq.Options)
		case models.ConvertImages:
			pdfData, err = h.converter.ConvertImages(ctx, convReq.Images, convReq.Options)
		default:
			err = fmt.Errorf("unsupported type: %s", convReq.Type)
		}

		if err != nil {
			itemResult.Success = false
			itemResult.Error = err.Error()
			result.Failed++
		} else {
			// Apply post-processing
			if convReq.Options != nil && h.processor != nil {
				pdfData, err = h.processor.Process(pdfData, convReq.Options)
				if err != nil {
					itemResult.Success = false
					itemResult.Error = err.Error()
					result.Failed++
				}
			}

			if err == nil {
				itemResult.Success = true
				itemResult.Size = int64(len(pdfData))
				if !req.Merge {
					itemResult.PDF = base64.StdEncoding.EncodeToString(pdfData)
				}
				result.Completed++
				allPDFs = append(allPDFs, pdfData)
			}
		}

		result.Results = append(result.Results, itemResult)
	}

	// Merge if requested
	if req.Merge && len(allPDFs) > 0 && h.processor != nil {
		merged, err := h.processor.MergePDFs(allPDFs)
		if err == nil {
			result.MergedPDF = base64.StdEncoding.EncodeToString(merged)
		}
	}

	h.logger.Info("Batch conversion completed",
		"request_id", requestID,
		"total", result.Total,
		"completed", result.Completed,
		"failed", result.Failed,
	)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// TableToPDF converts table data (CSV/JSON) to PDF
func (h *ExtendedHandler) TableToPDF(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())

	var req struct {
		Data    models.TableData   `json:"data"`
		Options *models.PDFOptions `json:"options,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.errorResponse(w, http.StatusBadRequest, "Invalid JSON payload", requestID)
		return
	}

	// Generate HTML table
	html := generateTableHTML(&req.Data)

	// Convert to PDF
	pdfData, err := h.converter.ConvertHTML(r.Context(), html, req.Options)
	if err != nil {
		h.errorResponse(w, http.StatusInternalServerError, "PDF conversion failed: "+err.Error(), requestID)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(pdfData)))
	w.Write(pdfData)
}

func generateTableHTML(data *models.TableData) string {
	html := `<!DOCTYPE html>
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
<body>`

	if data.Title != "" {
		html += fmt.Sprintf("<h1>%s</h1>", data.Title)
	}

	html += "<table><thead><tr>"
	for _, header := range data.Headers {
		html += fmt.Sprintf("<th>%s</th>", header)
	}
	html += "</tr></thead><tbody>"

	for _, row := range data.Rows {
		html += "<tr>"
		for _, cell := range row {
			html += fmt.Sprintf("<td>%s</td>", cell)
		}
		html += "</tr>"
	}

	html += "</tbody></table>"

	if data.Footer != "" {
		html += fmt.Sprintf("<div class='footer'>%s</div>", data.Footer)
	}

	html += "</body></html>"
	return html
}
