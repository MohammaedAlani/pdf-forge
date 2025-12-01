package handlers

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"pdf-forge/internal/converters"
	"pdf-forge/internal/middleware"
	"pdf-forge/internal/models"
)

type Handler struct {
	// Renamed back to 'converter' to match ExtendedHandler expectations
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

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	// Simple health check
	response := models.HealthResponse{
		Status:  "healthy",
		Version: h.version,
		Uptime:  time.Since(h.startTime).String(),
		Chrome:  "running",
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Convert handles the main conversion logic
func (h *Handler) Convert(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())

	var req models.ConversionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.errorResponse(w, http.StatusBadRequest, "Invalid JSON payload", requestID)
		return
	}

	// 1. Handle Base64 Decoding if flag is set
	if req.IsBase64 && req.HTML != "" {
		decoded, err := base64.StdEncoding.DecodeString(req.HTML)
		if err != nil {
			h.errorResponse(w, http.StatusBadRequest, "Invalid Base64 HTML: "+err.Error(), requestID)
			return
		}
		req.HTML = string(decoded)
	}

	var pdfData []byte
	var err error
	ctx := r.Context()

	// 2. Route based on type
	switch req.Type {
	case models.ConvertHTML:
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
		// Default to HTML if type not specified but HTML exists
		if req.HTML != "" {
			pdfData, err = h.converter.ConvertHTML(ctx, req.HTML, req.Options)
		} else {
			h.errorResponse(w, http.StatusBadRequest, "Invalid conversion type", requestID)
			return
		}
	}

	if err != nil {
		h.logger.Error("Conversion failed", "request_id", requestID, "error", err)
		h.errorResponse(w, http.StatusInternalServerError, err.Error(), requestID)
		return
	}

	// 3. Post-processing (Security, Metadata)
	if req.Options != nil && h.processor != nil {
		pdfData, err = h.processor.Process(pdfData, req.Options)
		if err != nil {
			h.logger.Error("Processing failed", "request_id", requestID, "error", err)
			h.errorResponse(w, http.StatusInternalServerError, "Processing failed: "+err.Error(), requestID)
			return
		}
	}

	// 4. Return Response
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(pdfData)))
	w.Header().Set("X-Request-ID", requestID)
	w.Write(pdfData)
}

// Wrappers for legacy/specific endpoints
func (h *Handler) ConvertHTML(w http.ResponseWriter, r *http.Request) {
	// Handle raw HTML string or JSON
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
func (h *Handler) MergePDFs(w http.ResponseWriter, r *http.Request) {
	// Placeholder if extended handler doesn't pick it up
	h.errorResponse(w, http.StatusNotImplemented, "Use /manipulate endpoint", "")
}
func (h *Handler) Metrics(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// errorResponse helper required by ExtendedHandler
func (h *Handler) errorResponse(w http.ResponseWriter, status int, message, requestID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{
		"error":      http.StatusText(status),
		"message":    message,
		"request_id": requestID,
	})
}
