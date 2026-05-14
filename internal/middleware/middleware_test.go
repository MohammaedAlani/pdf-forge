package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRateLimiter_ConcurrentSafe(t *testing.T) {
	rl := NewRateLimiter(1000, time.Second)
	handler := rl.Limit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	const goroutines = 32
	const perGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.RemoteAddr = "10.0.0.1:1234" // same IP — exercises map write race
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
			}
		}(i)
	}
	wg.Wait()
	// No panic = pass.
}

func TestAPIKeyAuth_AllowsHealthAndMetrics(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := APIKeyAuth("secret")(next)

	for _, path := range []string{"/health", "/healthz", "/metrics"} {
		called = false
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, w.Code)
		}
		if !called {
			t.Errorf("%s: next handler not called", path)
		}
	}
}

func TestAPIKeyAuth_BlocksMissingKey(t *testing.T) {
	handler := APIKeyAuth("secret")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("next handler should not be called")
	}))
	req := httptest.NewRequest(http.MethodPost, "/convert", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestAPIKeyAuth_AcceptsValidKey(t *testing.T) {
	called := false
	handler := APIKeyAuth("secret")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/convert", nil)
	req.Header.Set("X-API-Key", "secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !called {
		t.Fatal("next handler not called")
	}
}
