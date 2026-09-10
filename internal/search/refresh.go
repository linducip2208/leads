package search

import (
	"context"
	"encoding/json"
	"sync/atomic"

	"leadforge/internal/crawler"
	"leadforge/internal/lead"
	"leadforge/internal/scoring"
)

// RefreshLead recrawls a lead's company website, refreshes enrichment,
// re-verifies email, recalculates scores and stamps freshness.
func (r *Runner) RefreshLead(ctx context.Context, leadID string) error {
	d := r.deps
	var tenantID, companyID, website, industry string
	if err := d.Pool.QueryRow(ctx, `
		SELECT l.tenant_id::text, l.company_id::text, COALESCE(c.website,''), COALESCE(c.industry,'')
		FROM leads l JOIN companies c ON c.id = l.company_id WHERE l.id=$1`, leadID).
		Scan(&tenantID, &companyID, &website, &industry); err != nil {
		return err
	}
	if website == "" {
		return nil // nothing to recrawl
	}
	log := d.Log.With("lead_id", leadID, "tenant_id", tenantID)
	res := d.Crawler.Crawl(ctx, website, crawler.SiteConfig{
		MaxPages: d.Cfg.CrawlerMaxPagesPerSite, MaxDepth: 1,
		Workers: 2, DomainConc: d.Cfg.CrawlerDomainConcurrency,
		Timeout: d.Cfg.CrawlerTimeout, MaxBody: d.Cfg.CrawlerMaxBodyBytes,
		UserAgent: "LeadForgeBot/1.0 (+refresh; respects robots.txt)",
		Stop:      &atomic.Bool{},
	})
	_ = log
	if res.Pages == 0 {
		return nil
	}
	ex := res.Data
	// fill empty fields only; never blank good data
	_, _ = d.Pool.Exec(ctx, `
		UPDATE companies SET
			description = CASE WHEN description='' THEN $2 ELSE description END,
			phone = CASE WHEN phone='' THEN $3 ELSE phone END,
			whatsapp = CASE WHEN whatsapp='' THEN $4 ELSE whatsapp END,
			address = CASE WHEN address='' THEN $5 ELSE address END,
			linkedin_url = CASE WHEN linkedin_url='' THEN $6 ELSE linkedin_url END,
			instagram_url = CASE WHEN instagram_url='' THEN $7 ELSE instagram_url END,
			facebook_url = CASE WHEN facebook_url='' THEN $8 ELSE facebook_url END,
			last_enriched_at=now(), last_crawl_at=now(), updated_at=now()
		WHERE id=$1`,
		companyID, truncStr(ex.Description, 2000), firstPhone(ex), firstWA(ex), ex.Address,
		ex.Socials["linkedin"], ex.Socials["instagram"], ex.Socials["facebook"])
	// refresh tech evidence
	_, _ = d.Pool.Exec(ctx, `DELETE FROM company_technologies WHERE company_id=$1`, companyID)
	r.storeTech(ctx, tenantID, companyID, ex.Technologies)
	eb, _ := json.Marshal(map[string]any{"refresh": true, "pages": res.Pages,
		"quality": crawler.QualityScore(ex, res.Pages)})
	_, _ = d.Pool.Exec(ctx, `
		INSERT INTO lead_enrichments (tenant_id, company_id, kind, status, data, finished_at)
		VALUES ($1,$2,'website','completed',$3,now())`, tenantID, companyID, string(eb))

	// re-verify primary email (cheap, local)
	var contactID, email string
	_ = d.Pool.QueryRow(ctx, `SELECT id::text, email FROM contacts WHERE company_id=$1 AND tenant_id=$2 AND email<>'' ORDER BY created_at LIMIT 1`,
		companyID, tenantID).Scan(&contactID, &email)
	if email != "" {
		if v, _ := d.Verifier.Verify(ctx, email); v.Status != "" {
			_, _ = d.Pool.Exec(ctx, `UPDATE contacts SET email_status=$2, updated_at=now() WHERE id=$1`, contactID, v.Status)
		}
	}
	// reload normalized-ish snapshot for scoring
	var c struct {
		name, website, email, phone, whatsapp, country, province, city, address, industry string
	}
	_ = d.Pool.QueryRow(ctx, `SELECT name, website, province, city, address, industry, country FROM companies WHERE id=$1`,
		companyID).Scan(&c.name, &c.website, &c.province, &c.city, &c.address, &c.industry, &c.country)
	n := lead.Normalized{Name: c.name, Website: c.website, Email: email, Province: c.province,
		City: c.city, Address: c.address, Industry: c.industry, Country: c.country}
	_ = d.Pool.QueryRow(ctx, `SELECT phone, whatsapp FROM companies WHERE id=$1`, companyID).Scan(&n.PhoneE164, &n.WhatsApp)
	rules, _ := scoring.LoadTenantRules(ctx, d.Pool, tenantID)
	var f Filters
	score := r.scoreLead(ctx, &Job{Search: Row{TenantID: tenantID}, Rules: rules}, companyID, leadID, n, f)
	complete := 0
	_ = d.Pool.QueryRow(ctx, `SELECT data_quality FROM companies WHERE id=$1`, companyID).Scan(&complete)
	_, _ = d.Pool.Exec(ctx, `UPDATE leads SET data_quality=$2 WHERE id=$1`, leadID, complete)
	_, _ = d.Pool.Exec(ctx, `
		INSERT INTO activities (tenant_id, kind, subject, lead_id, company_id)
		VALUES ($1,'lead.enriched',$2,$3,$4)`, tenantID, "Lead refreshed (score "+itoa(score)+")", leadID, companyID)
	return nil
}

// RefreshStale refreshes up to `limit` hot leads whose enrichment is older
// than the freshness window. Never sweeps the whole database.
func (r *Runner) RefreshStale(ctx context.Context, limit int) int {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.deps.Pool.Query(ctx, `
		SELECT l.id::text FROM leads l JOIN companies c ON c.id = l.company_id
		WHERE l.status='hot' AND (c.last_enriched_at IS NULL OR c.last_enriched_at < now() - make_interval(days => $1))
		ORDER BY l.lead_score DESC LIMIT $2`, r.deps.Cfg.EnrichFreshDays, limit)
	if err != nil {
		return 0
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			if err := r.RefreshLead(ctx, id); err == nil {
				n++
			}
		}
		select {
		case <-ctx.Done():
			return n
		default:
		}
	}
	return n
}

func truncStr(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func firstPhone(ex *crawler.Extracted) string {
	if p, _ := lead.NormalizePhone(ex.PrimaryPhone()); p != "" {
		return p
	}
	return ""
}

func firstWA(ex *crawler.Extracted) string {
	if w, _ := lead.NormalizePhone(ex.PrimaryWhatsApp()); w != "" {
		return w
	}
	return ""
}
