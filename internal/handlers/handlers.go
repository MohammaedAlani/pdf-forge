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

// Handler holds dependencies for HTTP handlers
type Handler struct {
	converter *converters.ChromeConverter
	processor *converters.PDFProcessor
	logger    *slog.Logger
	startTime time.Time
	version   string
}

// NewHandler creates a new handler instance
func NewHandler(converter *converters.ChromeConverter, processor *converters.PDFProcessor, logger *slog.Logger, version string) *Handler {
	return &Handler{
		converter: converter,
		processor: processor,
		logger:    logger,
		startTime: time.Now(),
		version:   version,
	}
}

// Health returns service health status
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(h.startTime).Round(time.Second)

	response := models.HealthResponse{
		Status:      "healthy",
		Version:     h.version,
		Uptime:      uptime.String(),
		Workers:     h.converter.GetWorkerStatus(),
		Chrome:      "running",
		Conversions: h.converter.GetMetrics(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Metrics returns Prometheus-compatible metrics
func (h *Handler) Metrics(w http.ResponseWriter, r *http.Request) {
	metrics := h.converter.GetMetrics()
	workers := h.converter.GetWorkerStatus()

	fmt.Fprintf(w, "# HELP pdf_forge_conversions_total Total number of conversions\n")
	fmt.Fprintf(w, "# TYPE pdf_forge_conversions_total counter\n")
	fmt.Fprintf(w, "pdf_forge_conversions_total %d\n", metrics.Total)

	fmt.Fprintf(w, "# HELP pdf_forge_conversions_successful Total successful conversions\n")
	fmt.Fprintf(w, "# TYPE pdf_forge_conversions_successful counter\n")
	fmt.Fprintf(w, "pdf_forge_conversions_successful %d\n", metrics.Successful)

	fmt.Fprintf(w, "# HELP pdf_forge_conversions_failed Total failed conversions\n")
	fmt.Fprintf(w, "# TYPE pdf_forge_conversions_failed counter\n")
	fmt.Fprintf(w, "pdf_forge_conversions_failed %d\n", metrics.Failed)

	fmt.Fprintf(w, "# HELP pdf_forge_workers_available Available workers\n")
	fmt.Fprintf(w, "# TYPE pdf_forge_workers_available gauge\n")
	fmt.Fprintf(w, "pdf_forge_workers_available %d\n", workers.Available)

	fmt.Fprintf(w, "# HELP pdf_forge_workers_in_use Workers currently in use\n")
	fmt.Fprintf(w, "# TYPE pdf_forge_workers_in_use gauge\n")
	fmt.Fprintf(w, "pdf_forge_workers_in_use %d\n", workers.InUse)

	for convType, count := range metrics.ByType {
		fmt.Fprintf(w, "# HELP pdf_forge_conversions_by_type_%s Conversions of type %s\n", convType, convType)
		fmt.Fprintf(w, "# TYPE pdf_forge_conversions_by_type_%s counter\n", convType)
		fmt.Fprintf(w, "pdf_forge_conversions_by_type_%s %d\n", convType, count)
	}
}

// Convert handles all conversion requests via unified endpoint
func (h *Handler) Convert(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())

	var req models.ConversionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.errorResponse(w, http.StatusBadRequest, "Invalid JSON payload", requestID)
		return
	}

	var pdfData []byte
	var err error

	ctx := r.Context()

	switch req.Type {
	case models.ConvertHTML:
		pdfData, err = h.convertHTML(&req)
	case models.ConvertURL:
		pdfData, err = h.converter.ConvertURL(ctx, req.URL, req.Options)
	case models.ConvertMarkdown:
		pdfData, err = h.converter.ConvertMarkdown(ctx, req.Markdown, req.Options)
	case models.ConvertImage:
		pdfData, err = h.converter.ConvertImage(ctx, req.Image, req.Options)
	case models.ConvertImages:
		pdfData, err = h.converter.ConvertImages(ctx, req.Images, req.Options)
	case models.ConvertMerge:
		pdfData, err = h.mergePDFs(&req)
	default:
		h.errorResponse(w, http.StatusBadRequest, "Invalid conversion type. Supported: html, url, markdown, image, images, merge", requestID)
		return
	}

	if err != nil {
		h.logger.Error("Conversion failed",
			"request_id", requestID,
			"type", req.Type,
			"error", err.Error(),
		)
		h.errorResponse(w, http.StatusInternalServerError, err.Error(), requestID)
		return
	}

	// Apply post-processing (security, watermark, etc.)
	if req.Options != nil && h.processor != nil {
		pdfData, err = h.processor.Process(pdfData, req.Options)
		if err != nil {
			h.logger.Error("Post-processing failed",
				"request_id", requestID,
				"error", err.Error(),
			)
			h.errorResponse(w, http.StatusInternalServerError, "Post-processing failed: "+err.Error(), requestID)
			return
		}
	}

	h.logger.Info("Conversion successful",
		"request_id", requestID,
		"type", req.Type,
		"size_bytes", len(pdfData),
	)

	// Set response headers
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(pdfData)))
	w.Header().Set("Content-Disposition", "attachment; filename=document.pdf")
	w.Header().Set("X-Request-ID", requestID)

	w.Write(pdfData)
}

