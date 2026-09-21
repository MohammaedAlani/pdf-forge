package converters

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"pdf-forge/internal/models"
)

func TestAcquireCancellation(t *testing.T) {
	alloc, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &ChromeConverter{allocCtx: alloc, semaphore: make(chan struct{}, 1)}
	if err := c.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if err := c.acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	c.release()
	if c.GetWorkerStatus().InUse != 0 {
		t.Fatal("slot leaked")
	}
}

func TestImageMIMEPrefix(t *testing.T) {
	data := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 1<<20)...)
	if got := detectImageMIME(base64.StdEncoding.EncodeToString(data)); got != "image/png" {
		t.Fatal(got)
	}
}

func TestChromeLifecycle(t *testing.T) {
	if os.Getenv("PDFFORGE_CHROME_TESTS") != "1" {
		t.Skip("set PDFFORGE_CHROME_TESTS=1 for browser integration")
	}
	c, err := NewChromeConverter(2)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	owner, _ := c.browser()
	browser := chromedp.FromContext(owner).Browser
	for i := 0; i < 2; i++ {
		pdf, err := c.ConvertHTML(context.Background(), "<html><body><h1>Reusable browser</h1></body></html>", nil)
		if err != nil || !strings.HasPrefix(string(pdf), "%PDF-") {
			t.Fatalf("render: %v", err)
		}
	}
	current, _ := c.browser()
	if chromedp.FromContext(current).Browser != browser {
		t.Fatal("browser was not reused")
	}

	// Readiness must wait for asynchronous application content.
	pdf, err := c.ConvertHTML(context.Background(), `<html><body><script>setTimeout(()=>{document.body.textContent='Ready';window.ready=true},100)</script></body></html>`, &models.PDFOptions{WaitForExpression: "window.ready === true"})
	if err != nil || len(pdf) == 0 {
		t.Fatalf("readiness: %v", err)
	}

	// Active cancellation must return promptly and leave the shared browser usable.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	start := time.Now()
	_, err = c.ConvertHTML(ctx, "<body>Waiting</body>", &models.PDFOptions{WaitForExpression: "false"})
	cancel()
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("cancellation: %v after %s", err, time.Since(start))
	}
	if !c.Healthy() || c.GetWorkerStatus().InUse != 0 {
		t.Fatal("cancellation damaged browser or leaked a slot")
	}

	// Jobs must not share cookies even though they share the browser process.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<body>Cookies</body>")) }))
	defer server.Close()
	first, closeFirst, err := c.task(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(first, chromedp.Navigate(server.URL), chromedp.Evaluate(`document.cookie='job=one'`, nil)); err != nil {
		t.Fatal(err)
	}
	second, closeSecond, err := c.task(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var cookies string
	if err := chromedp.Run(second, chromedp.Navigate(server.URL), chromedp.Evaluate(`document.cookie`, &cookies)); err != nil {
		t.Fatal(err)
	}
	closeFirst()
	closeSecond()
	if cookies != "" {
		t.Fatalf("cookies leaked: %s", cookies)
	}

	// Subsequent requests recover after browser process failure.
	if err := browser.Process().Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-browser.LostConnection:
	case <-time.After(5 * time.Second):
		t.Fatal("browser did not exit")
	}
	pdf, err = c.ConvertHTML(context.Background(), "<body>Recovered</body>", nil)
	if err != nil || len(pdf) == 0 {
		t.Fatalf("recovery: %v", err)
	}
}

func TestChromeWaitsForImage(t *testing.T) {
	if os.Getenv("PDFFORGE_CHROME_TESTS") != "1" {
		t.Skip("requires Chrome")
	}
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=")
	loaded := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/image.png" {
			w.Write([]byte("<body>Image test</body>"))
			return
		}
		time.Sleep(150 * time.Millisecond)
		w.Header().Set("Content-Type", "image/png")
		w.Write(png)
		loaded <- struct{}{}
	}))
	defer server.Close()
	c, err := NewChromeConverter(1)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	task, closeTask, err := c.task(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer closeTask()
	// Use a same-origin fixture: modern Chrome blocks public/opaque pages from
	// loading loopback assets. This must not require relaxing browser security.
	var pdf []byte
	err = chromedp.Run(task, chromedp.Navigate(server.URL),
		chromedp.Evaluate(`document.body.innerHTML='<img loading="lazy" src="/image.png">'`, nil),
		documentReady(nil), chromedp.ActionFunc(func(ctx context.Context) error { var err error; pdf, err = printPDF(ctx, nil); return err }))
	if err != nil || len(pdf) == 0 {
		t.Fatalf("render: %v", err)
	}
	select {
	case <-loaded:
	default:
		t.Fatal("render finished before image loaded")
	}
}
