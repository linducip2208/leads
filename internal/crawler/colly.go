package crawler

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gocolly/colly/v2"
)

// SiteConfig tunes a bounded site crawl.
type SiteConfig struct {
	MaxPages   int
	MaxDepth   int
	Workers    int // collector parallelism
	DomainConc int // per-domain parallelism
	Timeout    time.Duration
	MaxBody    int64
	UserAgent  string
	Stop       *atomic.Bool // set to halt further requests (pause/cancel)
}

// SiteResult is the merged outcome of crawling one start URL.
type SiteResult struct {
	Data   *Extracted
	Pages  int
	Failed int
	Errors []string
}

// CrawlSite crawls startURL (same site only) with colly: bounded pages/depth,
// robots honored by colly itself, guarded transport for SSRF + body caps.
// Individual page failures are recorded, never fatal.
func CrawlSite(ctx context.Context, g Guard, robots *RobotsChecker, startURL string, cfg SiteConfig) *SiteResult {
	res := &SiteResult{Data: &Extracted{Socials: map[string]string{}}}
	start, err := g.ValidateURL(startURL)
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
		return res
	}
	if robots != nil && !robots.Allowed(ctx, start.String(), start.Host) {
		res.Errors = append(res.Errors, "blocked by robots.txt")
		return res
	}
	if cfg.MaxPages <= 0 {
		cfg.MaxPages = 10
	}
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = 2
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 8
	}
	if cfg.DomainConc <= 0 {
		cfg.DomainConc = 2
	}

	baseHost := strings.ToLower(start.Host)
	var mu sync.Mutex
	visited := map[string]bool{}
	attempts := map[string]int{}
	var pages, failed int32

	c := colly.NewCollector(
		colly.Async(true),
		colly.MaxDepth(cfg.MaxDepth),
		colly.UserAgent(cfg.UserAgent),
	)
	c.AllowURLRevisit = true // own visited-set below (needed for retries)
	c.IgnoreRobotsTxt = false
	c.SetRequestTimeout(cfg.Timeout)
	c.WithTransport(GuardedTransport(g, cfg.MaxBody))
	_ = c.Limit(&colly.LimitRule{
		DomainGlob:  "*",
		Parallelism: cfg.DomainConc,
		Delay:       400 * time.Millisecond,
		RandomDelay: 400 * time.Millisecond,
	})

	sameSite := func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return ""
		}
		h := strings.ToLower(u.Host)
		if h != baseHost && !strings.HasSuffix(h, "."+strings.TrimPrefix(baseHost, "www.")) && h != "www."+strings.TrimPrefix(baseHost, "www.") {
			return ""
		}
		u.Fragment = ""
		return u.String()
	}

	c.OnRequest(func(r *colly.Request) {
		if ctx.Err() != nil || (cfg.Stop != nil && cfg.Stop.Load()) {
			r.Abort()
			return
		}
		norm := sameSite(r.URL.String())
		if norm == "" {
			r.Abort()
			return
		}
		mu.Lock()
		if visited[norm] || int(pages+failed) >= cfg.MaxPages {
			mu.Unlock()
			r.Abort()
			return
		}
		visited[norm] = true
		mu.Unlock()
	})

	c.OnHTML("a[href]", func(e *colly.HTMLElement) {
		if ctx.Err() != nil || (cfg.Stop != nil && cfg.Stop.Load()) {
			return
		}
		link := e.Request.AbsoluteURL(e.Attr("href"))
		if sameSite(link) == "" {
			return
		}
		mu.Lock()
		over := int(pages+failed) >= cfg.MaxPages
		mu.Unlock()
		if over {
			return
		}
		_ = e.Request.Visit(link)
	})

	c.OnResponse(func(r *colly.Response) {
		ct := r.Headers.Get("Content-Type")
		if ct != "" && !strings.Contains(ct, "html") && !strings.Contains(ct, "text") {
			return
		}
		part := Extract(r.Request.URL.String(), r.Body)
		mu.Lock()
		res.Data.Merge(part)
		mu.Unlock()
		atomic.AddInt32(&pages, 1)
	})

	c.OnError(func(r *colly.Response, err error) {
		u := r.Request.URL.String()
		mu.Lock()
		attempts[u]++
		n := attempts[u]
		mu.Unlock()
		code := 0
		if r != nil {
			code = r.StatusCode
		}
		if n < 3 && retryable(HTTPStatusError(code)) && ctx.Err() == nil && (cfg.Stop == nil || !cfg.Stop.Load()) {
			time.Sleep(time.Duration(n) * 600 * time.Millisecond)
			_ = r.Request.Visit(u)
			return
		}
		mu.Lock()
		res.Errors = append(res.Errors, u+": "+err.Error())
		mu.Unlock()
		atomic.AddInt32(&failed, 1)
	})

	_ = c.Visit(start.String())
	done := make(chan struct{})
	go func() { c.Wait(); close(done) }()
	select {
	case <-ctx.Done():
	case <-done:
	}
	res.Pages = int(pages)
	res.Failed = int(failed)
	return res
}
