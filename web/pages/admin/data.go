package admin

import "leadforge/internal/webapp"

// PageMeta is layout metadata passed from the web server.
type PageMeta = webapp.Page

// HealthRow is one dependency status row.
type HealthRow struct {
	Name    string
	Status  string
	Detail  string
	Latency string
}

// HealthData powers the admin health page.
type HealthData struct {
	Rows       []HealthRow
	Uptime     string
	GoVersion  string
	Goroutines int
	MemoryMB   int64
}

// QueueRow is one queue stat row.
type QueueRow struct {
	Queue     string
	Pending   int
	Active    int
	Scheduled int
	Retry     int
	Failed    int
	Processed int
}

// QueueData powers the admin queue page.
type QueueData struct {
	Rows      []QueueRow
	WorkerHB  string
	SchedHB   string
	RedisAddr string
}

// CrawlerData powers the admin crawlers page.
type CrawlerData struct {
	Workers     int
	DomainConc  int
	Timeout     string
	PendingJobs int
	PagesMin    float64
	SuccessRate float64
	AvgMs       int
	ActiveHosts []string
	Errors      []CrawlError
}

// CrawlError is a recent crawl failure.
type CrawlError struct {
	URL   string
	Error string
	When  string
}
