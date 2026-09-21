package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"pdf-forge/internal/converters"
	"pdf-forge/internal/middleware"
	"pdf-forge/internal/models"
)

func TestAsyncRetainsAdmissionThroughDelivery(t *testing.T) {
	entered := make(chan struct{})
	unblock := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-unblock:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(unblock)
	h, err := NewExtendedHandler(NewHandler(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), "test"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	req := models.AsyncRequest{Request: models.ConversionRequest{Type: "unsupported"}, Webhook: &models.WebhookConfig{URL: server.URL}}
	body, _ := json.Marshal(req)
	handler := middleware.Admission(1, 1024, 1024, time.Second)(http.HandlerFunc(h.Async))
	call := func() int {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("POST", "/async", bytes.NewReader(body)))
		return w.Code
	}
	if status := call(); status != 202 {
		t.Fatal(status)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("callback not started")
	}
	if len(h.asyncSlots) != 0 {
		t.Fatal("callback still holds conversion slot")
	}
	if status := call(); status != 503 {
		t.Fatalf("admission released too early: %d", status)
	}
}

func TestBatchPreservesOrderAndReportsInvalidBase64(t *testing.T) {
	if os.Getenv("PDFFORGE_CHROME_TESTS") != "1" {
		t.Skip("requires Chrome")
	}
	c, err := converters.NewChromeConverter(2)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	h, err := NewExtendedHandler(NewHandler(c, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), "test"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	req := models.BatchRequest{Requests: []models.ConversionRequest{
		{HTML: "<body>First</body>"}, {HTML: "%%%", IsBase64: true}, {HTML: "<body>Third</body>"},
	}}
	body, _ := json.Marshal(req)
	w := httptest.NewRecorder()
	h.Batch(w, httptest.NewRequest("POST", "/batch", bytes.NewReader(body)))
	var result models.BatchResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Completed != 2 || result.Failed != 1 {
		t.Fatalf("%+v", result)
	}
	for i, item := range result.Results {
		if item.Index != i {
			t.Fatal("order changed")
		}
		if i != 1 {
			pdf, err := base64.StdEncoding.DecodeString(item.PDF)
			if err != nil || !bytes.HasPrefix(pdf, []byte("%PDF-")) {
				t.Fatal("invalid PDF")
			}
		}
	}
}

func TestBatchLimitBeforeConversion(t *testing.T) {
	h := &ExtendedHandler{Handler: newHandlerForTest()}
	body, _ := json.Marshal(models.BatchRequest{Requests: make([]models.ConversionRequest, 33)})
	w := httptest.NewRecorder()
	h.Batch(w, httptest.NewRequest("POST", "/batch", bytes.NewReader(body)))
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}
