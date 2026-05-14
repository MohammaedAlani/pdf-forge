package converters

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"pdf-forge/internal/metrics"
	"pdf-forge/internal/models"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/yuin/goldmark"
)

// Defaults shared across conversion paths.
const (
	defaultHTMLTimeout = 90 * time.Second
	defaultURLTimeout  = 60 * time.Second
	htmlSettleWait     = 1500 * time.Millisecond
	urlSettleWait      = 1500 * time.Millisecond
)

// ChromeConverter manages a single shared Chrome allocator and bounds
// concurrent conversions via a semaphore. inUse tracks how many slots are
// currently busy for /health reporting.
type ChromeConverter struct {
	allocCtx    context.Context
	cancelAlloc context.CancelFunc
	semaphore   chan struct{}
	inUse       atomic.Int32
}

// NewChromeConverter creates the Chrome allocator and warms up one tab so that
// any launch failure surfaces immediately.
func NewChromeConverter(maxWorkers int) (*ChromeConverter, error) {
	if maxWorkers <= 0 {
		maxWorkers = 1
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)

	// Verify Chrome can launch — fail fast if it can't.
	warmCtx, warmCancel := context.WithTimeout(allocCtx, 20*time.Second)
	defer warmCancel()
	ctx, c := chromedp.NewContext(warmCtx)
	if err := chromedp.Run(ctx); err != nil {
		c()
		cancel()
		return nil, fmt.Errorf("chrome warm-up failed: %w", err)
	}
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

func (c *ChromeConverter) acquire() {
	c.semaphore <- struct{}{}
	c.inUse.Add(1)
	metrics.WorkersInUse.Set(float64(c.inUse.Load()))
}

func (c *ChromeConverter) release() {
	<-c.semaphore
	c.inUse.Add(-1)
	metrics.WorkersInUse.Set(float64(c.inUse.Load()))
}

// GetWorkerStatus returns the current worker pool snapshot.
func (c *ChromeConverter) GetWorkerStatus() models.WorkerStatus {
	max := cap(c.semaphore)
	inUse := int(c.inUse.Load())
	return models.WorkerStatus{
		Max:       max,
		InUse:     inUse,
		Available: max - inUse,
	}
}

// GetMetrics returns a snapshot of in-process conversion counters.
func (c *ChromeConverter) GetMetrics() models.ConversionMetrics {
	s := metrics.Counters()
	return models.ConversionMetrics{
		Total:      s.Total,
		Successful: s.Successful,
		Failed:     s.Failed,
	}
}

// dimensions resolves PageSize / Orientation / CustomDimensions to inches.
func dimensions(opts *models.PDFOptions) (width, height float64) {
	width, height = 8.27, 11.69 // A4 default
	if opts == nil {
		return
	}
	if opts.CustomDimensions != nil && opts.CustomDimensions.Width > 0 && opts.CustomDimensions.Height > 0 {
		width, height = opts.CustomDimensions.Width, opts.CustomDimensions.Height
	} else if opts.PageSize != "" {
		d := opts.PageSize.GetDimensions()
		width, height = d.Width, d.Height
	}
	if opts.Orientation == models.Landscape {
		width, height = height, width
	}
	return
}

// applyPrintOptions configures a PrintToPDF action with the shared option set.
func applyPrintOptions(p *page.PrintToPDFParams, opts *models.PDFOptions) *page.PrintToPDFParams {
	p = p.WithPrintBackground(true)
	if opts == nil {
		return p
	}
	w, h := dimensions(opts)
	p = p.WithPaperWidth(w).WithPaperHeight(h)
	if opts.Margins != nil {
		p = p.WithMarginTop(opts.Margins.Top).
			WithMarginBottom(opts.Margins.Bottom).
			WithMarginLeft(opts.Margins.Left).
			WithMarginRight(opts.Margins.Right)
	}
	if opts.Scale > 0 {
		p = p.WithScale(opts.Scale)
	}
	if opts.HeaderFooter != nil {
		hf := opts.HeaderFooter
		header := headerFooterHTML("header", hf.HeaderLeft, hf.HeaderCenter, hf.HeaderRight, hf.FontSize)
		footer := headerFooterHTML("footer", hf.FooterLeft, hf.FooterCenter, hf.FooterRight, hf.FontSize)
		p = p.WithDisplayHeaderFooter(true).
			WithHeaderTemplate(header).
			WithFooterTemplate(footer)
	}
	return p
}

// headerFooterHTML builds a Chromium DisplayHeaderFooter template.
// Empty cells are rendered as blank spans so the layout stays balanced.
func headerFooterHTML(kind, left, center, right string, fontSize float64) string {
	if fontSize <= 0 {
		fontSize = 9
	}
	// Chromium recognizes the .pageNumber / .totalPages / .title / .date classes.
	subst := func(s string) string {
		s = strings.ReplaceAll(s, "{pageNumber}", `<span class="pageNumber"></span>`)
		s = strings.ReplaceAll(s, "{totalPages}", `<span class="totalPages"></span>`)
		s = strings.ReplaceAll(s, "{title}", `<span class="title"></span>`)
		s = strings.ReplaceAll(s, "{date}", `<span class="date"></span>`)
		return s
	}
	return fmt.Sprintf(
		`<div style="font-size:%.1fpx; width:100%%; padding:0 12px; display:flex; justify-content:space-between;">
			<span>%s</span><span>%s</span><span>%s</span>
		</div>`,
		fontSize, subst(left), subst(center), subst(right),
	)
}

// ConvertHTML renders an HTML document and returns PDF bytes.
func (c *ChromeConverter) ConvertHTML(ctx context.Context, html string, opts *models.PDFOptions) ([]byte, error) {
	c.acquire()
	defer c.release()

	taskCtx, cancel := chromedp.NewContext(c.allocCtx)
	defer cancel()
	taskCtx, cancel = context.WithTimeout(taskCtx, defaultHTMLTimeout)
	defer cancel()

	actions := []chromedp.Action{
		chromedp.Navigate("about:blank"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			frameTree, err := page.GetFrameTree().Do(ctx)
			if err != nil {
				return err
			}
			return page.SetDocumentContent(frameTree.Frame.ID, html).Do(ctx)
		}),
		chromedp.WaitReady("body"),
	}

	if opts != nil && opts.Grayscale {
		actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
			return emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{
				{Name: "prefers-color-scheme", Value: "no-preference"},
			}).Do(ctx)
		}))
	}

	// Allow late-loading CSS/fonts to settle.
	actions = append(actions, chromedp.Sleep(htmlSettleWait))

	var buf []byte
	actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
		p := applyPrintOptions(page.PrintToPDF(), opts)
		var err error
		buf, _, err = p.Do(ctx)
		return err
	}))

	if err := chromedp.Run(taskCtx, actions...); err != nil {
		return nil, err
	}
	if opts != nil && opts.Grayscale {
		// Chromium's grayscale isn't directly exposed via PrintToPDF; the
		// post-process step is left to the processor (e.g. via Ghostscript).
		return buf, nil
	}
	return buf, nil
}

