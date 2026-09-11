// Package metrics holds in-memory pipeline counters (Prometheus-ready
// architecture: swap Add/Load with client_golang vectors later).
package metrics

import (
	"sync/atomic"
	"time"
)

// Counter is an atomic int64 counter.
type Counter struct{ v atomic.Int64 }

// Add increments by n.
func (c *Counter) Add(n int64) { c.v.Add(n) }

// Load returns the value.
func (c *Counter) Load() int64 { return c.v.Load() }

var (
	Searches        Counter
	Candidates      Counter
	Crawls          Counter
	CrawlFailed     Counter
	LeadsCreated    Counter
	Duplicates      Counter
	Enrichments     Counter
	Qualified       Counter
	HTTPRequests    Counter
	HTTPDurationMs  Counter
	EmailSent       Counter
	EmailFailed     Counter
	WebhooksFailed  Counter
	BrowserFallback Counter
)

// Snapshot is a point-in-time view.
type Snapshot struct {
	Searches        int64 `json:"searches_total"`
	Candidates      int64 `json:"candidates_total"`
	Crawls          int64 `json:"crawl_total"`
	CrawlFailed     int64 `json:"crawl_failed_total"`
	LeadsCreated    int64 `json:"leads_created_total"`
	Duplicates      int64 `json:"duplicates_total"`
	Enrichments     int64 `json:"enrichments_total"`
	Qualified       int64 `json:"qualified_total"`
	HTTPRequests    int64 `json:"http_requests_total"`
	HTTPDurationMs  int64 `json:"http_duration_ms_total"`
	EmailSent       int64 `json:"email_sent_total"`
	EmailFailed     int64 `json:"email_failed_total"`
	WebhooksFailed  int64 `json:"webhooks_failed_total"`
	BrowserFallback int64 `json:"browser_fallback_total"`
}

// Snap returns current values.
func Snap() Snapshot {
	return Snapshot{
		Searches: Searches.Load(), Candidates: Candidates.Load(),
		Crawls: Crawls.Load(), CrawlFailed: CrawlFailed.Load(),
		LeadsCreated: LeadsCreated.Load(), Duplicates: Duplicates.Load(),
		Enrichments: Enrichments.Load(), Qualified: Qualified.Load(),
		HTTPRequests: HTTPRequests.Load(), HTTPDurationMs: HTTPDurationMs.Load(),
		EmailSent: EmailSent.Load(), EmailFailed: EmailFailed.Load(),
		WebhooksFailed: WebhooksFailed.Load(), BrowserFallback: BrowserFallback.Load(),
	}
}

// ObserveHTTP records request count and aggregate duration without any
// high-cardinality labels.
func ObserveHTTP(d time.Duration) {
	HTTPRequests.Add(1)
	HTTPDurationMs.Add(d.Milliseconds())
}
