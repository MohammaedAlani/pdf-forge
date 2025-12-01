package converters

import (
	"context"
	"encoding/base64"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"pdf-forge/internal/models"
)

// ChromeConverter handles all Chrome-based conversions
type ChromeConverter struct {
	allocCtx    context.Context
	cancelAlloc context.CancelFunc
	workerPool  chan struct{}
	maxWorkers  int

	// Metrics
	totalConversions      int64
	successfulConversions int64
	failedConversions     int64
	conversionsByType     sync.Map
}

// NewChromeConverter creates a new converter instance
func NewChromeConverter(maxWorkers int) (*ChromeConverter, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-zygote", true),
		chromedp.Flag("single-process", false),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("disable-translate", true),
	)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)

	// Warm up Chrome
	ctx, cancel := chromedp.NewContext(allocCtx)
	if err := chromedp.Run(ctx); err != nil {
		cancelAlloc()
		return nil, fmt.Errorf("failed to start Chrome: %w", err)
	}
	cancel()

	return &ChromeConverter{
		allocCtx:    allocCtx,
		cancelAlloc: cancelAlloc,
		workerPool:  make(chan struct{}, maxWorkers),
		maxWorkers:  maxWorkers,
	}, nil
}

// Close shuts down the converter
func (c *ChromeConverter) Close() {
	c.cancelAlloc()
}

// GetWorkerStatus returns current worker pool status
func (c *ChromeConverter) GetWorkerStatus() models.WorkerStatus {
	inUse := len(c.workerPool)
	return models.WorkerStatus{
		Max:       c.maxWorkers,
		Available: c.maxWorkers - inUse,
		InUse:     inUse,
	}
}

// GetMetrics returns conversion metrics
func (c *ChromeConverter) GetMetrics() models.ConversionMetrics {
	byType := make(map[string]int64)
	c.conversionsByType.Range(func(key, value interface{}) bool {
		byType[key.(string)] = value.(int64)
		return true
	})

	return models.ConversionMetrics{
		Total:      atomic.LoadInt64(&c.totalConversions),
		Successful: atomic.LoadInt64(&c.successfulConversions),
		Failed:     atomic.LoadInt64(&c.failedConversions),
		ByType:     byType,
	}
}

func (c *ChromeConverter) incrementMetric(convType string, success bool) {
	atomic.AddInt64(&c.totalConversions, 1)
	if success {
		atomic.AddInt64(&c.successfulConversions, 1)
	} else {
		atomic.AddInt64(&c.failedConversions, 1)
	}

	// Increment by type
	for {
		val, _ := c.conversionsByType.LoadOrStore(convType, int64(0))
		current := val.(int64)
		if c.conversionsByType.CompareAndSwap(convType, current, current+1) {
			break
		}
	}
}

// acquireWorker blocks until a worker slot is available
func (c *ChromeConverter) acquireWorker() {
	c.workerPool <- struct{}{}
}

// releaseWorker releases a worker slot
func (c *ChromeConverter) releaseWorker() {
	<-c.workerPool
}

// ConvertHTML converts HTML content to PDF
func (c *ChromeConverter) ConvertHTML(ctx context.Context, html string, opts *models.PDFOptions) ([]byte, error) {
	c.semaphore <- struct{}{}
	defer func() { <-c.semaphore }()

	taskCtx, cancel := chromedp.NewContext(c.allocCtx)
	defer cancel()

	// Increase total timeout to allow for network loads
	taskCtx, cancel = context.WithTimeout(taskCtx, 60*time.Second)
	defer cancel()

	var buf []byte

	// Setup dimensions
	width, height := 8.27, 11.69 // A4
	if opts != nil && opts.PageSize == "Letter" {
		width, height = 8.5, 11
	}
	if opts != nil && opts.Orientation == "landscape" {
		width, height = height, width
	}

	err := chromedp.Run(taskCtx,
		chromedp.Navigate("about:blank"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			frameTree, err := page.GetFrameTree().Do(ctx)
			if err != nil {
				return err
			}
			return page.SetDocumentContent(frameTree.Frame.ID, html).Do(ctx)
		}),
		// 1. Wait for the body to be technically present
		chromedp.WaitReady("body"),
		// 2. Wait explicitly for external scripts (Tailwind/Fonts) to render
		chromedp.Sleep(3*time.Second),
		chromedp.ActionFunc(func(ctx context.Context) error {
			printParams := page.PrintToPDF().
				WithPaperWidth(width).
				WithPaperHeight(height).
				WithPrintBackground(true)

			if opts != nil && opts.Margins != nil {
				printParams = printParams.
					WithMarginTop(opts.Margins.Top).
					WithMarginBottom(opts.Margins.Bottom).
					WithMarginLeft(opts.Margins.Left).
					WithMarginRight(opts.Margins.Right)
			} else {
				// Ensure no margins if not specified to prevent cutting off design
				printParams = printParams.
					WithMarginTop(0).
					WithMarginBottom(0).
					WithMarginLeft(0).
					WithMarginRight(0)
			}

			var err error
			buf, _, err = printParams.Do(ctx)
			return err
		}),
	)

	return buf, err
}

