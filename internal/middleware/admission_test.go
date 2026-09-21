package middleware

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type unreadBody struct{ t *testing.T }

func (b unreadBody) Read([]byte) (int, error) {
	b.t.Error("rejected request body was read")
	return 0, io.EOF
}
func (b unreadBody) Close() error { return nil }

func TestAdmissionRetainsBackgroundReservation(t *testing.T) {
	var release func()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			release = RetainAdmission(r.Context())
		}
	})
	h := Admission(1, 16, 16, time.Second)(next)
	request := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/async", strings.NewReader(body)))
		return w
	}
	if w := request("payload"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	r := httptest.NewRequest(http.MethodPost, "/html", nil)
	r.Body = unreadBody{t}
	r.ContentLength = 1
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	if w.Code != 200 {
		t.Fatal("health should bypass admission")
	}
	release()
	release()
	if w := request("again"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	release()
}

func TestAdmissionByteBudgetAndBodyLimit(t *testing.T) {
	var release func()
	h := Admission(10, 10, 12, time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { release = RetainAdmission(r.Context()) }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader("12345678")))
	for _, tc := range []struct {
		size int64
		want int
	}{{5, 503}, {-1, 503}, {11, 413}} {
		r := httptest.NewRequest("POST", "/", nil)
		r.Body = unreadBody{t}
		r.ContentLength = tc.size
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("size %d: got %d", tc.size, w.Code)
		}
	}
	release()
}

func TestAdmissionDeadline(t *testing.T) {
	h := Admission(1, 10, 10, 10*time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		if r.Context().Err() != context.DeadlineExceeded {
			t.Fatal(r.Context().Err())
		}
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/", nil))
}

func TestRateLimiterSharesClientPorts(t *testing.T) {
	h := NewRateLimiter(1, time.Minute).Limit(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for i, addr := range []string{"192.0.2.1:1000", "192.0.2.1:2000"} {
		r := httptest.NewRequest("POST", "/html", nil)
		r.RemoteAddr = addr
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if i == 0 && w.Code != 200 || i == 1 && w.Code != 429 {
			t.Fatal(w.Code)
		}
	}
}
