package dashboard

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Data powers the dashboard page.
type Data struct {
	Loaded       bool
	UserName     string
	Total        int64
	Qualified    int64
	Contacted    int64
	Replies      int64
	Meetings     int64
	Won          int64
	SparkTotal   []float64
	LeadGrowth   []Point
	Funnel       []FunnelRow
	Sources      []NameValue
	RecentLeads  []LeadRow
	HotLeads     []LeadRow
	Activities   []ActivityRow
	RecentSearch []SearchRow
}

// Metric is a KPI card.
type Metric struct {
	Label string
	Value string
	Delta float64
	Good  bool
	Spark []float64
}

// Point is a chart data point.
type Point struct {
	Label string
	Value float64
}

// FunnelRow is pipeline funnel data.
type FunnelRow struct {
	Label string
	Value int64
}

// NameValue for breakdowns.
type NameValue struct {
	Name  string
	Value int64
}

// LeadRow is a compact lead row.
type LeadRow struct {
	ID       string
	Company  string
	City     string
	Industry string
	Score    int
	Status   string
	Contact  string
	Email    string
	When     string
}

// ActivityRow for feeds.
type ActivityRow struct {
	Kind string
	Text string
	Who  string
	When string
}

// SearchRow for recent searches.
type SearchRow struct {
	ID        string
	Query     string
	Location  string
	Status    string
	Found     int64
	Saved     int64
	Qualified int64
	When      string
}

// SearchResult for global search.
type SearchResult struct {
	Type string
	ID   string
	Text string
	Sub  string
	Href string
}

// Load fetches dashboard data for tenant.
func Load(ctx context.Context, pool *pgxpool.Pool, tenantID, userName string) *Data {
	d := &Data{UserName: userName}
	if tenantID == "" {
		return d
	}

	_ = pool.QueryRow(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status IN ('qualified','hot')),
			COUNT(*) FILTER (WHERE status = 'contacted')
		FROM leads WHERE tenant_id = $1 AND status <> 'archived'`, tenantID).
		Scan(&d.Total, &d.Qualified, &d.Contacted)

	_ = pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT l.company_id)
		FROM email_messages m
		JOIN campaign_contacts cc ON cc.contact_id = m.contact_id
		JOIN leads l ON l.id = cc.lead_id
		WHERE l.tenant_id = $1 AND m.direction = 'in'`, tenantID).Scan(&d.Replies)
	if d.Replies == 0 {
		d.Replies = countQuery(ctx, pool, tenantID, `SELECT COUNT(*) FROM activities WHERE tenant_id=$1 AND kind = 'reply'`)
	}
	d.Meetings = countQuery(ctx, pool, tenantID, `SELECT COUNT(*) FROM tasks WHERE tenant_id=$1 AND kind='meeting' AND completed_at IS NOT NULL`)
	d.Won = countQuery(ctx, pool, tenantID, `SELECT COUNT(*) FROM deals WHERE tenant_id=$1 AND status='won'`)

	// lead growth last 14 days
	rows, err := pool.Query(ctx, `
		SELECT d::date::text, COALESCE(c,0) FROM generate_series(now()::date - interval '13 days', now()::date, interval '1 day') d
		LEFT JOIN (
			SELECT created_at::date AS day, COUNT(*) c FROM leads
			WHERE tenant_id = $1 AND created_at > now() - interval '14 days'
			GROUP BY 1
		) x ON x.day = d::date
		ORDER BY d`, tenantID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var p Point
			var v int64
			_ = rows.Scan(&p.Label, &v)
			p.Value = float64(v)
			d.LeadGrowth = append(d.LeadGrowth, p)
			d.SparkTotal = append(d.SparkTotal, p.Value)
		}
	}

	// funnel
	funnelDefs := []struct {
		label string
		q     string
	}{
		{"Total Leads", `SELECT COUNT(*) FROM leads WHERE tenant_id=$1 AND status <> 'archived'`},
		{"Qualified", `SELECT COUNT(*) FROM leads WHERE tenant_id=$1 AND status IN ('qualified','hot','contacted')`},
		{"Contacted", `SELECT COUNT(*) FROM leads WHERE tenant_id=$1 AND status = 'contacted'`},
		{"Replied", `SELECT COUNT(*) FROM campaign_contacts WHERE tenant_id=$1 AND status='replied'`},
		{"Meetings", `SELECT COUNT(*) FROM tasks WHERE tenant_id=$1 AND kind='meeting' AND completed_at IS NOT NULL`},
		{"Deals Won", `SELECT COUNT(*) FROM deals WHERE tenant_id=$1 AND status='won'`},
	}
	for _, fd := range funnelDefs {
		d.Funnel = append(d.Funnel, FunnelRow{Label: fd.label, Value: countQuery(ctx, pool, tenantID, fd.q)})
	}

	// sources
	rows, err = pool.Query(ctx, `
		SELECT COALESCE(NULLIF(source,''),'unknown') s, COUNT(*) FROM leads
		WHERE tenant_id = $1 GROUP BY 1 ORDER BY 2 DESC LIMIT 6`, tenantID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var nv NameValue
			_ = rows.Scan(&nv.Name, &nv.Value)
			d.Sources = append(d.Sources, nv)
		}
	}

	// recent + hot leads
	loadLeadRows := func(q string, dst *[]LeadRow) {
		rows, err := pool.Query(ctx, q, tenantID)
		if err != nil {
			return
		}
		defer rows.Close()
		for rows.Next() {
			var r LeadRow
			var contact, email *string
			var created time.Time
			if err := rows.Scan(&r.ID, &r.Company, &r.City, &r.Industry, &r.Score, &r.Status, &contact, &email, &created); err == nil {
				if contact != nil {
					r.Contact = *contact
				}
				if email != nil {
					r.Email = *email
				}
				r.When = created.Format("02 Jan")
				*dst = append(*dst, r)
			}
		}
	}
	loadLeadRows(`
		SELECT l.id, c.name, c.city, c.industry, l.lead_score, l.status, ct.full_name, ct.email, l.created_at
		FROM leads l JOIN companies c ON c.id = l.company_id
		LEFT JOIN contacts ct ON ct.id = l.primary_contact_id
		WHERE l.tenant_id = $1 AND l.status <> 'archived'
		ORDER BY l.created_at DESC LIMIT 8`, &d.RecentLeads)
	loadLeadRows(`
		SELECT l.id, c.name, c.city, c.industry, l.lead_score, l.status, ct.full_name, ct.email, l.created_at
		FROM leads l JOIN companies c ON c.id = l.company_id
		LEFT JOIN contacts ct ON ct.id = l.primary_contact_id
		WHERE l.tenant_id = $1 AND l.lead_score >= 70 AND l.status <> 'archived'
		ORDER BY l.lead_score DESC LIMIT 6`, &d.HotLeads)

	// activities
	rows, err = pool.Query(ctx, `
		SELECT a.kind, COALESCE(NULLIF(a.subject,''), a.kind), COALESCE(u.name,'System'), a.created_at
		FROM activities a LEFT JOIN users u ON u.id = a.user_id
		WHERE a.tenant_id = $1 ORDER BY a.created_at DESC LIMIT 10`, tenantID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var a ActivityRow
			var t time.Time
			_ = rows.Scan(&a.Kind, &a.Text, &a.Who, &t)
			a.When = t.Format("02 Jan 15:04")
			d.Activities = append(d.Activities, a)
		}
	}

	// recent searches
	rows, err = pool.Query(ctx, `
		SELECT id::text, COALESCE(NULLIF(keyword,''), query), location, status, found_count, saved_count, qualified_count, created_at
		FROM lead_searches WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT 5`, tenantID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var sr SearchRow
			var t time.Time
			_ = rows.Scan(&sr.ID, &sr.Query, &sr.Location, &sr.Status, &sr.Found, &sr.Saved, &sr.Qualified, &t)
			sr.When = t.Format("02 Jan 15:04")
			d.RecentSearch = append(d.RecentSearch, sr)
		}
	}

	d.Loaded = d.Total > 0
	return d
}