// ConvertURL converts a URL to PDF
func (c *ChromeConverter) ConvertURL(ctx context.Context, url string, opts *models.PDFOptions) ([]byte, error) {
	c.acquireWorker()
	defer c.releaseWorker()

	if opts == nil {
		defaults := models.DefaultOptions()
		opts = &defaults
	}

	chromeCtx, cancel := chromedp.NewContext(c.allocCtx)
	defer cancel()

	chromeCtx, cancel = context.WithTimeout(chromeCtx, 120*time.Second)
	defer cancel()

	var pdf []byte

	dims := opts.PageSize.GetDimensions()
	if opts.Orientation == models.Landscape {
		dims.Width, dims.Height = dims.Height, dims.Width
	}

	margins := models.DefaultMargins()
	if opts.Margins != nil {
		margins = *opts.Margins
	}

	scale := opts.Scale
	if scale <= 0 || scale > 2 {
		scale = 1.0
	}

	err := chromedp.Run(chromeCtx,
		chromedp.Navigate(url),
		chromedp.WaitReady("body"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			buf, _, err := page.PrintToPDF().
				WithPrintBackground(opts.PrintBackground).
				WithPaperWidth(dims.Width).
				WithPaperHeight(dims.Height).
				WithMarginTop(margins.Top).
				WithMarginBottom(margins.Bottom).
				WithMarginLeft(margins.Left).
				WithMarginRight(margins.Right).
				WithScale(scale).
				Do(ctx)
			pdf = buf
			return err
		}),
	)

	c.incrementMetric("url", err == nil)

	if err != nil {
		return nil, fmt.Errorf("URL conversion failed: %w", err)
	}

	return pdf, nil
}

// ConvertMarkdown converts Markdown to PDF via HTML
func (c *ChromeConverter) ConvertMarkdown(ctx context.Context, markdown string, opts *models.PDFOptions) ([]byte, error) {
	// Convert markdown to HTML with styling
	html := markdownToHTML(markdown)
	return c.ConvertHTML(ctx, html, opts)
}

// ConvertImage converts a single image to PDF
func (c *ChromeConverter) ConvertImage(ctx context.Context, imageBase64 string, opts *models.PDFOptions) ([]byte, error) {
	return c.ConvertImages(ctx, []string{imageBase64}, opts)
}

