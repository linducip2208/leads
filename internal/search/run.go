package search

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"leadforge/internal/crawler"
	"leadforge/internal/lead"
	"leadforge/internal/scoring"
	"leadforge/internal/source"
	"leadforge/internal/verify"
)

// Run is the worker entry point: claim, discover, process, finish.
func (r *Runner) Run(ctx context.Context, searchID string) error {
	j, err := r.Load(ctx, searchID)
	if err != nil {
		return err
	}
	if !r.Claim(ctx, j) {
		// already claimed/finished elsewhere
		return nil
	}
	cands := r.Discover(ctx, j)
	eg, ctx2 := errgroup.WithContext(ctx)
	eg.SetLimit(4)
	count := 0
	for cand := range cands {
		if st := r.Control(ctx, j); st == "cancelled" || st == "failed" {
			j.stop.Store(true)
			break
		}
		if st := r.Control(ctx, j); st == "paused" {
			r.Flush(ctx, j)
			if !r.WaitWhilePaused(ctx, j) {
				j.stop.Store(true)
				break
			}
		}
		c := cand
		count++
		eg.Go(func() error {
			r.Process(ctx2, j, c)
			if (j.found.Load())%10 == 0 {
				r.Flush(ctx, j)
			}
			return nil
		})
		if count >= j.Search.Limit {
			break
		}
	}
	_ = eg.Wait()
	r.Flush(ctx, j)
	final := "completed"
	note := ""
	if j.found.Load() == 0 {
		j.srcMu.Lock()
		if len(j.srcErrs) > 0 {
			note = "No candidates discovered (" + strings.Join(j.srcErrs, "; ") + ")"
			if len(note) > 500 {
				note = note[:500]
			}
		} else {
			note = "No candidates discovered for this query."
		}
		j.srcMu.Unlock()
	}
	if j.Search.Status == "cancelled" || j.stop.Load() && j.Search.Status == "cancelled" {
		final = "cancelled"
	}
	// re-read in case cancel landed during drain
	var st string
	_ = r.deps.Pool.QueryRow(ctx, `SELECT status FROM lead_searches WHERE id=$1`, j.Search.ID).Scan(&st)
	if st == "cancelled" || st == "failed" {
		final = st
		note = ""
	}
	r.Finish(ctx, j, final, note)
	return nil
}

// Discover fans in all selected providers, stopping at the search limit.
func (r *Runner) Discover(ctx context.Context, j *Job) <-chan source.RawLead {
	out := make(chan source.RawLead, 64)
	f := j.Search.Filters
	q := source.SearchQuery{
		Keyword: fSeedKeyword(j), Industry: j.Search.Industry,
		Country:  firstNonEmpty(f.Country, j.Search.Country),
		Province: f.Province, City: f.City, CompanySize: f.CompanySize,
		Limit: j.Search.Limit, SeedURLs: f.SeedURLs, Sources: f.Sources,
	}
	go func() {
		defer close(out)
		sctx, cancel := context.WithCancel(ctx)
		defer cancel()
		var wg sync.WaitGroup
		for _, slug := range r.deps.Registry.Slugs() {
			prov, _ := r.deps.Registry.Get(slug)
			if prov == nil || !source.Wants(q, slug) {
				continue
			}
			if slug == "manual" && len(q.SeedURLs) == 0 {
				continue
			}
			if slug == "csv" {
				continue // imports run inline, not via finder
			}
			if slug == "google_places" {
				continue // dormant until API key configured
			}
			ch, err := prov.Search(sctx, q)
			if err != nil {
				r.deps.Log.Warn("source failed", "source", slug, "err", err)
				j.srcMu.Lock()
				j.srcErrs = append(j.srcErrs, slug+": "+err.Error())
				j.srcMu.Unlock()
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				for cand := range ch {
					select {
					case <-sctx.Done():
						return
					case out <- cand:
					}
				}
			}()
		}
		wg.Wait()
	}()
	return out
}