// ConvertHTML handles direct HTML to PDF conversion (legacy endpoint)
func (h *Handler) ConvertHTML(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())

	var htmlContent string
	var opts *models.PDFOptions

	contentType := r.Header.Get("Content-Type")

	if strings.Contains(contentType, "application/json") {
		var req models.ConversionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.errorResponse(w, http.StatusBadRequest, "Invalid JSON payload", requestID)
			return
		}

		if req.IsBase64 {
			decoded, err := base64.StdEncoding.DecodeString(req.HTML)
			if err != nil {
				h.errorResponse(w, http.StatusBadRequest, "Invalid Base64 string", requestID)
				return
			}
			htmlContent = string(decoded)
		} else {
			htmlContent = req.HTML
		}
		opts = req.Options
	} else {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			h.errorResponse(w, http.StatusInternalServerError, "Failed to read body", requestID)
			return
		}
		htmlContent = string(bodyBytes)
	}

	if len(htmlContent) == 0 {
		h.errorResponse(w, http.StatusBadRequest, "Empty HTML content", requestID)
		return
	}

	pdfData, err := h.converter.ConvertHTML(r.Context(), htmlContent, opts)
	if err != nil {
		h.logger.Error("HTML conversion failed",
			"request_id", requestID,
			"error", err.Error(),
		)
		h.errorResponse(w, http.StatusInternalServerError, "Failed to render PDF: "+err.Error(), requestID)
		return
	}

	// Apply post-processing
	if opts != nil && h.processor != nil {
		pdfData, err = h.processor.Process(pdfData, opts)
		if err != nil {
			h.logger.Error("Post-processing failed",
				"request_id", requestID,
				"error", err.Error(),
			)
			h.errorResponse(w, http.StatusInternalServerError, "Post-processing failed: "+err.Error(), requestID)
			return
		}
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(pdfData)))
	w.Write(pdfData)
}

// ConvertURL handles URL to PDF conversion
func (h *Handler) ConvertURL(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())

	var req struct {
		URL     string            `json:"url"`
		Options *models.PDFOptions `json:"options,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.errorResponse(w, http.StatusBadRequest, "Invalid JSON payload", requestID)
		return
	}

	if req.URL == "" {
		h.errorResponse(w, http.StatusBadRequest, "URL is required", requestID)
		return
	}

	pdfData, err := h.converter.ConvertURL(r.Context(), req.URL, req.Options)
	if err != nil {
		h.logger.Error("URL conversion failed",
			"request_id", requestID,
			"url", req.URL,
			"error", err.Error(),
		)
		h.errorResponse(w, http.StatusInternalServerError, "Failed to render PDF: "+err.Error(), requestID)
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

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(pdfData)))
	w.Write(pdfData)
}

// ConvertImage handles image(s) to PDF conversion
func (h *Handler) ConvertImage(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())

	var req struct {
		Image   string            `json:"image,omitempty"`
		Images  []string          `json:"images,omitempty"`
		Options *models.PDFOptions `json:"options,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.errorResponse(w, http.StatusBadRequest, "Invalid JSON payload", requestID)
		return
	}

	var pdfData []byte
	var err error

	if len(req.Images) > 0 {
		pdfData, err = h.converter.ConvertImages(r.Context(), req.Images, req.Options)
	} else if req.Image != "" {
		pdfData, err = h.converter.ConvertImage(r.Context(), req.Image, req.Options)
	} else {
		h.errorResponse(w, http.StatusBadRequest, "Image or images array is required", requestID)
		return
	}

	if err != nil {
		h.logger.Error("Image conversion failed",
			"request_id", requestID,
			"error", err.Error(),
		)
		h.errorResponse(w, http.StatusInternalServerError, "Failed to render PDF: "+err.Error(), requestID)
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

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(pdfData)))
	w.Write(pdfData)
}