// ConvertURL fetches a URL and returns PDF bytes. The URL is validated against
// SSRF — private, loopback, link-local and non-HTTP(S) targets are rejected.
func (c *ChromeConverter) ConvertURL(ctx context.Context, raw string, opts *models.PDFOptions) ([]byte, error) {
	if err := ValidateURL(raw); err != nil {
		return nil, err
	}

	c.acquire()
	defer c.release()

	taskCtx, cancel := chromedp.NewContext(c.allocCtx)
	defer cancel()
	taskCtx, cancel = context.WithTimeout(taskCtx, defaultURLTimeout)
	defer cancel()

	var buf []byte
	err := chromedp.Run(taskCtx,
		chromedp.Navigate(raw),
		chromedp.WaitReady("body"),
		chromedp.Sleep(urlSettleWait),
		chromedp.ActionFunc(func(ctx context.Context) error {
			p := applyPrintOptions(page.PrintToPDF(), opts)
			var perr error
			buf, _, perr = p.Do(ctx)
			return perr
		}),
	)
	return buf, err
}

// ConvertMarkdown renders Markdown via goldmark, then converts to PDF.
func (c *ChromeConverter) ConvertMarkdown(ctx context.Context, markdown string, opts *models.PDFOptions) ([]byte, error) {
	var rendered bytes.Buffer
	if err := goldmark.Convert([]byte(markdown), &rendered); err != nil {
		return nil, fmt.Errorf("markdown rendering failed: %w", err)
	}
	html := `<!DOCTYPE html><html><head><meta charset="UTF-8"><style>
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif; padding: 40px; line-height: 1.6; color: #24292e; max-width: 900px; margin: 0 auto; }
h1, h2, h3, h4 { color: #1a202c; margin-top: 1.5em; }
h1, h2 { border-bottom: 1px solid #eaecef; padding-bottom: 0.3em; }
code { background: #f6f8fa; padding: 2px 6px; border-radius: 3px; font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace; font-size: 85%; }
pre { background: #f6f8fa; padding: 16px; border-radius: 6px; overflow-x: auto; }
pre code { background: transparent; padding: 0; font-size: 100%; }
table { border-collapse: collapse; width: 100%; margin: 16px 0; }
th, td { border: 1px solid #dfe2e5; padding: 6px 13px; }
th { background: #f6f8fa; }
blockquote { border-left: 4px solid #dfe2e5; padding: 0 1em; color: #6a737d; margin: 0; }
img { max-width: 100%; }
a { color: #0366d6; }
</style></head><body>` + rendered.String() + `</body></html>`
	return c.ConvertHTML(ctx, html, opts)
}

