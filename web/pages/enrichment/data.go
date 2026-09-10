package enrichment

import "leadforge/internal/webapp"

// PageMeta is layout metadata passed from the web server.
type PageMeta = webapp.Page

// Run is one enrichment run.
type Run struct {
	Company    string
	CompanyID  string
	Kind       string
	Status     string
	Confidence int
	When       string
}

// Stat is an aggregate counter.
type Stat struct {
	Label string
	Value int
}

// PageData powers the enrichment page.
type PageData struct {
	Stats []Stat
	Runs  []Run
}
