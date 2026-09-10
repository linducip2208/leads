// Package source defines lead discovery connectors. Every provider yields
// RawLead candidates; crawling, normalization and persistence happen downstream.
// No provider requires an API key to boot the application.
package source

import (
	"context"
	"sort"
	"strings"
	"time"
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
	GoogleKey   string            // google places (tenant key or env)
	Sources     []string          // slugs to use; empty or ["auto"] = auto select
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
	SourceSlug   string
	SourceConf   int // source trust 0-100
	ExternalID   string
	Name         string
	Website      string
	Domain       string
	Email        string
	Phone        string
	Address      string
	City         string
	Province     string
	Country      string
	Industry     string
	SourceURL    string
	DiscoveredAt time.Time
	Payload      map[string]string
}

// SourceInfo describes a provider for registry/UI/monitoring.
type SourceInfo struct {
	Slug               string
	Name               string
	Description        string
	Priority           int // higher runs first in fan-in ordering
	Confidence         int // default candidate trust 0-100
	RequiresCredential bool
}

// LeadSource discovers candidate websites/companies.
type LeadSource interface {
	// Slug is the stable identifier stored on raw_leads.source_slug.
	Slug() string
	// Name is the display name.
	Name() string
	// Info describes the provider.
	Info() SourceInfo
	// Search streams candidates; it must respect ctx cancellation and stop
	// after roughly query.Limit candidates.
	Search(ctx context.Context, query SearchQuery) (<-chan RawLead, error)
}

// Registry maps slugs to providers, ordered by priority.
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
	sort.SliceStable(r.order, func(i, j int) bool {
		return r.sources[r.order[i]].Info().Priority > r.sources[r.order[j]].Info().Priority
	})
	return r
}

// Get returns a provider by slug.
func (r *Registry) Get(slug string) (LeadSource, bool) {
	p, ok := r.sources[slug]
	return p, ok
}

// Slugs lists registered slugs in priority order.
func (r *Registry) Slugs() []string { return append([]string(nil), r.order...) }

// Infos lists provider metadata in priority order.
func (r *Registry) Infos() []SourceInfo {
	out := make([]SourceInfo, 0, len(r.order))
	for _, s := range r.order {
		out = append(out, r.sources[s].Info())
	}
	return out
}

// DefaultRegistry wires all built-in providers plus the Google Places adapter
// (dormant until a key is configured).
func DefaultRegistry(googleAPIKey string) *Registry {
	return NewRegistry(
		NewManualSource(),
		NewGooglePlacesSource(googleAPIKey),
		NewPublicDirectorySource(),
		NewWebsiteSearchSource(),
		NewCustomAPISource(),
		NewCSVSource(),
	)
}

// MockSource emits deterministic synthetic candidates (no network) for
// benchmarks and scale simulations.
type MockSource struct {
	N      int
	Domain string
}

func NewMockSource(n int) *MockSource { return &MockSource{N: n} }

func (m *MockSource) Slug() string { return "mock" }
func (m *MockSource) Name() string { return "Mock (benchmark)" }

func (m *MockSource) Info() SourceInfo {
	return SourceInfo{Slug: m.Slug(), Name: m.Name(),
		Description: "Synthetic in-process candidates for load testing.",
		Priority:    1, Confidence: 50}
}

func (m *MockSource) Search(ctx context.Context, query SearchQuery) (<-chan RawLead, error) {
	out := make(chan RawLead, 256)
	go func() {
		defer close(out)
		for i := 0; i < m.N; i++ {
			name := "PT Benchmark " + itoa(i) + " Jaya"
			select {
			case <-ctx.Done():
				return
			case out <- RawLead{
				SourceSlug: m.Slug(), SourceConf: 50,
				ExternalID:   "mock-" + itoa(i),
				Name:         name,
				Email:        "info" + itoa(i) + "@bench.local",
				Phone:        "0812" + itoa(1000000+i),
				City:         "Jakarta",
				Country:      "Indonesia",
				Industry:     query.Industry,
				SourceURL:    "mock:",
				DiscoveredAt: time.Now(),
				Payload:      map[string]string{},
			}:
			}
		}
	}()
	return out, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// autoMode reports whether the query wants automatic source selection.
func autoMode(query SearchQuery) bool {
	if len(query.Sources) == 0 {
		return true
	}
	for _, s := range query.Sources {
		if s == "auto" {
			return true
		}
	}
	return false
}

// Wants reports whether the query selects the given slug.
func Wants(query SearchQuery, slug string) bool {
	if autoMode(query) {
		return true
	}
	for _, s := range query.Sources {
		if s == slug {
			return true
		}
	}
	return false
}

// Applicable reports whether a provider can run for this query (prerequisites
// met: seeds for manual, data for csv, key for google/custom api).
func Applicable(query SearchQuery, slug string) bool {
	switch slug {
	case "manual":
		return len(query.SeedURLs) > 0
	case "csv":
		return len(query.CSVData) > 0
	case "google_places":
		return query.GoogleKey != ""
	case "custom_api":
		return strings.TrimSpace(query.APIURL) != ""
	default:
		return true
	}
}