// ConvertImages converts multiple images to a single PDF
func (c *ChromeConverter) ConvertImages(ctx context.Context, imagesBase64 []string, opts *models.PDFOptions) ([]byte, error) {
	c.acquireWorker()
	defer c.releaseWorker()

	if opts == nil {
		defaults := models.DefaultOptions()
		opts = &defaults
	}

	// Build HTML with images
	var imagesHTML string
	for i, img := range imagesBase64 {
		// Detect image type from base64 prefix or default to jpeg
		mimeType := "image/jpeg"
		if len(img) > 30 {
			if img[0] == '/' {
				mimeType = "image/jpeg"
			} else if img[0] == 'i' {
				mimeType = "image/png"
			} else if img[0] == 'R' {
				mimeType = "image/gif"
			} else if img[0] == 'U' {
				mimeType = "image/webp"
			}
		}

		pageBreak := ""
		if i > 0 {
			pageBreak = "page-break-before: always;"
		}

		imagesHTML += fmt.Sprintf(`
			<div style="%s display:flex; justify-content:center; align-items:center; height:100vh; width:100%%;">
				<img src="data:%s;base64,%s" style="max-width:100%%; max-height:100%%; object-fit:contain;" />
			</div>
		`, pageBreak, mimeType, img)
	}

	html := fmt.Sprintf(`
		<!DOCTYPE html>
		<html>
		<head>
			<meta charset="UTF-8">
			<style>
				* { margin: 0; padding: 0; box-sizing: border-box; }
				body { font-family: Arial, sans-serif; }
				@page { margin: 0; }
			</style>
		</head>
		<body>%s</body>
		</html>
	`, imagesHTML)

	chromeCtx, cancel := chromedp.NewContext(c.allocCtx)
	defer cancel()

	chromeCtx, cancel = context.WithTimeout(chromeCtx, 120*time.Second)
	defer cancel()

	var pdf []byte

	dims := opts.PageSize.GetDimensions()
	if opts.Orientation == models.Landscape {
		dims.Width, dims.Height = dims.Height, dims.Width
	}

	err := chromedp.Run(chromeCtx,
		chromedp.Navigate("about:blank"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			tree, err := page.GetFrameTree().Do(ctx)
			if err != nil {
				return err
			}
			return page.SetDocumentContent(tree.Frame.ID, html).Do(ctx)
		}),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.ActionFunc(func(ctx context.Context) error {
			buf, _, err := page.PrintToPDF().
				WithPrintBackground(true).
				WithPaperWidth(dims.Width).
				WithPaperHeight(dims.Height).
				WithMarginTop(0).
				WithMarginBottom(0).
				WithMarginLeft(0).
				WithMarginRight(0).
				Do(ctx)
			pdf = buf
			return err
		}),
	)

	c.incrementMetric("images", err == nil)

	if err != nil {
		return nil, fmt.Errorf("image conversion failed: %w", err)
	}

	return pdf, nil
}

// markdownToHTML converts markdown to styled HTML
func markdownToHTML(md string) string {
	// Simple markdown conversion (for production, use goldmark or blackfriday)
	html := md

	// Basic styling
	styledHTML := fmt.Sprintf(`
		<!DOCTYPE html>
		<html>
		<head>
			<meta charset="UTF-8">
			<style>
				body {
					font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif;
					line-height: 1.6;
					max-width: 800px;
					margin: 0 auto;
					padding: 20px;
					color: #333;
				}
				h1, h2, h3, h4, h5, h6 { margin-top: 1.5em; margin-bottom: 0.5em; }
				h1 { font-size: 2em; border-bottom: 1px solid #eee; padding-bottom: 0.3em; }
				h2 { font-size: 1.5em; border-bottom: 1px solid #eee; padding-bottom: 0.3em; }
				code { background: #f4f4f4; padding: 2px 6px; border-radius: 3px; font-family: 'SF Mono', Monaco, monospace; }
				pre { background: #f4f4f4; padding: 16px; border-radius: 6px; overflow-x: auto; }
				pre code { background: none; padding: 0; }
				blockquote { border-left: 4px solid #ddd; margin: 0; padding-left: 16px; color: #666; }
				table { border-collapse: collapse; width: 100%%; margin: 1em 0; }
				th, td { border: 1px solid #ddd; padding: 8px 12px; text-align: left; }
				th { background: #f4f4f4; }
				img { max-width: 100%%; }
				a { color: #0066cc; }
			</style>
		</head>
		<body>
			<div class="markdown-body">%s</div>
		</body>
		</html>
	`, html)

	return styledHTML
}

// MergePDFs merges multiple PDFs into one (placeholder - needs pdfcpu)
func (c *ChromeConverter) MergePDFs(ctx context.Context, pdfsBase64 []string, opts *models.PDFOptions) ([]byte, error) {
	c.incrementMetric("merge", false)
	return nil, fmt.Errorf("PDF merge requires external library - use pdfcpu or qpdf")
}

// DecodeBase64 decodes a base64 string, handling optional data URL prefix
func DecodeBase64(encoded string) ([]byte, error) {
	// Remove data URL prefix if present
	if idx := len("data:"); len(encoded) > idx {
		if commaIdx := findComma(encoded[:100]); commaIdx > 0 {
			encoded = encoded[commaIdx+1:]
		}
	}

	return base64.StdEncoding.DecodeString(encoded)
}

func findComma(s string) int {
	for i, c := range s {
		if c == ',' {
			return i
		}
	}
	return -1
}
