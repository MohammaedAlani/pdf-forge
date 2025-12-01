package converters

import (
	"context"
	"fmt"
	"time"

	"pdf-forge/internal/models"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

type ChromeConverter struct {
	allocCtx    context.Context
	cancelAlloc context.CancelFunc
	semaphore   chan struct{}
}

func NewChromeConverter(maxWorkers int) (*ChromeConverter, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)

	// Warm up
	ctx, c := chromedp.NewContext(allocCtx)
	chromedp.Run(ctx)
	c()

	return &ChromeConverter{
		allocCtx:    allocCtx,
		cancelAlloc: cancel,
		semaphore:   make(chan struct{}, maxWorkers),
	}, nil
}

func (c *ChromeConverter) Close() {
	c.cancelAlloc()
}

// GetWorkerStatus is required by Health handler
func (c *ChromeConverter) GetWorkerStatus() models.WorkerStatus {
	return models.WorkerStatus{Max: cap(c.semaphore), InUse: len(c.semaphore)}
}

// GetMetrics is required by Health handler
func (c *ChromeConverter) GetMetrics() models.ConversionMetrics {
	return models.ConversionMetrics{}
}

func (c *ChromeConverter) ConvertHTML(ctx context.Context, html string, opts *models.PDFOptions) ([]byte, error) {
	c.semaphore <- struct{}{}
	defer func() { <-c.semaphore }()

	taskCtx, cancel := chromedp.NewContext(c.allocCtx)
	defer cancel()
	taskCtx, cancel = context.WithTimeout(taskCtx, 90*time.Second) // Increased timeout
	defer cancel()

	var buf []byte

	width, height := 8.27, 11.69
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
		chromedp.WaitReady("body"),
		// WAITING FOR TAILWIND CSS
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
			}
			var err error
			buf, _, err = printParams.Do(ctx)
			return err
		}),
	)
	return buf, err
}

func (c *ChromeConverter) ConvertURL(ctx context.Context, url string, opts *models.PDFOptions) ([]byte, error) {
	c.semaphore <- struct{}{}
	defer func() { <-c.semaphore }()

	taskCtx, cancel := chromedp.NewContext(c.allocCtx)
	defer cancel()
	taskCtx, cancel = context.WithTimeout(taskCtx, 60*time.Second)
	defer cancel()

	var buf []byte
	err := chromedp.Run(taskCtx,
		chromedp.Navigate(url),
		chromedp.WaitReady("body"),
		chromedp.Sleep(2*time.Second),
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, _, err := page.PrintToPDF().WithPrintBackground(true).Do(ctx)
			return err
		}),
	)
	return buf, err
}

// ConvertMarkdown wraps markdown in HTML styles and converts
func (c *ChromeConverter) ConvertMarkdown(ctx context.Context, markdown string, opts *models.PDFOptions) ([]byte, error) {
	// Simple wrapper style
	html := fmt.Sprintf(`<html>
		<head><style>body { font-family: sans-serif; padding: 20px; }</style></head>
		<body><pre>%s</pre></body>
		</html>`, markdown)
	return c.ConvertHTML(ctx, html, opts)
}

// ConvertImage wraps an image in HTML and converts
func (c *ChromeConverter) ConvertImage(ctx context.Context, imgBase64 string, opts *models.PDFOptions) ([]byte, error) {
	html := fmt.Sprintf(`<html><body style="margin:0;display:flex;justify-content:center;align-items:center;">
		<img src="data:image;base64,%s" style="max-width:100%%;max-height:100%%;" />
		</body></html>`, imgBase64)
	return c.ConvertHTML(ctx, html, opts)
}

func (c *ChromeConverter) ConvertImages(ctx context.Context, imgs []string, opts *models.PDFOptions) ([]byte, error) {
	// Basic implementation taking first image for now to satisfy interface
	if len(imgs) > 0 {
		return c.ConvertImage(ctx, imgs[0], opts)
	}
	return nil, fmt.Errorf("no images provided")
}
