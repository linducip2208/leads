package crawler

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/chromedp"
)

// BrowserOptions tunes the headless fallback pool.
type BrowserOptions struct {
	Enabled  bool
	Workers  int
	Timeout  time.Duration
	MaxPages int
}

// BrowserPool keeps a bounded set of headless Chrome tabs for JS-required
// pages. When Chrome is unavailable the pool stays disabled and crawling
// continues HTTP-only; the app never fails to boot because of it.
type BrowserPool struct {
	opts        BrowserOptions
	slots       chan struct{}
	allocOK     atomic.Bool
	once        sync.Once
	closeOnce   sync.Once
	mu          sync.Mutex
	allocCtx    context.Context
	allocCancel context.CancelFunc
}

// NewBrowserPool builds (but does not yet launch) the pool.
func NewBrowserPool(o BrowserOptions) *BrowserPool {
	if o.Workers <= 0 {
		o.Workers = 2
	}
	if o.Timeout <= 0 {
		o.Timeout = 20 * time.Second
	}
	if o.MaxPages <= 0 {
		o.MaxPages = 3
	}
	return &BrowserPool{opts: o, slots: make(chan struct{}, o.Workers)}
}

// Available reports whether browser rendering can be attempted.
func (p *BrowserPool) Available() bool {
	if !p.opts.Enabled {
		return false
	}
	p.once.Do(p.init)
	return p.allocOK.Load()
}

func (p *BrowserPool) init() {
	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(),
		chromedp.Headless,
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.DisableGPU,
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("mute-audio", true),
	)
	// probe: create + cancel a throwaway context to detect missing Chrome
	probe, probeCancel := chromedp.NewContext(allocCtx)
	ctx, cancel2 := context.WithTimeout(probe, 8*time.Second)
	defer cancel2()
	defer probeCancel()
	if err := chromedp.Run(ctx, chromedp.Navigate("about:blank")); err != nil {
		cancel()
		return // Chrome unavailable: stay disabled, HTTP-only
	}
	p.mu.Lock()
	p.allocCtx = allocCtx
	p.allocCancel = cancel
	p.mu.Unlock()
	p.allocOK.Store(true)
}

// Close releases the allocator and all Chrome child processes. It is safe to
// call more than once and is intentionally a no-op when Chrome was disabled.
func (p *BrowserPool) Close() {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		cancel := p.allocCancel
		p.allocCancel = nil
		p.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		p.allocOK.Store(false)
	})
}

// Render loads url in a pooled tab and returns rendered HTML + final URL.
func (p *BrowserPool) Render(ctx context.Context, guard Guard, rawurl string) (html, finalURL string, err error) {
	if !p.Available() {
		return "", "", errBrowserDown
	}
	u, err := guard.ValidateURL(rawurl)
	if err != nil {
		return "", "", err
	}
	if _, err := guard.CheckHost(ctx, u.Host); err != nil {
		return "", "", err
	}
	select {
	case <-ctx.Done():
		return "", "", ctx.Err()
	case p.slots <- struct{}{}:
	}
	defer func() { <-p.slots }()

	tab, cancel := chromedp.NewContext(p.allocCtx)
	defer cancel()
	tctx, cancel2 := context.WithTimeout(tab, p.opts.Timeout)
	defer cancel2()
	var out, href string
	tasks := chromedp.Tasks{
		chromedp.Navigate(u.String()),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(1500 * time.Millisecond),
		chromedp.OuterHTML("html", &out, chromedp.ByQuery),
		chromedp.Evaluate(`location.href`, &href),
	}
	if err := chromedp.Run(tctx, tasks); err != nil {
		return "", "", err
	}
	if href == "" {
		href = u.String()
	}
	// revalidate the final URL (redirects may land elsewhere)
	fu, err := guard.ValidateURL(href)
	if err != nil {
		return "", "", err
	}
	if _, err := guard.CheckHost(ctx, fu.Host); err != nil {
		return "", "", err
	}
	if len(out) > 5<<20 {
		out = out[:5<<20]
	}
	return out, fu.String(), nil
}

var errBrowserDown = browserDownError("browser unavailable")

type browserDownError string

func (e browserDownError) Error() string { return string(e) }

// CrawlSite renders the start page plus top priority same-site links
// (contact/about, bounded by MaxPages) and merges extraction.
func (p *BrowserPool) CrawlSite(ctx context.Context, m *Manager, startURL string, maxPages int) *SiteResult {
	res := &SiteResult{Data: &Extracted{Socials: map[string]string{}}}
	if maxPages <= 0 {
		maxPages = 3
	}
	html, finalURL, err := p.Render(ctx, m.Guard, startURL)
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
		return res
	}
	res.Data.Merge(Extract(finalURL, []byte(html)))
	res.Pages = 1
	// follow 1-2 priority links (contact/about) from the rendered DOM
	for _, link := range priorityLinks(finalURL, html, maxPages-1) {
		if res.Pages >= maxPages {
			break
		}
		h2, f2, err := p.Render(ctx, m.Guard, link)
		if err != nil {
			continue
		}
		res.Data.Merge(Extract(f2, []byte(h2)))
		res.Pages++
	}
	return res
}
