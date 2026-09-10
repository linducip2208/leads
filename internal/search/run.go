package search

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"leadforge/internal/crawler"
	"leadforge/internal/lead"
	"leadforge/internal/metrics"
	"leadforge/internal/scoring"
	"leadforge/internal/source"
	"leadforge/internal/verify"
)

// failure reasons for breakdowns.
const (
	FailNoWebsite = "NO_WEBSITE"
	FailRobots    = "ROBOTS"
	FailTimeout   = "TIMEOUT"
	FailHTTP403   = "HTTP_403"
	FailHTTP404   = "HTTP_404"
	FailHTTP429   = "HTTP_429"
	FailHTTP5xx   = "HTTP_5XX"
	FailDNS       = "DNS"
	FailSSRF      = "SSRF_BLOCKED"
	FailBadURL    = "INVALID_URL"
	FailParse     = "PARSE"
	FailNoData    = "NO_DATA"
)

// classifyCrawlError maps crawl errors to breakdown reasons.
func classifyCrawlError(errs []string) string {
	s := strings.ToLower(strings.Join(errs, "; "))
	switch {
	case s == "":
		return FailNoData
	case strings.Contains(s, "robots"):
		return FailRobots
	case strings.Contains(s, "blocked by crawl policy"):
		return FailSSRF
	case strings.Contains(s, "timeout") || strings.Contains(s, "deadline"):
		return FailTimeout
	case strings.Contains(s, "no such host") || strings.Contains(s, "dns"):
		return FailDNS
	case strings.Contains(s, "403"):
		return FailHTTP403
	case strings.Contains(s, "404"):
		return FailHTTP404
	case strings.Contains(s, "429"):
		return FailHTTP429
	case strings.Contains(s, "500") || strings.Contains(s, "502") || strings.Contains(s, "503") || strings.Contains(s, "504"):
		return FailHTTP5xx
	default:
		return FailNoData
	}
}

