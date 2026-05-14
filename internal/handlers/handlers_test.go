package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pdf-forge/internal/middleware"
)

// newHandlerForTest builds a handler with nil converter/processor for tests
// that exercise only request validation, not actual PDF generation.
func newHandlerForTest() *Handler {
	return &Handler{
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		version: "test",
	}
}

func TestHealth(t *testing.T) {
	// Health needs a real converter to call GetWorkerStatus/GetMetrics, so we
	// skip it here. Other behavior is covered by manual integration runs.
	t.Skip("requires live ChromeConverter")
}

func TestConvert_RejectsInvalidJSON(t *testing.T) {
	h := newHandlerForTest()
	req := httptest.NewRequest(http.MethodPost, "/convert", strings.NewReader("not json"))
	w := httptest.NewRecorder()

	h.Convert(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !strings.Contains(body["message"], "Invalid JSON") {
		t.Errorf("body message = %q, want it to mention Invalid JSON", body["message"])
	}
}

func TestConvert_RejectsMissingHTML(t *testing.T) {
	h := newHandlerForTest()
	body := bytes.NewBufferString(`{"type":"html"}`)
	req := httptest.NewRequest(http.MethodPost, "/convert", body)
	w := httptest.NewRecorder()

	h.Convert(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestConvert_RejectsUnknownType(t *testing.T) {
	h := newHandlerForTest()
	body := bytes.NewBufferString(`{"type":"banana"}`)
	req := httptest.NewRequest(http.MethodPost, "/convert", body)
	w := httptest.NewRecorder()

	h.Convert(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestMergePDFs_RejectsTooFew(t *testing.T) {
	h := newHandlerForTest()
	body := bytes.NewBufferString(`{"pdfs":["YQ=="]}`)
	req := httptest.NewRequest(http.MethodPost, "/merge", body)
	w := httptest.NewRecorder()

	h.MergePDFs(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d (no processor wired in test)", w.Code, http.StatusServiceUnavailable)
	}
}

func TestMaxBodySize_Returns413(t *testing.T) {
	h := newHandlerForTest()
	// Build a request body larger than the 100-byte cap installed by middleware.
	big := strings.Repeat("a", 500)
	body := bytes.NewBufferString(`{"type":"html","html":"` + big + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/convert", body)
	w := httptest.NewRecorder()

	// Wrap with the MaxBodySize middleware at 100 bytes.
	chain := middleware.MaxBodySize(100)(http.HandlerFunc(h.Convert))
	chain.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}
