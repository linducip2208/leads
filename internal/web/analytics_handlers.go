package web

import (
	"net/http"
	"strconv"
	"time"

	"leadforge/internal/role"
	"leadforge/web/layouts"
	"leadforge/web/pages/analytics"
)

func (s *Server) analyticsRoutes() {
	s.Router.HandleFunc("GET", "/analytics/leads", s.requirePerm(role.LeadRead, s.handleAnalyticsLeads))
	s.Router.HandleFunc("GET", "/analytics/campaigns", s.requirePerm(role.LeadRead, s.handleAnalyticsCampaigns))
	s.Router.HandleFunc("GET", "/analytics/sales", s.requirePerm(role.DealManage, s.handleAnalyticsSales))
	s.Router.HandleFunc("GET", "/analytics/sources", s.requirePerm(role.LeadRead, s.handleAnalyticsSources))
}

func analyticsTabs(active string) []analytics.Tab {
	return []analytics.Tab{
		{Label: "Leads", Href: "/analytics/leads", Active: active == "leads"},
		{Label: "Campaigns", Href: "/analytics/campaigns", Active: active == "campaigns"},
		{Label: "Sales", Href: "/analytics/sales", Active: active == "sales"},
		{Label: "Sources", Href: "/analytics/sources", Active: active == "sources"},
	}
}

func (s *Server) handleAnalyticsLeads(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	d := &analytics.PageData{Title: "Lead Analytics", Note: "Volume, quality and momentum of your database."}
	var total, qualified, hot int
	var avg float64
	_ = s.PG.QueryRow(r.Context(), `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE status IN ('qualified','hot','contacted')),
			COUNT(*) FILTER (WHERE status='hot'), COALESCE(AVG(lead_score),0)
		FROM leads WHERE tenant_id=$1 AND status<>'archived'`, id.TenantID).Scan(&total, &qualified, &hot, &avg)
	d.Stats = []analytics.Stat{
		{Label: "Total leads", Value: itoa(total)},
		{Label: "Qualified", Value: itoa(qualified)},
		{Label: "Hot", Value: itoa(hot)},
		{Label: "Avg score", Value: strconv.FormatFloat(avg, 'f', 1, 64)},
	}
	rows, _ := s.PG.Query(r.Context(), `
		SELECT status, COUNT(*) FROM leads WHERE tenant_id=$1 GROUP BY status ORDER BY 2 DESC`, id.TenantID)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var st string
			var n int
			if err := rows.Scan(&st, &n); err == nil {
				d.Bars = append(d.Bars, analytics.Bar{Label: st, Value: itoa(n), Count: n, Pct: pctOf(n, total)})
			}
		}
	}
	d.Days = s.daySeries(r, `SELECT to_char(created_at,'YYYY-MM-DD') FROM leads WHERE tenant_id=$1 AND created_at > now() - interval '14 days'`, id.TenantID)
	p := s.page(w, r, "Lead Analytics", "/analytics/leads")
	layouts.AppShell(s.Ren, id, p, analytics.Page(p, d, analyticsTabs("leads"))).Render(r.Context(), w)
}

func (s *Server) handleAnalyticsCampaigns(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	d := &analytics.PageData{Title: "Campaign Analytics", Note: "Delivery and reply performance per campaign."}
	var sent, replies, bounces, unsubs int
	_ = s.PG.QueryRow(r.Context(), `
		SELECT COALESCE(SUM(sent_count),0), COALESCE(SUM(reply_count),0),
			COALESCE(SUM(bounce_count),0), COALESCE(SUM(unsubscribe_count),0)
		FROM campaigns WHERE tenant_id=$1`, id.TenantID).Scan(&sent, &replies, &bounces, &unsubs)
	replyRate := 0.0
	if sent > 0 {
		replyRate = float64(replies) / float64(sent) * 100
	}
	d.Stats = []analytics.Stat{
		{Label: "Sent", Value: itoa(sent)},
		{Label: "Replies", Value: itoa(replies)},
		{Label: "Reply rate", Value: strconv.FormatFloat(replyRate, 'f', 1, 64) + "%"},
		{Label: "Bounces", Value: itoa(bounces)},
	}
	rows, _ := s.PG.Query(r.Context(), `
		SELECT name, sent_count, reply_count FROM campaigns WHERE tenant_id=$1 ORDER BY sent_count DESC LIMIT 10`, id.TenantID)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			var cs, cr int
			if err := rows.Scan(&name, &cs, &cr); err == nil {
				label := name + " (" + itoa(cr) + " replies)"
				d.Bars = append(d.Bars, analytics.Bar{Label: label, Value: itoa(cs) + " sent", Count: cs, Pct: pctOf(cs, maxInt(sent, 1))})
			}
		}
	}
	d.Days = s.daySeries(r, `SELECT to_char(created_at,'YYYY-MM-DD') FROM email_messages WHERE tenant_id=$1 AND status='sent' AND created_at > now() - interval '14 days'`, id.TenantID)
	p := s.page(w, r, "Campaign Analytics", "/analytics/campaigns")
	layouts.AppShell(s.Ren, id, p, analytics.Page(p, d, analyticsTabs("campaigns"))).Render(r.Context(), w)
}