// Run is the worker entry point: claim, discover, process, finish.
func (r *Runner) Run(ctx context.Context, searchID string) error {
	j, err := r.Load(ctx, searchID)
	if err != nil {
		return err
	}
	if !r.Claim(ctx, j) {
		return nil // already claimed/finished elsewhere
	}
	metrics.Searches.Add(1)
	t0 := time.Now()
	cands := r.Discover(ctx, j)
	eg, ctx2 := errgroup.WithContext(ctx)
	eg.SetLimit(r.deps.Cfg.SearchProcessWorkers)
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
			if j.found.Load()%10 == 0 {
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
	r.deps.Log.Info("search run done", "search_id", j.Search.ID,
		"tenant_id", j.Search.TenantID, "duration_ms", time.Since(t0).Milliseconds())
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
	var st string
	_ = r.deps.Pool.QueryRow(ctx, `SELECT status FROM lead_searches WHERE id=$1`, j.Search.ID).Scan(&st)
	if st == "cancelled" || st == "failed" {
		final = st
		note = ""
	}
	r.Finish(ctx, j, final, note)
	return nil
}

// Discover fans in all selected+applicable providers (priority order).
// Per-source stats feed the search report.
func (r *Runner) Discover(ctx context.Context, j *Job) <-chan source.RawLead {
	out := make(chan source.RawLead, 64)
	f := j.Search.Filters
	q := source.SearchQuery{
		Keyword: fSeedKeyword(j), Industry: j.Search.Industry,
		Country:  firstNonEmpty(f.Country, j.Search.Country),
		Province: f.Province, City: f.City, CompanySize: f.CompanySize,
		Limit: j.Search.Limit, SeedURLs: f.SeedURLs, Sources: f.Sources,
		GoogleKey: j.GoogleKey,
	}
	go func() {
		defer close(out)
		sctx, cancel := context.WithCancel(ctx)
		defer cancel()
		var wg sync.WaitGroup
		for _, slug := range r.deps.Registry.Slugs() {
			prov, _ := r.deps.Registry.Get(slug)
			if prov == nil || !source.Wants(q, slug) || !source.Applicable(q, slug) {
				continue
			}
			if slug == "csv" {
				continue // imports run inline, not via finder
			}
			if off := j.SourceOff[slug]; off {
				continue // tenant-disabled connector
			}
			t0 := time.Now()
			ch, err := prov.Search(sctx, q)
			if err != nil {
				r.deps.Log.Warn("source failed", "search_id", j.Search.ID,
					"tenant_id", j.Search.TenantID, "source", slug, "err", err)
				j.srcMu.Lock()
				j.srcErrs = append(j.srcErrs, slug+": "+err.Error())
				j.srcMu.Unlock()
				j.bumpSrcErr(slug, err.Error())
				continue
			}
			wg.Add(1)
			go func(slug string, ch <-chan source.RawLead, t0 time.Time) {
				defer wg.Done()
				n := 0
				for cand := range ch {
					n++
					select {
					case <-sctx.Done():
						return
					case out <- cand:
					}
				}
				j.statMu.Lock()
				st := j.srcStats[slug]
				st.Candidates += n
				st.DurationMs += time.Since(t0).Milliseconds()
				j.srcStats[slug] = st
				j.statMu.Unlock()
			}(slug, ch, t0)
		}
		wg.Wait()
	}()
	return out
}

// fail marks a raw lead failed with a breakdown reason.
func (r *Runner) fail(ctx context.Context, j *Job, rawID, reason string) {
	_, _ = r.deps.Pool.Exec(ctx, `UPDATE raw_leads SET status='failed', fail_reason=$2 WHERE id=$1`, rawID, reason)
	j.failed.Add(1)
	metrics.CrawlFailed.Add(1)
}

// filtered drops a candidate that fails late filters (kept, not an error).
func (r *Runner) filtered(ctx context.Context, j *Job, rawID string) {
	_, _ = r.deps.Pool.Exec(ctx, `UPDATE raw_leads SET status='filtered' WHERE id=$1`, rawID)
	j.filtered.Add(1)
}

// Process runs one candidate: normalize → dedupe → crawl/enrich → persist →
// score. Minimum-score filtering happens AFTER scoring, never before.
// Errors are counted with reasons, never propagated.
func (r *Runner) Process(ctx context.Context, j *Job, cand source.RawLead) {
	d := r.deps
	tid := j.Search.TenantID
	log := d.Log.With("search_id", j.Search.ID, "tenant_id", tid,
		"source", cand.SourceSlug, "domain", cand.Domain)

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
	metrics.Candidates.Add(1)
	j.bumpSrc(cand.SourceSlug, true)

	f := j.Search.Filters
	n := lead.NormalizeCandidate(cand.Name, cand.Website, cand.Email, cand.Phone,
		"", firstNonEmpty(cand.Country, f.Country), firstNonEmpty(cand.Province, f.Province),
		firstNonEmpty(cand.City, f.City), cand.Address, firstNonEmpty(cand.Industry, j.Search.Industry))

	var rawID string
	err := d.Pool.QueryRow(ctx, `
		INSERT INTO raw_leads (tenant_id, search_id, source_slug, source_confidence, external_id, name, domain,
			website, phone, email, address, city, province, country, industry, payload, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,'discovered') RETURNING id::text`,
		tid, j.Search.ID, cand.SourceSlug, cand.SourceConf, cand.ExternalID, n.Name, n.Domain, n.Website,
		n.PhoneE164, n.Email, n.Address, n.City, n.Province, n.Country, n.Industry,
		string(payloadJSON(cand))).Scan(&rawID)
	if err != nil {
		log.Warn("raw insert failed", "err", err)
		j.failed.Add(1)
		return
	}
	j.discovered.Add(1)
	_, _ = d.Pool.Exec(ctx, `UPDATE raw_leads SET status='normalized' WHERE id=$1`, rawID)

	// cheap pre-store filters (before expensive crawl)
	if f.HasWebsite && n.Website == "" {
		r.filtered(ctx, j, rawID)
		return
	}

	// dedupe against existing companies
	match, _ := lead.FindDuplicate(ctx, d.Pool, tid, n, cand.ExternalID, cand.SourceSlug)
	if match != nil && match.AutoLink {
		r.linkExisting(ctx, j, cand, n, rawID, match)
		return
	}
	dupOf, dupConf := "", 0
	if match != nil {
		dupOf, dupConf = match.CompanyID, match.Confidence
	}

	// crawl when we have a website; source-only data can still convert
	var ex *crawler.Extracted
	canon := n.Domain
	quality := 0
	if n.Website != "" {
		res := d.Crawler.Crawl(ctx, n.Website, crawler.SiteConfig{
			MaxPages: d.Cfg.CrawlerMaxPagesPerSite, MaxDepth: d.Cfg.CrawlerMaxDepth,
			Workers: d.Cfg.CrawlerSiteWorkers, DomainConc: d.Cfg.CrawlerDomainConcurrency,
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
		if res.Pages == 0 {
			r.fail(ctx, j, rawID, classifyCrawlError(res.Errors))
			return
		}
		j.crawled.Add(1)
		metrics.Crawls.Add(1)
		_, _ = d.Pool.Exec(ctx, `UPDATE raw_leads SET status='crawled' WHERE id=$1`, rawID)
		ex = res.Data
		if ex.CompanyName != "" && n.Name == "" {
			n.Name = lead.NormalizeName(ex.CompanyName)
		}
		if n.Domain != "" && len(ex.Emails) > 0 {
			ex.Emails = crawler.RankEmails(ex.Emails, n.Domain)
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
		if res.FinalURL != "" {
			if cu, err := url.Parse(res.FinalURL); err == nil {
				if cd := lead.CanonicalDomain(cu.Host); cd != "" {
					canon = cd
				}
			}
		}
		quality = crawler.QualityScore(ex, res.Pages)
		_, _ = d.Pool.Exec(ctx, `UPDATE raw_leads SET quality=$2 WHERE id=$1`, rawID, quality)
	} else if n.Email == "" && n.PhoneE164 == "" {
		r.fail(ctx, j, rawID, FailNoWebsite)
		return
	}

	// late contactability filters
	if f.HasEmail && !contactableEmail(n.Email) {
		r.filtered(ctx, j, rawID)
		return
	}
	if f.HasPhone && n.PhoneE164 == "" {
		r.filtered(ctx, j, rawID)
		return
	}
	if f.HasWhatsApp && n.WhatsApp == "" {
		r.filtered(ctx, j, rawID)
		return
	}

	r.persistNew(ctx, j, cand, n, rawID, ex, canon, quality, dupOf, dupConf)
}

// linkExisting links a candidate to a known company: refresh when stale,
// always ensure the lead exists, rescore, and count as matched (never dropped).
func (r *Runner) linkExisting(ctx context.Context, j *Job, cand source.RawLead, n lead.Normalized, rawID string, match *lead.Match) {
	d := r.deps
	tid := j.Search.TenantID
	var lastEnriched, lastCrawled *time.Time
	var companyID = match.CompanyID
	_ = d.Pool.QueryRow(ctx, `SELECT last_enriched_at, last_crawl_at FROM companies WHERE id=$1`,
		companyID).Scan(&lastEnriched, &lastCrawled)
	stale := isStale(lastEnriched, d.Cfg.EnrichFreshDays) || isStale(lastCrawled, d.Cfg.CrawlFreshDays)

	var ex *crawler.Extracted
	if stale {
		var website string
		_ = d.Pool.QueryRow(ctx, `SELECT website FROM companies WHERE id=$1`, companyID).Scan(&website)
		if website == "" {
			website = n.Website
		}
		if website != "" {
			res := d.Crawler.Crawl(ctx, website, crawler.SiteConfig{
				MaxPages: d.Cfg.CrawlerMaxPagesPerSite, MaxDepth: 1,
				Workers: 2, DomainConc: d.Cfg.CrawlerDomainConcurrency,
				Timeout: d.Cfg.CrawlerTimeout, MaxBody: d.Cfg.CrawlerMaxBodyBytes,
				UserAgent: "LeadForgeBot/1.0 (+lead discovery; respects robots.txt)",
				Stop:      &j.stop,
			})
			if res.Pages > 0 {
				j.crawled.Add(1)
				ex = res.Data
				eb, _ := json.Marshal(map[string]any{
					"refresh": true, "email": ex.PrimaryEmail(), "phone": ex.PrimaryPhone(),
					"pages": res.Pages, "quality": crawler.QualityScore(ex, res.Pages),
				})
				_, _ = d.Pool.Exec(ctx, `
					INSERT INTO lead_enrichments (tenant_id, company_id, kind, status, data, finished_at)
					VALUES ($1,$2,'website','completed',$3,now())`, tid, companyID, string(eb))
				_, _ = d.Pool.Exec(ctx, `UPDATE companies SET last_enriched_at=now(), last_crawl_at=now() WHERE id=$1`, companyID)
				j.enriched.Add(1)
				metrics.Enrichments.Add(1)
			}
		}
	}
	_, _ = d.Pool.Exec(ctx, `UPDATE raw_leads SET status='matched', company_id=$2, duplicate_of=$2, confidence=$3 WHERE id=$1`,
		rawID, companyID, match.Confidence)
	leadID := r.ensureLead(ctx, j, companyID, cand.SourceSlug)
	if leadID != "" {
		r.scoreLead(ctx, j, companyID, leadID, n, fOf(j))
	}
	j.dup.Add(1)
	j.matched.Add(1)
	metrics.Duplicates.Add(1)
}

// persistNew stores a brand-new company + contact + lead with enrichment,
// quality scores, opportunities and late min-score filtering.
func (r *Runner) persistNew(ctx context.Context, j *Job, cand source.RawLead, n lead.Normalized,
	rawID string, ex *crawler.Extracted, canon string, quality int, dupOf string, dupConf int) {
	d := r.deps
	tid := j.Search.TenantID
	f := j.Search.Filters

	enrichData := "{}"
	enrichConf := 0
	socialCount := 0
	descLen := 0
	var techs []crawler.Tech
	if ex != nil {
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
			"technologies": techNames(ex.Technologies), "confidence": enrichConf,
			"source_url": n.Website, "js_required": ex.JSRequired, "quality": quality,
		})
		enrichData = string(eb)
		socialCount = len(ex.Socials)
		descLen = len(ex.Description)
		techs = ex.Technologies
	}
	completeness := lead.Completeness(n.Name, n.Website, n.Email, n.PhoneE164, n.Address, n.Industry, socialCount, descLen)

	var companyID string
	created := false
	if n.Domain != "" {
		err := d.Pool.QueryRow(ctx, `
			INSERT INTO companies (tenant_id, name, domain, canonical_domain, website, description, industry, country, province, city, address, phone, whatsapp, source, external_id, data_quality, last_crawl_at, last_enriched_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,now(),now())
			ON CONFLICT (tenant_id, domain) WHERE domain <> '' DO NOTHING RETURNING id::text`,
			tid, n.Name, n.Domain, firstNonEmpty(canon, n.Domain), n.Website, enrichDesc(enrichData),
			n.Industry, n.Country, n.Province, n.City, n.Address, n.PhoneE164, n.WhatsApp,
			cand.SourceSlug, cand.ExternalID, completeness).Scan(&companyID)
		if err != nil {
			_ = d.Pool.QueryRow(ctx, `SELECT id::text FROM companies WHERE tenant_id=$1 AND domain=$2`,
				tid, n.Domain).Scan(&companyID)
			if companyID != "" {
				_, _ = d.Pool.Exec(ctx, `UPDATE raw_leads SET status='matched', company_id=$2, duplicate_of=$2, confidence=85 WHERE id=$1`, rawID, companyID)
				leadID := r.ensureLead(ctx, j, companyID, cand.SourceSlug)
				if leadID != "" {
					r.scoreLead(ctx, j, companyID, leadID, n, f)
				}
				j.dup.Add(1)
				j.matched.Add(1)
				return
			}
			r.fail(ctx, j, rawID, FailNoData)
			return
		}
		created = true
	} else {
		if n.Name == "" {
			r.fail(ctx, j, rawID, FailNoData)
			return
		}
		if err := d.Pool.QueryRow(ctx, `
			INSERT INTO companies (tenant_id, name, website, description, industry, country, province, city, address, phone, whatsapp, source, external_id, data_quality, last_crawl_at, last_enriched_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,now(),now()) RETURNING id::text`,
			tid, n.Name, n.Website, enrichDesc(enrichData), n.Industry, n.Country,
			n.Province, n.City, n.Address, n.PhoneE164, n.WhatsApp,
			cand.SourceSlug, cand.ExternalID, completeness).Scan(&companyID); err != nil {
			r.fail(ctx, j, rawID, FailNoData)
			return
		}
		created = true
	}
	if created {
		j.saved.Add(1)
		j.created.Add(1)
		metrics.LeadsCreated.Add(1)
	}
	_, _ = d.Pool.Exec(ctx, `
		INSERT INTO lead_enrichments (tenant_id, company_id, kind, status, data, finished_at)
		VALUES ($1,$2,'website','completed',$3,now())`, tid, companyID, enrichData)
	j.enriched.Add(1)
	metrics.Enrichments.Add(1)
	r.storeTech(ctx, tid, companyID, techs)
	_, _ = d.Pool.Exec(ctx, `UPDATE raw_leads SET status='enriched', company_id=$2, duplicate_of=$3, confidence=$4 WHERE id=$1`,
		rawID, companyID, nullStr(dupOf), dupConfOr90(dupConf, created))

	// primary contact with email classification
	contactID := r.ensureContact(ctx, j, companyID, n, cand)

	leadID := r.ensureLead(ctx, j, companyID, cand.SourceSlug)
	if contactID != "" && leadID != "" {
		_, _ = d.Pool.Exec(ctx, `UPDATE leads SET primary_contact_id=$2::uuid WHERE id=$1 AND primary_contact_id IS NULL`, leadID, contactID)
	}
	if leadID == "" {
		r.fail(ctx, j, rawID, FailNoData)
		return
	}

	// score, then LATE min-score filter (never drop before scoring)
	score := r.scoreLead(ctx, j, companyID, leadID, n, f)
	_, _ = d.Pool.Exec(ctx, `UPDATE leads SET data_quality=$2 WHERE id=$1`, leadID, completeness)
	if f.MinScore > 0 && score < f.MinScore {
		r.filtered(ctx, j, rawID)
		return
	}
	if contactable(n) {
		j.contactable.Add(1)
	}
	if score >= 90 {
		j.hot.Add(1)
	}
	if score >= 50 {
		j.qualified.Add(1)
		metrics.Qualified.Add(1)
		_, _ = d.Pool.Exec(ctx, `
			INSERT INTO activities (tenant_id, kind, subject, lead_id, company_id, user_id)
			VALUES ($1,'lead.qualified',$2,$3,$4,$5)`,
			tid, n.Name+" qualified ("+itoa(score)+")", leadID, companyID, nullUUID(j.Search.UserID))
	}
	_, _ = d.Pool.Exec(ctx, `
		INSERT INTO activities (tenant_id, kind, subject, lead_id, company_id, user_id)
		VALUES ($1,'lead.created',$2,$3,$4,$5)`,
		tid, "Lead created from "+cand.SourceSlug, leadID, companyID, nullUUID(j.Search.UserID))
	_, _ = d.Pool.Exec(ctx, `UPDATE raw_leads SET status='converted' WHERE id=$1`, rawID)

	// opportunities from technology signals
	r.storeOpportunities(ctx, j, leadID, companyID, n.Industry, techNames(techs), n.Website != "")
}

// ensureLead inserts the lead row idempotently and returns its id.
func (r *Runner) ensureLead(ctx context.Context, j *Job, companyID, sourceSlug string) string {
	_, _ = r.deps.Pool.Exec(ctx, `
		INSERT INTO leads (tenant_id, company_id, status, source, source_search_id, owner_id)
		VALUES ($1,$2,'new',$3,$4,$5) ON CONFLICT (tenant_id, company_id) DO NOTHING`,
		j.Search.TenantID, companyID, sourceSlug, j.Search.ID, nullUUID(j.Search.UserID))
	var leadID string
	_ = r.deps.Pool.QueryRow(ctx, `SELECT id::text FROM leads WHERE tenant_id=$1 AND company_id=$2`,
		j.Search.TenantID, companyID).Scan(&leadID)
	return leadID
}

// ensureContact inserts the primary contact with classification.
func (r *Runner) ensureContact(ctx context.Context, j *Job, companyID string, n lead.Normalized, cand source.RawLead) string {
	if n.Email == "" {
		return ""
	}
	kind := crawler.EmailKind(n.Email)
	var vres verify.Result
	vres, _ = r.deps.Verifier.Verify(ctx, n.Email)
	var cid string
	_ = r.deps.Pool.QueryRow(ctx, `
		INSERT INTO contacts (tenant_id, company_id, full_name, email, email_kind, email_status, phone, whatsapp, source, source_url)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (tenant_id, email) WHERE email <> '' DO NOTHING RETURNING id::text`,
		j.Search.TenantID, companyID, n.Name, n.Email, kind, vres.Status, n.PhoneE164, n.WhatsApp, cand.SourceSlug, n.Website).Scan(&cid)
	if cid == "" {
		_ = r.deps.Pool.QueryRow(ctx, `SELECT id::text FROM contacts WHERE tenant_id=$1 AND email=$2`,
			j.Search.TenantID, n.Email).Scan(&cid)
	}
	return cid
}

// scoreLead evaluates rules, persists score + status, returns the score.
func (r *Runner) scoreLead(ctx context.Context, j *Job, companyID, leadID string, n lead.Normalized, f Filters) int {
	d := r.deps
	tid := j.Search.TenantID
	sig := scoring.Signals{
		Bool: map[string]bool{
			"has_website":    n.Website != "",
			"has_email":      contactableEmail(n.Email),
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
	_, _ = d.Pool.Exec(ctx, `UPDATE leads SET lead_score=$2, status=$3, last_activity_at=now() WHERE id=$1`,
		leadID, score, status)
	_, _ = d.Pool.Exec(ctx, `
		INSERT INTO lead_scores (tenant_id, lead_id, score, breakdown)
		VALUES ($1,$2,$3,$4) ON CONFLICT (lead_id) DO UPDATE SET score=$3, breakdown=$4, calculated_at=now()`,
		tid, leadID, score, string(bb))
	_, _ = d.Pool.Exec(ctx, `UPDATE companies SET lead_score=$2 WHERE id=$1`, companyID, score)
	return score
}

// storeTech upserts technology evidence rows.
func (r *Runner) storeTech(ctx context.Context, tenantID, companyID string, techs []crawler.Tech) {
	if companyID == "" {
		return
	}
	for _, t := range techs {
		_, _ = r.deps.Pool.Exec(ctx, `
			INSERT INTO company_technologies (company_id, name, confidence, evidence)
			VALUES ($1::uuid,$2,60,$3) ON CONFLICT (company_id, name) DO UPDATE SET evidence=$3`,
			companyID, t.Name, t.Evidence)
	}
	_ = tenantID
}

// storeOpportunities writes detected product-fit angles.
func (r *Runner) storeOpportunities(ctx context.Context, j *Job, leadID, companyID, industry string, techs []string, hasWebsite bool) {
	for _, o := range lead.DetectOpportunities(industry, techs, hasWebsite) {
		var pid string
		_ = r.deps.Pool.QueryRow(ctx, `
			INSERT INTO products_services (tenant_id, name, description, is_active)
			VALUES ($1,$2,$3,true) ON CONFLICT DO NOTHING RETURNING id::text`,
			j.Search.TenantID, o.Title, o.Reason).Scan(&pid)
		if pid == "" {
			_ = r.deps.Pool.QueryRow(ctx, `SELECT id::text FROM products_services WHERE tenant_id=$1 AND name=$2`,
				j.Search.TenantID, o.Title).Scan(&pid)
		}
		if pid == "" {
			continue
		}
		_, _ = r.deps.Pool.Exec(ctx, `
			INSERT INTO lead_opportunities (tenant_id, lead_id, product_id, reason, score)
			VALUES ($1,$2::uuid,$3::uuid,$4,$5) ON CONFLICT (lead_id, product_id) DO NOTHING`,
			j.Search.TenantID, leadID, pid, o.Reason, o.Confidence)
	}
	_ = companyID
}

// --- helpers ---

func contactableEmail(email string) bool {
	if email == "" {
		return false
	}
	k := crawler.EmailKind(email)
	return k == "personal" || k == "role"
}

func contactable(n lead.Normalized) bool {
	return contactableEmail(n.Email) || n.PhoneE164 != ""
}

func isStale(t *time.Time, freshDays int) bool {
	if t == nil {
		return true
	}
	if freshDays <= 0 {
		return false
	}
	return time.Since(*t) > time.Duration(freshDays)*24*time.Hour
}

func techNames(techs []crawler.Tech) []string {
	out := make([]string, 0, len(techs))
	for _, t := range techs {
		out = append(out, t.Name)
	}
	return out
}

func payloadJSON(cand source.RawLead) []byte {
	b, _ := json.Marshal(map[string]any{
		"source_url": cand.SourceURL, "external_id": cand.ExternalID,
		"rating": cand.Payload["rating"], "lat": cand.Payload["lat"], "lon": cand.Payload["lon"],
	})
	return b
}

func parseURL(raw string) (*url.URL, error) { return url.Parse(raw) }

func fOf(j *Job) Filters { return j.Search.Filters }

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

func dupConfOr90(conf int, created bool) int {
	if created && conf == 0 {
		return 90
	}
	return conf
}

func dupConf(conf int, created bool) int { return dupConfOr90(conf, created) }

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

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func fSeedKeyword(j *Job) string {
	if j.Search.Keyword != "" {
		return j.Search.Keyword
	}
	return j.Search.Query
}
