// Package enrichment fills company records from public website data.
// The built-in WebsiteProvider crawls the company site; no AI, no vendor.
package enrichment

import (
	"context"
	"strings"
	"sync/atomic"

	"leadforge/internal/crawler"
	"leadforge/internal/platform/config"
)

// CompanyInput is the record to enrich.
type CompanyInput struct {
	Name    string
	Website string
	Domain  string
}

// CompanyOutput is the enrichment result with provenance.
type CompanyOutput struct {
	Name         string
	Description  string
	Email        string
	Phone        string
	WhatsApp     string
	Address      string
	LinkedIn     string
	Instagram    string
	Facebook     string
	X            string
	YouTube      string
	Technologies []string
	Confidence   int // 0-100 based on fields filled
	SourceURL    string
}

// Provider enriches a company.
type Provider interface {
	Name() string
	EnrichCompany(ctx context.Context, in CompanyInput) (CompanyOutput, error)
}

// WebsiteProvider crawls the company website and maps extraction to fields.
type WebsiteProvider struct {
	Guard  crawler.Guard
	Robots *crawler.RobotsChecker
	Cfg    *config.Config
}

func (p *WebsiteProvider) Name() string { return "website" }

// EnrichCompany crawls the site (bounded) and returns mapped fields.
func (p *WebsiteProvider) EnrichCompany(ctx context.Context, in CompanyInput) (CompanyOutput, error) {
	out := CompanyOutput{SourceURL: in.Website}
	if strings.TrimSpace(in.Website) == "" {
		return out, nil
	}
	res := crawler.CrawlSite(ctx, p.Guard, p.Robots, in.Website, crawler.SiteConfig{
		MaxPages:   p.Cfg.CrawlerMaxPagesPerSite,
		MaxDepth:   p.Cfg.CrawlerMaxDepth,
		Workers:    4,
		DomainConc: p.Cfg.CrawlerDomainConcurrency,
		Timeout:    p.Cfg.CrawlerTimeout,
		MaxBody:    p.Cfg.CrawlerMaxBodyBytes,
		UserAgent:  "LeadForgeBot/1.0 (+lead discovery; respects robots.txt)",
		Stop:       &atomic.Bool{},
	})
	d := res.Data
	out.Name = firstNonEmpty(d.CompanyName, in.Name)
	out.Description = d.Description
	out.Email = d.PrimaryEmail()
	out.Phone = d.PrimaryPhone()
	out.WhatsApp = d.PrimaryWhatsApp()
	out.Address = d.Address
	out.LinkedIn = d.Socials["linkedin"]
	out.Instagram = d.Socials["instagram"]
	out.Facebook = d.Socials["facebook"]
	out.X = d.Socials["x"]
	out.YouTube = d.Socials["youtube"]
	out.Technologies = nil
	for _, t := range d.Technologies {
		out.Technologies = append(out.Technologies, t.Name)
	}
	filled := 0
	for _, v := range []string{out.Name, out.Description, out.Email, out.Phone, out.Address} {
		if v != "" {
			filled++
		}
	}
	filled += len(d.Socials)
	out.Confidence = filled * 10
	if out.Confidence > 100 {
		out.Confidence = 100
	}
	return out, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
