// Command crawler-test validates crawler reliability against a user-provided
// URL list. It is deliberately polite: five workers, one request stream per
// domain, and a one-second inter-domain start delay by default.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"leadforge/internal/crawler"
	"leadforge/internal/platform/config"
)

type result struct {
	pages   int
	email   bool
	phone   bool
	quality int
	errors  []string
}

func main() {
	file := flag.String("file", "", "file containing one URL/domain per line")
	workers := flag.Int("workers", 5, "bounded worker count")
	delay := flag.Duration("delay", time.Second, "minimum delay before each crawl")
	browser := flag.Bool("browser", false, "enable chromedp fallback when available")
	flag.Parse()
	if *file == "" {
		fmt.Fprintln(os.Stderr, "usage: crawler-test --file=testdata/crawler/domains.txt [--workers=5]")
		os.Exit(2)
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var urls []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, "://") {
			line = "https://" + line
		}
		urls = append(urls, line)
		if len(urls) >= 1000 {
			break
		}
	}
	if len(urls) == 0 {
		fmt.Fprintln(os.Stderr, "no URLs found")
		os.Exit(1)
	}
	if *workers < 1 {
		*workers = 1
	}

	cfg := &config.Config{CrawlerTimeout: 20 * time.Second, CrawlerMaxPagesPerSite: 10,
		CrawlerMaxDepth: 2, CrawlerGlobalWorkers: *workers, CrawlerDomainConcurrency: 1,
		CrawlerAllowPrivate: false, BrowserEnabled: *browser, BrowserWorkers: 1,
		BrowserTimeout: 20 * time.Second, BrowserMaxPages: 3, RedisAddr: ""}
	mgr := crawler.NewManager(cfg, nil)
	defer mgr.Close()
	jobCh := make(chan string)
	resCh := make(chan result, len(urls))
	var wg sync.WaitGroup
	started := time.Now()
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for raw := range jobCh {
				if *delay > 0 {
					t := time.NewTimer(*delay)
					<-t.C
				}
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				r := mgr.Crawl(ctx, raw, crawler.SiteConfig{MaxPages: 10, MaxDepth: 2, Workers: 2, DomainConc: 1, Timeout: 20 * time.Second, MaxBody: 5 << 20})
				cancel()
				item := result{pages: r.Pages, errors: r.Errors}
				if r.Data != nil {
					item.email = r.Data.PrimaryEmail() != ""
					item.phone = r.Data.PrimaryPhone() != ""
					item.quality = crawler.QualityScore(r.Data, r.Pages)
				}
				resCh <- item
			}
		}()
	}
	for _, u := range urls {
		jobCh <- u
	}
	close(jobCh)
	wg.Wait()
	close(resCh)

	var success, failed, timeout, robots, forbidden, notFound, rate, server5xx, email, phone, quality int
	for r := range resCh {
		if r.pages > 0 {
			success++
		} else {
			failed++
		}
		if r.email {
			email++
		}
		if r.phone {
			phone++
		}
		quality += r.quality
		for _, e := range r.errors {
			s := strings.ToLower(e)
			switch {
			case strings.Contains(s, "timeout") || strings.Contains(s, "deadline"):
				timeout++
			case strings.Contains(s, "robots"):
				robots++
			case strings.Contains(s, "403"):
				forbidden++
			case strings.Contains(s, "404"):
				notFound++
			case strings.Contains(s, "429"):
				rate++
			case strings.Contains(s, "500") || strings.Contains(s, "502") || strings.Contains(s, "503") || strings.Contains(s, "504"):
				server5xx++
			}
		}
	}
	avg := 0
	if len(urls) > 0 {
		avg = quality / len(urls)
	}
	snap := mgr.Snapshot()
	fmt.Printf("Total: %d\nSuccess: %d\nFailed: %d\nTimeout: %d\nRobots: %d\n403: %d\n404: %d\n429: %d\n5xx: %d\nBrowser Fallback: %d\nEmail Found: %d\nPhone Found: %d\nAverage Quality: %d\nDuration: %s\n", len(urls), success, failed, timeout, robots, forbidden, notFound, rate, server5xx, snap.BrowserOK, email, phone, avg, time.Since(started).Round(time.Millisecond))
}