func (s *Server) handleAnalyticsSales(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	d := &analytics.PageData{Title: "Sales Analytics", Note: "Pipeline value, win rate and owner leaderboard."}
	var open, won, lost int
	var openVal, wonVal float64
	_ = s.PG.QueryRow(r.Context(), `
		SELECT COUNT(*) FILTER (WHERE status='open'), COALESCE(SUM(value) FILTER (WHERE status='open'),0),
			COUNT(*) FILTER (WHERE status='won'), COALESCE(SUM(value) FILTER (WHERE status='won'),0),
			COUNT(*) FILTER (WHERE status='lost')
		FROM deals WHERE tenant_id=$1`, id.TenantID).Scan(&open, &openVal, &won, &wonVal, &lost)
	winRate := 0.0
	if won+lost > 0 {
		winRate = float64(won) / float64(won+lost) * 100
	}
	d.Stats = []analytics.Stat{
		{Label: "Open pipeline", Value: compactMoney(openVal)},
		{Label: "Won revenue", Value: compactMoney(wonVal)},
		{Label: "Win rate", Value: strconv.FormatFloat(winRate, 'f', 1, 64) + "%"},
		{Label: "Open deals", Value: itoa(open)},
	}
	rows, _ := s.PG.Query(r.Context(), `
		SELECT COALESCE(ps.name, dl.status), COUNT(*), COALESCE(SUM(dl.value),0)
		FROM deals dl LEFT JOIN pipeline_stages ps ON ps.id = dl.stage_id
		WHERE dl.tenant_id=$1 GROUP BY 1 ORDER BY 3 DESC`, id.TenantID)
	if rows != nil {
		defer rows.Close()
		maxV := 1.0
		type sb struct {
			label string
			n     int
			v     float64
		}
		var tmp []sb
		for rows.Next() {
			var t sb
			if err := rows.Scan(&t.label, &t.n, &t.v); err == nil {
				tmp = append(tmp, t)
				if t.v > maxV {
					maxV = t.v
				}
			}
		}
		for _, t := range tmp {
			d.Bars = append(d.Bars, analytics.Bar{Label: t.label + " (" + itoa(t.n) + ")", Value: compactMoney(t.v), Count: t.n, Pct: t.v / maxV * 100})
		}
	}
	d.Days = s.daySeries(r, `SELECT to_char(created_at,'YYYY-MM-DD') FROM deals WHERE tenant_id=$1 AND status='won' AND created_at > now() - interval '14 days'`, id.TenantID)
	p := s.page(w, r, "Sales Analytics", "/analytics/sales")
	layouts.AppShell(s.Ren, id, p, analytics.Page(p, d, analyticsTabs("sales"))).Render(r.Context(), w)
}

func (s *Server) handleAnalyticsSources(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	d := &analytics.PageData{Title: "Source Analytics", Note: "Which discovery channels produce the best leads."}
	var total int
	_ = s.PG.QueryRow(r.Context(), `SELECT COUNT(*) FROM leads WHERE tenant_id=$1 AND status<>'archived'`, id.TenantID).Scan(&total)
	d.Stats = []analytics.Stat{{Label: "Tracked leads", Value: itoa(total)}}
	var qual int
	_ = s.PG.QueryRow(r.Context(), `SELECT COUNT(*) FROM leads WHERE tenant_id=$1 AND status IN ('qualified','hot','contacted')`, id.TenantID).Scan(&qual)
	d.Stats = append(d.Stats, analytics.Stat{Label: "Qualified", Value: itoa(qual)})
	rows, _ := s.PG.Query(r.Context(), `
		SELECT COALESCE(NULLIF(source,''),'unknown'), COUNT(*), COALESCE(AVG(lead_score),0),
			COUNT(*) FILTER (WHERE status IN ('qualified','hot','contacted'))
		FROM leads WHERE tenant_id=$1 AND status<>'archived' GROUP BY 1 ORDER BY 2 DESC LIMIT 12`, id.TenantID)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var src string
			var n, q int
			var avg float64
			if err := rows.Scan(&src, &n, &avg, &q); err == nil {
				label := src + " · avg " + strconv.FormatFloat(avg, 'f', 0, 64) + " · " + itoa(q) + " qual"
				d.Bars = append(d.Bars, analytics.Bar{Label: label, Value: itoa(n), Count: n, Pct: pctOf(n, maxInt(total, 1))})
			}
		}
	}
	d.Days = s.daySeries(r, `SELECT to_char(created_at,'YYYY-MM-DD') FROM leads WHERE tenant_id=$1 AND created_at > now() - interval '14 days'`, id.TenantID)
	p := s.page(w, r, "Source Analytics", "/analytics/sources")
	layouts.AppShell(s.Ren, id, p, analytics.Page(p, d, analyticsTabs("sources"))).Render(r.Context(), w)
}

// daySeries buckets timestamps into the last 14 days.
func (s *Server) daySeries(r *http.Request, sql, tenantID string) []analytics.DayPoint {
	rows, err := s.PG.Query(r.Context(), sql, tenantID)
	counts := map[string]int{}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var day string
			if err := rows.Scan(&day); err == nil {
				counts[day]++
			}
		}
	}
	var out []analytics.DayPoint
	now := time.Now()
	for i := 13; i >= 0; i-- {
		day := now.AddDate(0, 0, -i)
		k := day.Format("2006-01-02")
		out = append(out, analytics.DayPoint{Label: day.Format("02/01"), Count: counts[k]})
	}
	return out
}

func pctOf(n, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(n) / float64(total) * 100
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
