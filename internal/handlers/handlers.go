package handlers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"pdf-forge/internal/converters"
	"pdf-forge/internal/metrics"
	"pdf-forge/internal/middleware"
	"pdf-forge/internal/models"
)

type Handler struct {
	converter *converters.ChromeConverter
	processor *converters.PDFProcessor
	logger    *slog.Logger
	startTime time.Time
	version   string
}

func NewHandler(c *converters.ChromeConverter, p *converters.PDFProcessor, l *slog.Logger, v string) *Handler {
	return &Handler{
		converter: c,
		processor: p,
		logger:    l,
		startTime: time.Now(),
		version:   v,
	}
}

// Health reports real worker pool state and conversion counters.
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	response := models.HealthResponse{
		Status:      "healthy",
		Version:     h.version,
		Uptime:      time.Since(h.startTime).String(),
		Chrome:      "running",
		Workers:     h.converter.GetWorkerStatus(),
		Conversions: h.converter.GetMetrics(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// Convert handles the main conversion logic.
func (h *Handler) Convert(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())

	var req models.ConversionRequest
	if err := decodeJSON(r, &req); err != nil {
		h.handleDecodeError(w, err, requestID)
		return
	}

	if req.IsBase64 && req.HTML != "" {
		decoded, err := base64.StdEncoding.DecodeString(req.HTML)
		if err != nil {
			h.errorResponse(w, http.StatusBadRequest, "Invalid Base64 HTML: "+err.Error(), requestID)
			return
		}
		req.HTML = string(decoded)
	}

	start := time.Now()
	convType := string(req.Type)
	if convType == "" {
		convType = "html"
	}

	var (
		pdfData []byte
		err     error
		ctx     = r.Context()
	)

	switch req.Type {
	case models.ConvertHTML, "":
		if req.HTML == "" {
			h.errorResponse(w, http.StatusBadRequest, "HTML content required", requestID)
			return
		}
		pdfData, err = h.converter.ConvertHTML(ctx, req.HTML, req.Options)
	case models.ConvertURL:
		pdfData, err = h.converter.ConvertURL(ctx, req.URL, req.Options)
	case models.ConvertMarkdown:
		pdfData, err = h.converter.ConvertMarkdown(ctx, req.Markdown, req.Options)
	case models.ConvertImage:
		pdfData, err = h.converter.ConvertImage(ctx, req.Image, req.Options)
	case models.ConvertImages:
		pdfData, err = h.converter.ConvertImages(ctx, req.Images, req.Options)
	default:
		h.errorResponse(w, http.StatusBadRequest, "Invalid conversion type", requestID)
		return
	}

	if err != nil {
		h.logger.Error("Conversion failed", "request_id", requestID, "type", convType, "error", err)
		metrics.Record(convType, "failure", time.Since(start).Seconds(), 0)
		h.errorResponse(w, http.StatusInternalServerError, err.Error(), requestID)
		return
	}

	if req.Options != nil && h.processor != nil {
		pdfData, err = h.processor.Process(pdfData, req.Options)
		if err != nil {
			h.logger.Error("Processing failed", "request_id", requestID, "error", err)
			metrics.Record(convType, "failure", time.Since(start).Seconds(), 0)
			h.errorResponse(w, http.StatusInternalServerError, "Processing failed: "+err.Error(), requestID)
			return
		}
	}

	metrics.Record(convType, "success", time.Since(start).Seconds(), len(pdfData))

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(pdfData)))
	w.Header().Set("X-Request-ID", requestID)
	_, _ = w.Write(pdfData)
}

// ConvertHTML supports both raw-HTML bodies and JSON ConversionRequest payloads.
func (h *Handler) ConvertHTML(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(strings.NewReader(fmt.Sprintf(`{"type":"html","html":%q}`, string(body))))
	}
	h.Convert(w, r)
}

func (h *Handler) ConvertURL(w http.ResponseWriter, r *http.Request)      { h.Convert(w, r) }
func (h *Handler) ConvertMarkdown(w http.ResponseWriter, r *http.Request) { h.Convert(w, r) }
func (h *Handler) ConvertImage(w http.ResponseWriter, r *http.Request)    { h.Convert(w, r) }

// MergePDFs handles POST /merge with body { "pdfs": ["base64...", ...] }.
func (h *Handler) MergePDFs(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())

	if h.processor == nil {
		h.errorResponse(w, http.StatusServiceUnavailable, "PDF processor unavailable", requestID)
		return
	}

	var req struct {
		PDFs []string `json:"pdfs"`
	}
	if err := decodeJSON(r, &req); err != nil {
		h.handleDecodeError(w, err, requestID)
		return
	}
	if len(req.PDFs) < 2 {
		h.errorResponse(w, http.StatusBadRequest, "At least two base64-encoded PDFs are required", requestID)
		return
	}

	start := time.Now()
	decoded := make([][]byte, 0, len(req.PDFs))
	for i, s := range req.PDFs {
		data, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			h.errorResponse(w, http.StatusBadRequest, fmt.Sprintf("pdfs[%d] is not valid base64", i), requestID)
			return
		}
		decoded = append(decoded, data)
	}

	merged, err := h.processor.MergePDFs(decoded)
	if err != nil {
		h.logger.Error("Merge failed", "request_id", requestID, "error", err)
		metrics.Record("merge", "failure", time.Since(start).Seconds(), 0)
		h.errorResponse(w, http.StatusInternalServerError, "Merge failed: "+err.Error(), requestID)
		return
	}

	metrics.Record("merge", "success", time.Since(start).Seconds(), len(merged))

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(merged)))
	w.Header().Set("X-Request-ID", requestID)
	_, _ = w.Write(merged)
}

// errorResponse helper required by ExtendedHandler.
func (h *Handler) errorResponse(w http.ResponseWriter, status int, message, requestID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":      http.StatusText(status),
		"message":    message,
		"request_id": requestID,
	})
}

// decodeJSON decodes JSON while preserving wrapped MaxBytesError so callers
// can return a proper 413 instead of a misleading 400.
func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func (h *Handler) handleDecodeError(w http.ResponseWriter, err error, requestID string) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		h.errorResponse(
			w,
			http.StatusRequestEntityTooLarge,
			fmt.Sprintf("Request body exceeds limit of %d bytes", maxBytesErr.Limit),
			requestID,
		)
		return
	}
	h.errorResponse(w, http.StatusBadRequest, "Invalid JSON payload: "+err.Error(), requestID)
}
