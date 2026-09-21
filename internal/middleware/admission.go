package middleware

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"pdf-forge/internal/metrics"
)

type admissionKey struct{}
type reservation struct {
	refs    atomic.Int32
	release func()
}

// RetainAdmission keeps a request's input reservation until background work
// releases it. The returned release function is safe to call more than once.
func RetainAdmission(ctx context.Context) func() {
	r, ok := ctx.Value(admissionKey{}).(*reservation)
	if !ok {
		return func() {}
	}
	r.refs.Add(1)
	var once sync.Once
	return func() {
		once.Do(func() {
			if r.refs.Add(-1) == 0 {
				r.release()
			}
		})
	}
}

// Admission rejects excess work before reading a body. Unknown-length bodies
// reserve the full per-request limit. The budget covers input bytes, not Chrome
// memory or output expansion, which require separate bounds.
func Admission(maxRequests int, maxBody, maxBytes int64, timeout time.Duration) func(http.Handler) http.Handler {
	var mu sync.Mutex
	var active int
	var reserved int64
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				next.ServeHTTP(w, r)
				return
			}
			if r.ContentLength > maxBody {
				http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			size := r.ContentLength
			if size < 0 {
				size = maxBody
			}
			mu.Lock()
			if active >= maxRequests || size > maxBytes-reserved {
				mu.Unlock()
				metrics.AdmissionRejected.Inc()
				w.Header().Set("Retry-After", "1")
				http.Error(w, "Server capacity exhausted; retry later", http.StatusServiceUnavailable)
				return
			}
			active++
			reserved += size
			metrics.AdmissionBytes.Add(float64(size))
			mu.Unlock()
			lease := &reservation{release: func() {
				mu.Lock()
				active--
				reserved -= size
				mu.Unlock()
				metrics.AdmissionBytes.Sub(float64(size))
			}}
			lease.refs.Store(1)
			defer func() {
				if lease.refs.Add(-1) == 0 {
					lease.release()
				}
			}()
			ctx, cancel := context.WithTimeout(context.WithValue(r.Context(), admissionKey{}, lease), timeout)
			defer cancel()
			deadline, _ := ctx.Deadline()
			controller := http.NewResponseController(w)
			_ = controller.SetReadDeadline(deadline)
			defer controller.SetReadDeadline(time.Time{})
			// Enforce the reserved amount even for custom clients or middleware bodies.
			r.Body = http.MaxBytesReader(w, r.Body, size)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
