package converters

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"pdf-forge/internal/limits"
	"pdf-forge/internal/metrics"
	"pdf-forge/internal/models"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	cdpio "github.com/chromedp/cdproto/io"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/yuin/goldmark"
)

// Defaults shared across conversion paths.
const (
	defaultHTMLTimeout = 90 * time.Second
	defaultURLTimeout  = 60 * time.Second
)

// ChromeConverter reuses a browser, with isolated browser contexts per job.
// The semaphore bounds active tabs; HTTP admission bounds waiting requests.
type ChromeConverter struct {
	allocCtx      context.Context
	cancelAlloc   context.CancelFunc
	browserMu     sync.Mutex
	browserCtx    context.Context
	cancelBrowser context.CancelFunc
	semaphore     chan struct{}
	inUse         atomic.Int32
}

func NewChromeConverter(maxWorkers int) (*ChromeConverter, error) {
	if maxWorkers <= 0 {
		maxWorkers = 1
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true), chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true), chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", os.Getenv("CHROME_USE_DEV_SHM") != "true"))
	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	c := &ChromeConverter{allocCtx: allocCtx, cancelAlloc: cancel, semaphore: make(chan struct{}, maxWorkers)}
	if _, err := c.browser(); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// browser replaces a crashed browser for subsequent jobs. Active jobs fail and
// are never silently retried, since navigating a URL may have side effects.
func (c *ChromeConverter) browser() (context.Context, error) {
	c.browserMu.Lock()
	defer c.browserMu.Unlock()
	if err := c.allocCtx.Err(); err != nil {
		return nil, err
	}
	if c.browserCtx != nil && c.browserCtx.Err() == nil {
		b := chromedp.FromContext(c.browserCtx).Browser
		select {
		case <-b.LostConnection:
		default:
			return c.browserCtx, nil
		}
	}
	if c.cancelBrowser != nil {
		c.cancelBrowser()
	}
	ctx, cancel := chromedp.NewContext(c.allocCtx)
	timer := time.AfterFunc(20*time.Second, cancel)
	err := chromedp.Run(ctx)
	timer.Stop()
	if err != nil || ctx.Err() != nil {
		cancel()
		if err == nil {
			err = ctx.Err()
		}
		return nil, fmt.Errorf("chrome startup failed: %w", err)
	}
	c.browserCtx, c.cancelBrowser = ctx, cancel
	metrics.BrowserStarts.Inc()
	return ctx, nil
}

func (c *ChromeConverter) Close() {
	c.cancelAlloc()
	c.browserMu.Lock()
	defer c.browserMu.Unlock()
	if c.cancelBrowser != nil {
		c.cancelBrowser()
	}
}

func (c *ChromeConverter) Healthy() bool {
	c.browserMu.Lock()
	defer c.browserMu.Unlock()
	if c.browserCtx == nil || c.browserCtx.Err() != nil {
		return false
	}
	select {
	case <-chromedp.FromContext(c.browserCtx).Browser.LostConnection:
		return false
	default:
		return true
	}
}

func (c *ChromeConverter) acquire(ctx context.Context) error {
	start := time.Now()
	defer func() { metrics.StageDuration.WithLabelValues("render_queue").Observe(time.Since(start).Seconds()) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case c.semaphore <- struct{}{}:
		c.inUse.Add(1)
		metrics.WorkersInUse.Inc()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.allocCtx.Done():
		return c.allocCtx.Err()
	}
}

func (c *ChromeConverter) release() {
	<-c.semaphore
	c.inUse.Add(-1)
	metrics.WorkersInUse.Dec()
}

// task bridges the request deadline/cancellation to an isolated tab without
// giving the request ownership of the shared browser.
func (c *ChromeConverter) task(ctx context.Context) (context.Context, func(), error) {
	owner, err := c.browser()
	if err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	tab, cancel := chromedp.NewContext(owner, chromedp.WithNewBrowserContext())
	stop := context.AfterFunc(ctx, cancel)
	return tab, func() { stop(); cancel() }, nil
}

// documentReady waits for document resources instead of sleeping. Applications
// can additionally supply a readiness predicate for asynchronous page content.
func documentReady(opts *models.PDFOptions) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		if opts != nil && opts.WaitForExpression != "" {
			if err := chromedp.Poll(opts.WaitForExpression, nil, chromedp.WithPollingTimeout(30*time.Second)).Do(ctx); err != nil {
				return err
			}
		}
		script := `(async () => {
   if (document.readyState !== "complete") await new Promise(resolve => window.addEventListener("load", resolve, {once:true}));
   await document.fonts.ready;
   await Promise.all(Array.from(document.images, img => { img.loading = "eager"; return img.decode().catch(() => {}); }));
   return true;
  })()`
		if err := chromedp.Evaluate(script, nil, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }).Do(ctx); err != nil {
			return err
		}
		return nil
	})
}

// printPDF reads bounded chunks instead of one large Base64 protocol response.
func printPDF(ctx context.Context, opts *models.PDFOptions) ([]byte, error) {
	_, stream, err := applyPrintOptions(page.PrintToPDF(), opts).WithTransferMode(page.PrintToPDFTransferModeReturnAsStream).Do(ctx)
	if err != nil {
		return nil, err
	}
	defer cdpio.Close(stream).Do(ctx)
	var out limits.Buffer
	for {
		var result cdpio.ReadReturns
		if err := cdp.Execute(ctx, cdpio.CommandRead, cdpio.Read(stream).WithSize(64<<10), &result); err != nil {
			return nil, err
		}
		chunk := []byte(result.Data)
		if result.Base64encoded {
			chunk, err = base64.StdEncoding.DecodeString(result.Data)
			if err != nil {
				return nil, err
			}
		}
		if _, err := out.Write(chunk); err != nil {
			return nil, err
		}
		if result.EOF {
			return out.Bytes(), nil
		}
	}
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
	ctx, deadlineCancel := context.WithTimeout(ctx, defaultHTMLTimeout)
	defer deadlineCancel()
	if err := c.acquire(ctx); err != nil {
		return nil, err
	}
	defer c.release()
	taskCtx, cancel, err := c.task(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	start := time.Now()
	defer func() { metrics.StageDuration.WithLabelValues("render").Observe(time.Since(start).Seconds()) }()

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

	actions = append(actions, documentReady(opts))

	var buf []byte
	actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		buf, err = printPDF(ctx, opts)
		return err
	}))

	if err = chromedp.Run(taskCtx, actions...); err != nil {
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
	ctx, deadlineCancel := context.WithTimeout(ctx, defaultURLTimeout)
	defer deadlineCancel()
	if err := validateURL(ctx, raw); err != nil {
		return nil, err
	}
	if err := c.acquire(ctx); err != nil {
		return nil, err
	}
	defer c.release()
	taskCtx, cancel, err := c.task(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	start := time.Now()
	defer func() { metrics.StageDuration.WithLabelValues("render").Observe(time.Since(start).Seconds()) }()

	var buf []byte
	err = chromedp.Run(taskCtx,
		chromedp.Navigate(raw),
		chromedp.WaitReady("body"),
		documentReady(opts),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var perr error
			buf, perr = printPDF(ctx, opts)
			return perr
		}),
	)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
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
	data, _ := io.ReadAll(io.LimitReader(base64.NewDecoder(base64.StdEncoding, strings.NewReader(b64)), 512))
	if len(data) > 0 {
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return validateURL(ctx, raw)
}

func validateURL(ctx context.Context, raw string) error {
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
	addrs, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
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