// ConvertMarkdown handles Markdown to PDF conversion
func (h *Handler) ConvertMarkdown(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())

	var req struct {
		Markdown string            `json:"markdown"`
		Options  *models.PDFOptions `json:"options,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.errorResponse(w, http.StatusBadRequest, "Invalid JSON payload", requestID)
		return
	}

	if req.Markdown == "" {
		h.errorResponse(w, http.StatusBadRequest, "Markdown content is required", requestID)
		return
	}

	pdfData, err := h.converter.ConvertMarkdown(r.Context(), req.Markdown, req.Options)
	if err != nil {
		h.logger.Error("Markdown conversion failed",
			"request_id", requestID,
			"error", err.Error(),
		)
		h.errorResponse(w, http.StatusInternalServerError, "Failed to render PDF: "+err.Error(), requestID)
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

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(pdfData)))
	w.Write(pdfData)
}

// MergePDFs handles PDF merging
func (h *Handler) MergePDFs(w http.ResponseWriter, r *http.Request) {
	requestID := middleware.GetRequestID(r.Context())

	var req struct {
		PDFs    []string          `json:"pdfs"`
		Options *models.PDFOptions `json:"options,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.errorResponse(w, http.StatusBadRequest, "Invalid JSON payload", requestID)
		return
	}

	if len(req.PDFs) < 2 {
		h.errorResponse(w, http.StatusBadRequest, "At least 2 PDFs required for merge", requestID)
		return
	}

	// Decode all PDFs
	var pdfBytes [][]byte
	for i, pdfBase64 := range req.PDFs {
		decoded, err := base64.StdEncoding.DecodeString(pdfBase64)
		if err != nil {
			h.errorResponse(w, http.StatusBadRequest, fmt.Sprintf("Invalid Base64 for PDF %d", i+1), requestID)
			return
		}
		pdfBytes = append(pdfBytes, decoded)
	}

	if h.processor == nil {
		h.errorResponse(w, http.StatusInternalServerError, "PDF processor not available", requestID)
		return
	}

	pdfData, err := h.processor.MergePDFs(pdfBytes)
	if err != nil {
		h.logger.Error("PDF merge failed",
			"request_id", requestID,
			"count", len(req.PDFs),
			"error", err.Error(),
		)
		h.errorResponse(w, http.StatusInternalServerError, "Failed to merge PDFs: "+err.Error(), requestID)
		return
	}

	// Apply post-processing
	if req.Options != nil {
		pdfData, err = h.processor.Process(pdfData, req.Options)
		if err != nil {
			h.errorResponse(w, http.StatusInternalServerError, "Post-processing failed: "+err.Error(), requestID)
			return
		}
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(pdfData)))
	w.Write(pdfData)
}

// Helper methods

func (h *Handler) convertHTML(req *models.ConversionRequest) ([]byte, error) {
	htmlContent := req.HTML
	if req.IsBase64 {
		decoded, err := base64.StdEncoding.DecodeString(req.HTML)
		if err != nil {
			return nil, fmt.Errorf("invalid Base64 string: %w", err)
		}
		htmlContent = string(decoded)
	}

	if len(htmlContent) == 0 {
		return nil, fmt.Errorf("empty HTML content")
	}

	return h.converter.ConvertHTML(nil, htmlContent, req.Options)
}

func (h *Handler) mergePDFs(req *models.ConversionRequest) ([]byte, error) {
	if len(req.PDFs) < 2 {
		return nil, fmt.Errorf("at least 2 PDFs required for merge")
	}

	var pdfBytes [][]byte
	for i, pdfBase64 := range req.PDFs {
		decoded, err := base64.StdEncoding.DecodeString(pdfBase64)
		if err != nil {
			return nil, fmt.Errorf("invalid Base64 for PDF %d: %w", i+1, err)
		}
		pdfBytes = append(pdfBytes, decoded)
	}

	if h.processor == nil {
		return nil, fmt.Errorf("PDF processor not available")
	}

	return h.processor.MergePDFs(pdfBytes)
}

func (h *Handler) errorResponse(w http.ResponseWriter, status int, message, requestID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{
		"error":      http.StatusText(status),
		"message":    message,
		"request_id": requestID,
	})
}