// detectImageMIME detects the MIME type from base64-encoded image data.
func detectImageMIME(b64 string) string {
	if data, err := base64.StdEncoding.DecodeString(b64); err == nil && len(data) > 0 {
		return http.DetectContentType(data)
	}
	return "image/png"
}

// ConvertImage wraps a single image in HTML and converts.
func (c *ChromeConverter) ConvertImage(ctx context.Context, imgBase64 string, opts *models.PDFOptions) ([]byte, error) {
	mime := detectImageMIME(imgBase64)
	html := fmt.Sprintf(`<html><body style="margin:0;display:flex;justify-content:center;align-items:center;height:100vh;">
<img src="data:%s;base64,%s" style="max-width:100%%;max-height:100%%;" />
</body></html>`, mime, imgBase64)
	return c.ConvertHTML(ctx, html, opts)
}

// ConvertImages renders multiple images, one per page, separated by page breaks.
func (c *ChromeConverter) ConvertImages(ctx context.Context, imgs []string, opts *models.PDFOptions) ([]byte, error) {
	if len(imgs) == 0 {
		return nil, fmt.Errorf("no images provided")
	}
	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html><html><head><style>
.page { display:flex; justify-content:center; align-items:center; height:100vh; }
.page:not(:last-child) { page-break-after: always; }
img { max-width:100%; max-height:100%; }
</style></head><body style="margin:0;">`)
	for _, img := range imgs {
		mime := detectImageMIME(img)
		fmt.Fprintf(&sb, `<div class="page"><img src="data:%s;base64,%s" /></div>`, mime, img)
	}
	sb.WriteString(`</body></html>`)
	return c.ConvertHTML(ctx, sb.String(), opts)
}

// ValidateURL rejects URLs that would let an external caller pivot to internal
// services (loopback, RFC1918, link-local, IPv6 ULA, IMDS metadata, etc.) or
// load non-HTTP schemes such as file://, data:, ftp:, etc.
func ValidateURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http(s) URLs are allowed (got %q)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("url must include a host")
	}
	// Block raw IP literals that are obviously internal.
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return fmt.Errorf("refusing to fetch %s: address is internal/private", ip)
		}
		return nil
	}
	// Resolve hostname and reject if ANY resolved IP is internal.
	addrs, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("dns lookup failed for %s: %w", host, err)
	}
	for _, ip := range addrs {
		if isBlockedIP(ip) {
			return fmt.Errorf("refusing to fetch %s: resolves to internal address %s", host, ip)
		}
	}
	return nil
}

func isBlockedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	// AWS / GCP / Azure instance metadata.
	if ip.Equal(net.IPv4(169, 254, 169, 254)) {
		return true
	}
	return false
}
