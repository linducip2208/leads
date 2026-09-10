package source

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

// ManualSource turns pasted seed URLs into candidates. Always available.
type ManualSource struct{}

func NewManualSource() *ManualSource { return &ManualSource{} }

func (m *ManualSource) Slug() string { return "manual" }
func (m *ManualSource) Name() string { return "Manual URLs" }

func (m *ManualSource) Info() SourceInfo {
	return SourceInfo{Slug: m.Slug(), Name: m.Name(),
		Description: "Crawl pasted company website URLs directly.",
		Priority:    100, Confidence: 95}
}

func (m *ManualSource) Search(ctx context.Context, query SearchQuery) (<-chan RawLead, error) {
	out := make(chan RawLead, 64)
	go func() {
		defer close(out)
		n := 0
		for _, raw := range query.SeedURLs {
			if n >= query.Limit && query.Limit > 0 {
				return
			}
			u := strings.TrimSpace(raw)
			if u == "" {
				continue
			}
			if !strings.Contains(u, "://") {
				u = "https://" + u
			}
			parsed, err := url.Parse(u)
			if err != nil || parsed.Host == "" {
				continue
			}
			if parsed.Scheme != "http" && parsed.Scheme != "https" {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case out <- RawLead{
				SourceSlug: m.Slug(), SourceConf: 95,
				DiscoveredAt: time.Now(),
				Website:      u,
				Domain:       strings.ToLower(parsed.Hostname()),
				SourceURL:    u,
				Country:      query.Country,
				Province:     query.Province,
				City:         query.City,
				Industry:     query.Industry,
			}:
				n++
			}
		}
	}()
	return out, nil
}

// CSVSource parses an uploaded CSV/XLSX-exported-as-CSV file. Header names are
// matched case-insensitively: name/company, website/domain, email, phone,
// address, city, province, country, industry.
type CSVSource struct{}

func NewCSVSource() *CSVSource { return &CSVSource{} }

func (c *CSVSource) Slug() string { return "csv" }
func (c *CSVSource) Name() string { return "CSV Import" }

func (c *CSVSource) Info() SourceInfo {
	return SourceInfo{Slug: c.Slug(), Name: c.Name(),
		Description: "Rows from an uploaded CSV file.",
		Priority:    90, Confidence: 85}
}

func (c *CSVSource) Search(ctx context.Context, query SearchQuery) (<-chan RawLead, error) {
	if len(query.CSVData) == 0 {
		return nil, fmt.Errorf("csv source: no file data")
	}
	rows, err := ParseCSV(query.CSVData)
	if err != nil {
		return nil, err
	}
	out := make(chan RawLead, 64)
	go func() {
		defer close(out)
		n := 0
		for _, r := range rows {
			if n >= query.Limit && query.Limit > 0 {
				return
			}
			lead := RawLead{
				SourceSlug: c.Slug(), SourceConf: 85,
				DiscoveredAt: time.Now(),
				Name:         firstNonEmpty(r["name"], r["company"], r["company_name"], r["business_name"]),
				Website:      firstNonEmpty(r["website"], r["domain"], r["url"], r["site"]),
				Email:        r["email"],
				Phone:        firstNonEmpty(r["phone"], r["mobile"], r["tel"], r["telephone"]),
				Address:      r["address"],
				City:         firstNonEmpty(r["city"], query.City),
				Province:     firstNonEmpty(r["province"], r["state"], query.Province),
				Country:      firstNonEmpty(r["country"], query.Country),
				Industry:     firstNonEmpty(r["industry"], r["category"], query.Industry),
				SourceURL:    "csv:" + query.CSVName,
			}
			if lead.Website != "" && !strings.Contains(lead.Website, "://") {
				lead.Website = "https://" + lead.Website
			}
			if lead.Name == "" && lead.Website == "" && lead.Email == "" {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case out <- lead:
				n++
			}
		}
	}()
	return out, nil
}

// ParseCSV reads CSV bytes into a slice of lower-cased header → value maps.
func ParseCSV(data []byte) ([]map[string]string, error) {
	// strip UTF-8 BOM
	s := strings.TrimPrefix(string(data), "\xef\xbb\xbf")
	r := csv.NewReader(strings.NewReader(s))
	r.TrimLeadingSpace = true
	r.LazyQuotes = true
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("csv header: %w", err)
	}
	for i := range header {
		header[i] = strings.ToLower(strings.TrimSpace(header[i]))
	}
	var rows []map[string]string
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("csv row: %w", err)
		}
		m := map[string]string{}
		for i, h := range header {
			if i < len(rec) {
				m[h] = strings.TrimSpace(rec[i])
			}
		}
		rows = append(rows, m)
	}
	return rows, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
