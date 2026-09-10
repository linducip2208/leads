package web

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"leadforge/internal/role"
)

// exportRoutes registers the streaming leads export.
func (s *Server) exportRoutes() {
	s.Router.HandleFunc("GET", "/leads/export", s.requirePerm(role.LeadExport, s.handleLeadExport))
}

const exportCap = 50000
const exportPage = 2000

// handleLeadExport streams the filtered leads as CSV in keyset pages
// (bounded memory) with formula-injection sanitization.
func (s *Server) handleLeadExport(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	f := parseLeadFilter(r)
	where, fargs := leadWhere(f, false)
	base := `SELECT c.name, COALESCE(ct.full_name,''), COALESCE(ct.job_title,''),
		COALESCE(c.industry,''), COALESCE(c.city,''), COALESCE(c.province,''), COALESCE(c.country,''),
		COALESCE(c.website,''), COALESCE(ct.email,''), COALESCE(NULLIF(ct.phone,''), c.phone,''),
		COALESCE(c.whatsapp,''), l.lead_score, l.status, l.source, COALESCE(u.name,''),
		to_char(l.created_at,'YYYY-MM-DD HH24:MI'), l.created_at, l.id::text
		FROM leads l
		JOIN companies c ON c.id = l.company_id
		LEFT JOIN contacts ct ON ct.id = l.primary_contact_id
		LEFT JOIN users u ON u.id = l.owner_id
		WHERE ` + where + ` ORDER BY l.created_at DESC, l.id DESC`

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="leads-`+time.Now().Format("20060102-150405")+`.csv"`)
	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write([]string{"company", "contact", "job_title", "industry", "city", "province", "country",
		"website", "email", "phone", "whatsapp", "score", "status", "source", "owner", "created"})

	var cursorTS, cursorID string
	n := 0
	for {
		q := base
		args := append([]any{id.TenantID}, fargs...)
		if cursorTS != "" {
			ts, lid, ok := splitCursor(cursorTS + "|" + cursorID)
			if !ok {
				break
			}
			args = append(args, ts, lid)
			m := len(args)
			q += " AND (l.created_at < $" + strconv.Itoa(m-1) + " OR (l.created_at = $" + strconv.Itoa(m-1) + " AND l.id < $" + strconv.Itoa(m) + "::uuid))"
		}
		q += " LIMIT " + strconv.Itoa(exportPage)
		rows, err := s.PG.Query(r.Context(), q, args...)
		if err != nil {
			break
		}
		batch := 0
		for rows.Next() {
			var vals [16]string
			var ts time.Time
			var lid string
			ptrs := make([]any, 16)
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			ptrs = append(ptrs, &ts, &lid)
			if err := rows.Scan(ptrs...); err != nil {
				continue
			}
			rec := make([]string, 0, 16)
			for i := 0; i < 16; i++ {
				rec = append(rec, csvSafe(vals[i]))
			}
			_ = cw.Write(rec)
			batch++
			n++
			cursorTS, cursorID = ts.UTC().Format(time.RFC3339), lid
			if n >= exportCap {
				break
			}
		}
		rows.Close()
		cw.Flush()
		if r.Context().Err() != nil {
			break
		}
		if batch < exportPage || n >= exportCap {
			break
		}
	}
}

// csvSafe neutralizes spreadsheet formula injection (= + - @ and friends).
func csvSafe(v string) string {
	if v == "" {
		return v
	}
	switch v[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + v
	}
	// also catch = after whitespace trimming by callers (values are raw here)
	t := strings.TrimLeft(v, " \t")
	if t != "" && (t[0] == '=' || t[0] == '+' || t[0] == '-' || t[0] == '@') {
		return "'" + v
	}
	return v
}