// GlobalSearch searches companies, contacts, leads, deals, campaigns.
func GlobalSearch(ctx context.Context, pool *pgxpool.Pool, tenantID, q string) []SearchResult {
	if tenantID == "" || q == "" {
		return nil
	}
	like := "%" + q + "%"
	var out []SearchResult
	scan := func(typ, href string) func(func(SearchResult) bool) error {
		return nil
	}
	_ = scan
	rows, err := pool.Query(ctx, `
		SELECT id::text, name, COALESCE(city,''), 'Company' FROM companies
		WHERE tenant_id = $1 AND (name ILIKE $2 OR domain ILIKE $2) LIMIT 5`, tenantID, like)
	if err == nil {
		for rows.Next() {
			var r SearchResult
			_ = rows.Scan(&r.ID, &r.Text, &r.Sub, &r.Type)
			r.Href = "/companies/" + r.ID
			out = append(out, r)
		}
		rows.Close()
	}
	rows, err = pool.Query(ctx, `
		SELECT l.id::text, c.name, COALESCE(c.city,'') || ' · ' || l.lead_score::text, 'Lead' FROM leads l
		JOIN companies c ON c.id = l.company_id
		WHERE l.tenant_id = $1 AND (c.name ILIKE $2 OR c.domain ILIKE $2) AND l.status <> 'archived' LIMIT 5`, tenantID, like)
	if err == nil {
		for rows.Next() {
			var r SearchResult
			_ = rows.Scan(&r.ID, &r.Text, &r.Sub, &r.Type)
			r.Href = "/leads/" + r.ID
			out = append(out, r)
		}
		rows.Close()
	}
	rows, err = pool.Query(ctx, `
		SELECT id::text, COALESCE(NULLIF(full_name,''), email), COALESCE(email,''), 'Contact' FROM contacts
		WHERE tenant_id = $1 AND (full_name ILIKE $2 OR email ILIKE $2) LIMIT 5`, tenantID, like)
	if err == nil {
		for rows.Next() {
			var r SearchResult
			_ = rows.Scan(&r.ID, &r.Text, &r.Sub, &r.Type)
			r.Href = "/people/" + r.ID
			out = append(out, r)
		}
		rows.Close()
	}
	rows, err = pool.Query(ctx, `
		SELECT id::text, title, COALESCE(status,''), 'Deal' FROM deals
		WHERE tenant_id = $1 AND title ILIKE $2 LIMIT 5`, tenantID, like)
	if err == nil {
		for rows.Next() {
			var r SearchResult
			_ = rows.Scan(&r.ID, &r.Text, &r.Sub, &r.Type)
			r.Href = "/deals/" + r.ID
			out = append(out, r)
		}
		rows.Close()
	}
	return out
}

func countQuery(ctx context.Context, pool *pgxpool.Pool, tenantID, q string) int64 {
	var n int64
	_ = pool.QueryRow(ctx, q, tenantID).Scan(&n)
	return n
}
