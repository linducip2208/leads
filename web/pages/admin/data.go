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
	GlobalUse   int
	GlobalCap   int
	BrowserOn   bool
	BrowserN    int
	RobotsHit   int
	SSRFHit     int
	QueueDepth  int
	Cooldowns   []CooldownRow
}

// CooldownRow is an active domain cooldown.
type CooldownRow struct {
	Domain string
	Until  string
}

// CrawlError is a recent crawl failure.
type CrawlError struct {
	URL   string
	Error string
	When  string
}

// SourceStatRow is one connector's global telemetry.
type SourceStatRow struct {
	Slug       string
	Candidates int
	Accepted   int
	Errors     int
	SuccessPct float64
	LastOK     string
	LastError  string
}

// TenantRow is one tenant for superadmin.
type TenantRow struct {
	ID      string
	Name    string
	Slug    string
	Status  string
	Plan    string
	Users   int
	Leads   int
	Created string
}

// TenantsData powers the tenants page.
type TenantsData struct {
	Tenants []TenantRow
}

// TenantDetailData powers tenant detail + plan assignment.
type TenantDetailData struct {
	Tenant TenantRow
	Plans  []PlanRow
}

// PlanRow is one billing plan.
type PlanRow struct {
	ID      string
	Slug    string
	Name    string
	Price   string
	Current bool
	Limits  string
}

// PlansData powers the plans page.
type PlansData struct {
	Plans []PlanRow
}

// AdminUserRow is one user for superadmin.
type AdminUserRow struct {
	ID     string
	Name   string
	Email  string
	Tenant string
	Status string
	Super  bool
	Joined string
}

// UsersData powers the admin users page.
type UsersData struct {
	Users []AdminUserRow
}

// UsageRow is per-tenant monthly usage.
type UsageRow struct {
	Tenant  string
	Search  int
	Crawl   int
	Enrich  int
	Email   int
	Credits int
}

// UsageData powers the usage page.
type UsageData struct {
	Rows []UsageRow
}

// LogRow is one audit entry.
type LogRow struct {
	Action string
	Entity string
	Target string
	User   string
	Tenant string
	IP     string
	When   string
}

// LogsData powers the audit log viewer.
type LogsData struct {
	Logs   []LogRow
	Action string
}
