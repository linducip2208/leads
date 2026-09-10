package searches

import (
	"strconv"
	"strings"

	"leadforge/internal/search"
	"leadforge/internal/webapp"
)

// PageMeta is layout metadata passed from the web server.
type PageMeta = webapp.Page

// Item is one search row.
type Item struct {
	ID        string
	Query     string
	Industry  string
	Location  string
	Status    string
	Requested int
	Found     int
	Unique    int
	Qualified int
	Created   string
	Duration  string
	CreatedBy string
}

// ListData powers the history page.
type ListData struct {
	Items []Item
}

// ResultRow is a lead found by the search.
type ResultRow struct {
	LeadID  string
	Company string
	City    string
	Score   int
	Status  string
}

// DetailData powers the search detail page.
type DetailData struct {
	Search   Item
	Progress *search.Progress
	Results  []ResultRow
	MinScore int
	Funnel   []FunnelStep
	Failures []FailRow
	Quality  QualityStats
	Sources  []SrcRow
}

// FunnelStep is one funnel row.
type FunnelStep struct {
	Label string
	Value int
}

// FailRow is one failure-reason count.
type FailRow struct {
	Reason string
	Count  int
}

// QualityStats aggregates result quality.
type QualityStats struct {
	AvgScore       float64
	HotPct         float64
	ContactablePct float64
	TopIndustries  []string
	TopCities      []string
}

// SrcRow is per-source debug telemetry.
type SrcRow struct {
	Slug       string
	Candidates int
	Accepted   int
	Errors     int
	LastError  string
}

// LatestRow is a realtime discovered lead.
type LatestRow struct {
	LeadID  string
	Company string
	Score   int
	When    string
}

// SavedItem is one saved search.
type SavedItem struct {
	ID      string
	Name    string
	Summary string
	Created string
}

// SavedData powers the saved searches page.
type SavedData struct {
	Items []SavedItem
}

// num formats thousands: 34280 -> 34,280.
func num(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	rem := len(s) % 3
	if rem > 0 {
		b.WriteString(s[:rem])
		if len(s) > rem {
			b.WriteByte(',')
		}
	}
	for i := rem; i < len(s); i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < len(s) {
			b.WriteByte(',')
		}
	}
	return b.String()
}