func fSeedKeyword(j *Job) string {
	if j.Search.Keyword != "" {
		return j.Search.Keyword
	}
	return j.Search.Query
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// Process runs one candidate through normalize → dedupe → crawl/enrich →
// persist → score. Errors are counted, never propagated.
func (r *Runner) Process(ctx context.Context, j *Job, cand source.RawLead) {
	d := r.deps
	tid := j.Search.TenantID

	key := strings.ToLower(cand.Website)
	if key == "" {
		key = strings.ToLower(cand.Domain)
	}
	if key == "" {
		key = strings.ToLower(cand.Email + "|" + cand.Name)
	}
	j.seenMu.Lock()
	if key == "" || j.seen[key] {
		j.seenMu.Unlock()
		return
	}
	j.seen[key] = true
	j.seenMu.Unlock()
	j.found.Add(1)

	f := j.Search.Filters
	n := lead.NormalizeCandidate(cand.Name, cand.Website, cand.Email, cand.Phone,
		"", firstNonEmpty(cand.Country, f.Country), firstNonEmpty(cand.Province, f.Province),
		firstNonEmpty(cand.City, f.City), cand.Address, firstNonEmpty(cand.Industry, j.Search.Industry))

	if f.HasWebsite && n.Website == "" {
		return // filtered before crawl; raw not stored
	}

	payload, _ := json.Marshal(map[string]any{
		"source_url": cand.SourceURL, "external_id": cand.ExternalID,
		"rating": cand.Payload["rating"], "lat": cand.Payload["lat"], "lon": cand.Payload["lon"],
	})
	var rawID string
	err := d.Pool.QueryRow(ctx, `
		INSERT INTO raw_leads (tenant_id, search_id, source_slug, external_id, name, domain,
			website, phone, email, address, city, province, country, industry, payload)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) RETURNING id::text`,
		tid, j.Search.ID, cand.SourceSlug, cand.ExternalID, n.Name, n.Domain, n.Website,
		n.PhoneE164, n.Email, n.Address, n.City, n.Province, n.Country, n.Industry, string(payload)).Scan(&rawID)
	if err != nil {
		d.Log.Warn("raw insert failed", "err", err)
		j.failed.Add(1)
		return
	}

	// dedupe against existing companies
	match, _ := lead.FindDuplicate(ctx, d.Pool, tid, n, cand.ExternalID, cand.SourceSlug)
	if match != nil && match.AutoLink {
		_, _ = d.Pool.Exec(ctx, `
			UPDATE raw_leads SET status='matched', company_id=$2, duplicate_of=$2, confidence=$3 WHERE id=$1`,
			rawID, match.CompanyID, match.Confidence)
		_, _ = d.Pool.Exec(ctx, `
			INSERT INTO leads (tenant_id, company_id, source, source_search_id, owner_id)
			VALUES ($1,$2,$3,$4,$5) ON CONFLICT (tenant_id, company_id) DO NOTHING`,
			tid, match.CompanyID, cand.SourceSlug, j.Search.ID, nullUUID(j.Search.UserID))
		j.dup.Add(1)
		return
	}
	dupOf, conf := "", 0
	if match != nil {
		dupOf, conf = match.CompanyID, match.Confidence
	}

	// crawl + enrich when we have a website
	enriched := false
	enrichData, enrichErr, enrichConf := "{}", "", 0
	if n.Website != "" {
		res := crawler.CrawlSite(ctx, d.Guard, d.Robots, n.Website, crawler.SiteConfig{
			MaxPages: d.Cfg.CrawlerMaxPagesPerSite, MaxDepth: d.Cfg.CrawlerMaxDepth,
			Workers: 4, DomainConc: d.Cfg.CrawlerDomainConcurrency,
			Timeout: d.Cfg.CrawlerTimeout, MaxBody: d.Cfg.CrawlerMaxBodyBytes,
			UserAgent: "LeadForgeBot/1.0 (+lead discovery; respects robots.txt)",
			Stop:      &j.stop,
		})
		crawlStatus := "completed"
		if res.Pages == 0 {
			crawlStatus = "failed"
		}
		crawlErr := strings.Join(res.Errors, "; ")
		if len(crawlErr) > 2000 {
			crawlErr = crawlErr[:2000]
		}
		_, _ = d.Pool.Exec(ctx, `
			INSERT INTO crawl_jobs (tenant_id, search_id, url, status, pages_crawled, error, finished_at)
			VALUES ($1,$2,$3,$4,$5,$6,now())`,
			tid, j.Search.ID, n.Website, crawlStatus, res.Pages, crawlErr)
		if res.Pages > 0 {
			j.crawled.Add(1)
		} else {
			j.failed.Add(1)
			_, _ = d.Pool.Exec(ctx, `UPDATE raw_leads SET status='failed' WHERE id=$1`, rawID)
			return
		}
		ex := res.Data
		if ex.CompanyName != "" && n.Name == "" {
			n.Name = lead.NormalizeName(ex.CompanyName)
		}
		if ex.PrimaryEmail() != "" && n.Email == "" {
			n.Email = lead.NormalizeEmail(ex.PrimaryEmail())
		}
		if ex.PrimaryPhone() != "" && n.PhoneE164 == "" {
			n.Phone, n.PhoneE164 = lead.NormalizePhone(ex.PrimaryPhone())
		}
		if n.Address == "" {
			n.Address = ex.Address
		}
		if wa, _ := lead.NormalizePhone(ex.PrimaryWhatsApp()); wa != "" && n.WhatsApp == "" {
			n.WhatsApp, _ = lead.NormalizePhone(ex.PrimaryWhatsApp())
		}
		if len(res.Errors) > 0 {
			enrichErr = strings.Join(res.Errors[:min(3, len(res.Errors))], "; ")
		}
		filled := 0
		for _, v := range []string{n.Name, n.Email, n.Phone, n.Address} {
			if v != "" {
				filled++
			}
		}
		filled += len(ex.Socials)
		enrichConf = filled * 10
		if enrichConf > 100 {
			enrichConf = 100
		}
		eb, _ := json.Marshal(map[string]any{
			"description": ex.Description, "email": n.Email, "phone": n.Phone,
			"whatsapp": ex.PrimaryWhatsApp(), "socials": ex.Socials,
			"technologies": ex.Technologies, "pages": res.Pages,
			"confidence": enrichConf, "source_url": n.Website,
			"js_required": ex.JSRequired,
		})
		enrichData = string(eb)
		enriched = true
	}

	if f.HasEmail && n.Email == "" {
		return
	}
	if f.HasPhone && n.PhoneE164 == "" {
		return
	}
	if f.HasWhatsApp && n.WhatsApp == "" {
		return
	}

	// company upsert (partial unique on non-empty domain)
	var companyID string
	created := false
	if n.Domain != "" {
		err = d.Pool.QueryRow(ctx, `
			INSERT INTO companies (tenant_id, name, domain, website, description, industry, country, province, city, address, phone, whatsapp, source, external_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			ON CONFLICT (tenant_id, domain) WHERE domain <> '' DO NOTHING RETURNING id::text`,
			tid, n.Name, n.Domain, n.Website, enrichDesc(enrichData), n.Industry, n.Country,
			n.Province, n.City, n.Address, n.PhoneE164, n.WhatsApp, cand.SourceSlug, cand.ExternalID).Scan(&companyID)
		if err != nil { // conflict → link existing
			_ = d.Pool.QueryRow(ctx, `SELECT id::text FROM companies WHERE tenant_id=$1 AND domain=$2`,
				tid, n.Domain).Scan(&companyID)
			if companyID != "" {
				_, _ = d.Pool.Exec(ctx, `UPDATE raw_leads SET status='matched', company_id=$2, duplicate_of=$2, confidence=80 WHERE id=$1`, rawID, companyID)
				j.dup.Add(1)
				return
			}
			j.failed.Add(1)
			_, _ = d.Pool.Exec(ctx, `UPDATE raw_leads SET status='failed' WHERE id=$1`, rawID)
			return
		}
		created = true
	} else {
		if n.Name == "" {
			j.failed.Add(1)
			_, _ = d.Pool.Exec(ctx, `UPDATE raw_leads SET status='failed' WHERE id=$1`, rawID)
			return
		}
		if err = d.Pool.QueryRow(ctx, `
			INSERT INTO companies (tenant_id, name, website, description, industry, country, province, city, address, phone, whatsapp, source, external_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id::text`,
			tid, n.Name, n.Website, enrichDesc(enrichData), n.Industry, n.Country,
			n.Province, n.City, n.Address, n.PhoneE164, n.WhatsApp, cand.SourceSlug, cand.ExternalID).Scan(&companyID); err != nil {
			j.failed.Add(1)
			_, _ = d.Pool.Exec(ctx, `UPDATE raw_leads SET status='failed' WHERE id=$1`, rawID)
			return
		}
		created = true
	}
	if created {
		j.saved.Add(1)
	}
	// enrichment record now that the company exists
	if n.Website != "" {
		_, _ = d.Pool.Exec(ctx, `
			INSERT INTO lead_enrichments (tenant_id, company_id, kind, status, data, error)
			VALUES ($1,$2,'website','completed',$3,$4)`, tid, companyID, enrichData, enrichErr)
		if enriched {
			j.enriched.Add(1)
		}
	}
	_, _ = d.Pool.Exec(ctx, `UPDATE raw_leads SET status='matched', company_id=$2, duplicate_of=$3, confidence=$4 WHERE id=$1`,
		rawID, companyID, nullStr(dupOf), dupConf(conf, created))

	// primary contact from best email
	var contactID *string
	if n.Email != "" {
		var vres verify.Result
		vres, _ = d.Verifier.Verify(ctx, n.Email)
		var cid string
		_ = d.Pool.QueryRow(ctx, `
			INSERT INTO contacts (tenant_id, company_id, full_name, email, email_status, phone, whatsapp, source, source_url)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (tenant_id, email) WHERE email <> '' DO NOTHING RETURNING id::text`,
			tid, companyID, n.Name, n.Email, vres.Status, n.PhoneE164, n.WhatsApp, cand.SourceSlug, n.Website).Scan(&cid)
		if cid == "" {
			_ = d.Pool.QueryRow(ctx, `SELECT id::text FROM contacts WHERE tenant_id=$1 AND email=$2`,
				tid, n.Email).Scan(&cid)
		}
		if cid != "" {
			contactID = &cid
		}
		_ = vres
	}

	// lead row
	_, _ = d.Pool.Exec(ctx, `
		INSERT INTO leads (tenant_id, company_id, primary_contact_id, status, source, source_search_id, owner_id)
		VALUES ($1,$2,$3,'new',$4,$5,$6) ON CONFLICT (tenant_id, company_id) DO NOTHING`,
		tid, companyID, nullStrPtr(contactID), cand.SourceSlug, j.Search.ID, nullUUID(j.Search.UserID))

	// rule scoring
	sig := scoring.Signals{
		Bool: map[string]bool{
			"has_website":    n.Website != "",
			"has_email":      n.Email != "",
			"email_verified": false,
			"has_phone":      n.PhoneE164 != "",
			"has_whatsapp":   n.WhatsApp != "",
		},
		Text: map[string]string{
			"industry": n.Industry, "city": n.City, "province": n.Province, "country": n.Country,
		},
	}
	if n.Email != "" {
		if v, _ := d.Verifier.Verify(ctx, n.Email); v.Status == verify.StatusValid {
			sig.Bool["email_verified"] = true
		}
	}
	if qi := strings.ToLower(j.Search.Industry); qi != "" {
		ci := strings.ToLower(n.Industry)
		sig.Bool["industry_match"] = ci != "" && (strings.Contains(ci, qi) || strings.Contains(qi, ci))
	}
	if qc := strings.ToLower(firstNonEmpty(f.City, j.Search.Location)); qc != "" {
		sig.Bool["location_match"] = strings.Contains(strings.ToLower(n.City+" "+n.Province), qc) ||
			strings.Contains(qc, strings.ToLower(n.City))
	}
	score, breakdown := scoring.Evaluate(sig, j.Rules)
	status := scoring.StatusForScore(score)
	bb, _ := json.Marshal(breakdown)
	var leadID string
	_ = d.Pool.QueryRow(ctx, `SELECT id::text FROM leads WHERE tenant_id=$1 AND company_id=$2`,
		tid, companyID).Scan(&leadID)
	if leadID != "" {
		_, _ = d.Pool.Exec(ctx, `UPDATE leads SET lead_score=$2, status=$3, last_activity_at=now() WHERE id=$1`,
			leadID, score, status)
		_, _ = d.Pool.Exec(ctx, `
			INSERT INTO lead_scores (tenant_id, lead_id, score, breakdown)
			VALUES ($1,$2,$3,$4) ON CONFLICT (lead_id) DO UPDATE SET score=$3, breakdown=$4, calculated_at=now()`,
			tid, leadID, score, string(bb))
		_, _ = d.Pool.Exec(ctx, `UPDATE companies SET lead_score=$2 WHERE id=$1`, companyID, score)
		if contactID != nil {
			_, _ = d.Pool.Exec(ctx, `UPDATE contacts SET lead_score=$2 WHERE id=$1`, *contactID, score)
		}
		if score >= 50 {
			j.qualified.Add(1)
			_, _ = d.Pool.Exec(ctx, `
				INSERT INTO activities (tenant_id, kind, subject, lead_id, company_id, user_id)
				VALUES ($1,'lead.qualified',$2,$3,$4,$5)`,
				tid, n.Name+" qualified ("+itoa(score)+")", leadID, companyID, nullUUID(j.Search.UserID))
		}
		_, _ = d.Pool.Exec(ctx, `
			INSERT INTO activities (tenant_id, kind, subject, lead_id, company_id, user_id)
			VALUES ($1,'lead.created',$2,$3,$4,$5)`,
			tid, "Lead created from "+cand.SourceSlug, leadID, companyID, nullUUID(j.Search.UserID))
	}
}

func enrichDesc(enrichJSON string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(enrichJSON), &m); err != nil {
		return ""
	}
	if s, ok := m["description"].(string); ok {
		if len(s) > 2000 {
			return s[:2000]
		}
		return s
	}
	return ""
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullStrPtr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func dupConf(conf int, created bool) int {
	if created && conf == 0 {
		return 90
	}
	return conf
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
