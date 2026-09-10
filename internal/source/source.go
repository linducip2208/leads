// Package source defines lead discovery connectors. Every provider yields
// RawLead candidates; crawling, normalization and persistence happen downstream.
// No provider requires an API key to boot the application.
package source

import (
	"context"
	"strings"
)

// SearchQuery carries finder filters to providers.
type SearchQuery struct {
	Keyword     string
	Industry    string
	Country     string
	Province    string
	City        string
	CompanySize string
	Limit       int
	SeedURLs    []string // manual source
	CSVData     []byte   // csv source
	CSVName     string
	APIURL      string            // custom api source
	APIConfig   map[string]string // custom api source options
	Sources     []string          // slugs to use; empty = all applicable
}

// Location joins the location parts for display/queries.
func (q SearchQuery) Location() string {
	parts := []string{q.City, q.Province, q.Country}
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ", ")
}

// Keywords joins keyword + industry for discovery queries.
func (q SearchQuery) Keywords() string {
	s := strings.TrimSpace(q.Keyword)
	if q.Industry != "" {
		s = strings.TrimSpace(s + " " + q.Industry)
	}
	return s
}

// RawLead is an unprocessed discovery candidate.
type RawLead struct {
	SourceSlug string
	ExternalID string
	Name       string
	Website    string
	Domain     string
	Email      string
	Phone      string
	Address    string
	City       string
	Province   string
	Country    string
	Industry   string
	SourceURL  string
	Payload    map[string]string
}

// LeadSource discovers candidate websites/companies.
type LeadSource interface {
	// Slug is the stable identifier stored on raw_leads.source_slug.
	Slug() string
	// Name is the display name.
	Name() string
	// Search streams candidates; it must respect ctx cancellation and stop
	// after roughly query.Limit candidates.
	Search(ctx context.Context, query SearchQuery) (<-chan RawLead, error)
}

// Registry maps slugs to providers.
type Registry struct {
	sources map[string]LeadSource
	order   []string
}

// NewRegistry builds a registry from providers.
func NewRegistry(providers ...LeadSource) *Registry {
	r := &Registry{sources: map[string]LeadSource{}}
	for _, p := range providers {
		r.sources[p.Slug()] = p
		r.order = append(r.order, p.Slug())
	}
	return r
}

// Get returns a provider by slug.
func (r *Registry) Get(slug string) (LeadSource, bool) { return r.sources[slug], true }

// Slugs lists registered slugs in order.
func (r *Registry) Slugs() []string { return append([]string(nil), r.order...) }

// DefaultRegistry wires all built-in providers plus the Google Places adapter
// (which stays dormant until an API key is configured).
func DefaultRegistry(googleAPIKey string) *Registry {
	return NewRegistry(
		NewManualSource(),
		NewCSVSource(),
		NewWebsiteSearchSource(),
		NewPublicDirectorySource(),
		NewCustomAPISource(),
		NewGooglePlacesSource(googleAPIKey),
	)
}

// Wants reports whether the query selects the given slug.
func Wants(query SearchQuery, slug string) bool {
	if len(query.Sources) == 0 {
		return true
	}
	for _, s := range query.Sources {
		if s == slug {
			return true
		}
	}
	return false
}
