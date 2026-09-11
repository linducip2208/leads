package web

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"leadforge/internal/audit"
	"leadforge/internal/httpx"
	"leadforge/internal/search"
	"leadforge/internal/webapp"
)

var apiLimiter = httpx.NewRateLimiter(600, time.Minute)

// apiKey holds a verified API key identity.
type apiKey struct {
	ID       string
	TenantID string
	Scopes   []string
}

func (k *apiKey) can(scope string) bool {
	for _, s := range k.Scopes {
		if s == scope || s == "admin" {
			return true
		}
	}
	return false
}

func hashAPIKey(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func randomAPIKey() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return "lf_" + hex.EncodeToString(b[:])
}

// apiAuth authenticates Bearer keys with an optional required scope.
func (s *Server) apiAuth(scope string, next func(w http.ResponseWriter, r *http.Request, k *apiKey)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		raw = strings.TrimSpace(raw)
		if raw == "" {
			writeAPIError(w, 401, "missing bearer token")
			return
		}
		if !apiLimiter.Allow(hashAPIKey(raw)) {
			writeAPIError(w, 429, "rate limit exceeded")
			return
		}
		var k apiKey
		var scopes []string
		var revokedAt, expiresAt *time.Time
		err := s.PG.QueryRow(r.Context(), `
			SELECT id::text, tenant_id::text, scopes, revoked_at, expires_at
			FROM api_keys WHERE key_hash=$1`, hashAPIKey(raw)).
			Scan(&k.ID, &k.TenantID, &scopes, &revokedAt, &expiresAt)
		if err != nil {
			writeAPIError(w, 401, "invalid api key")
			return
		}
		if revokedAt != nil || (expiresAt != nil && expiresAt.Before(time.Now())) {
			writeAPIError(w, 401, "api key revoked or expired")
			return
		}
		k.Scopes = scopes
		if scope != "" && !k.can(scope) {
			writeAPIError(w, 403, "insufficient scope")
			return
		}
		_, _ = s.PG.Exec(r.Context(), `UPDATE api_keys SET last_used_at=now() WHERE id=$1`, k.ID)
		next(w, r, &k)
	}
}

func writeAPIError(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	code := "http_" + strconv.Itoa(status)
	if statusText := http.StatusText(status); statusText != "" {
		code = strings.ToLower(strings.ReplaceAll(statusText, " ", "_"))
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
		"code": code, "message": msg, "request_id": w.Header().Get("X-Request-Id"),
	}})
}

func writeAPIData(w http.ResponseWriter, data any, meta any) {
	out := map[string]any{"data": data}
	if meta != nil {
		out["meta"] = meta
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) apiRoutes() {
	s.Router.HandleFunc("GET", "/api/v1/leads", s.apiAuth("read", s.apiLeadList))
	s.Router.HandleFunc("GET", "/api/v1/leads/{id}", s.apiAuth("read", s.apiLeadDetail))
	s.Router.HandleFunc("GET", "/api/v1/companies", s.apiAuth("read", s.apiCompanyList))
	s.Router.HandleFunc("GET", "/api/v1/companies/{id}", s.apiAuth("read", s.apiCompanyDetail))
	s.Router.HandleFunc("GET", "/api/v1/searches", s.apiAuth("read", s.apiSearchList))
	s.Router.HandleFunc("POST", "/api/v1/searches", s.apiAuth("write", s.apiSearchCreate))
	s.Router.HandleFunc("GET", "/api/v1/searches/{id}", s.apiAuth("read", s.apiSearchDetail))
}

func (s *Server) apiLeadList(w http.ResponseWriter, r *http.Request, k *apiKey) {
	f := parseLeadFilter(r)
	_ = f
	where, fargs := leadWhere(parseLeadFilter(r), false)
	args := append([]any{k.TenantID}, fargs...)
	q := `SELECT l.id::text, c.name, COALESCE(ct.email,''), l.lead_score, l.status, l.source,
		to_char(l.created_at,'YYYY-MM-DD"T"HH24:MI:SS'), l.created_at
		FROM leads l JOIN companies c ON c.id=l.company_id
		LEFT JOIN contacts ct ON ct.id=l.primary_contact_id
		WHERE ` + where
	if cur := r.URL.Query().Get("cursor"); cur != "" {
		if ts, lid, ok := splitCursor(cur); ok {
			args = append(args, ts, lid)
			m := len(args)
			q += " AND (l.created_at < $" + strconv.Itoa(m-1) + " OR (l.created_at = $" + strconv.Itoa(m-1) + " AND l.id < $" + strconv.Itoa(m) + "::uuid))"
		}
	}
	limit := apiPageLimit(r)
	q += " ORDER BY l.created_at DESC, l.id DESC LIMIT " + strconv.Itoa(limit+1)
	rows, err := s.PG.Query(r.Context(), q, args...)
	if err != nil {
		writeAPIError(w, 500, "query failed")
		return
	}
	defer rows.Close()
	type item struct {
		ID      string `json:"id"`
		Company string `json:"company"`
		Email   string `json:"email,omitempty"`
		Score   int    `json:"score"`
		Status  string `json:"status"`
		Source  string `json:"source"`
		Created string `json:"created_at"`
	}
	var items []item
	var nextCursor string
	for rows.Next() {
		var it item
		var ts time.Time
		if err := rows.Scan(&it.ID, &it.Company, &it.Email, &it.Score, &it.Status, &it.Source, &it.Created, &ts); err == nil {
			items = append(items, it)
			nextCursor = ts.UTC().Format(time.RFC3339) + "|" + it.ID
		}
	}
	meta := map[string]any{"has_more": false}
	if len(items) > limit {
		items = items[:limit]
		// recompute cursor from the new tail
		meta["has_more"] = true
		meta["cursor"] = nextCursor
	}
	writeAPIData(w, items, meta)
}

func (s *Server) apiLeadDetail(w http.ResponseWriter, r *http.Request, k *apiKey) {
	lid := r.PathValue("id")
	ctx := webapp.WithIdentity(r.Context(), &webapp.Identity{TenantID: k.TenantID})
	d, ok := s.loadLeadDetail(r.WithContext(ctx), lid)
	if !ok {
		writeAPIError(w, 404, "not found")
		return
	}
	writeAPIData(w, d, nil)
}

func (s *Server) apiCompanyList(w http.ResponseWriter, r *http.Request, k *apiKey) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := apiPageLimit(r)
	rows, err := s.PG.Query(r.Context(), `
		SELECT id::text, name, COALESCE(domain,''), COALESCE(city,''), lead_score
		FROM companies WHERE tenant_id=$1 AND ($2='' OR name ILIKE '%'||$2||'%' OR domain ILIKE '%'||$2||'%')
		ORDER BY created_at DESC LIMIT `+strconv.Itoa(limit), k.TenantID, q)
	if err != nil {
		writeAPIError(w, 500, "query failed")
		return
	}
	defer rows.Close()
	type item struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Domain string `json:"domain,omitempty"`
		City   string `json:"city,omitempty"`
		Score  int    `json:"score"`
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ID, &it.Name, &it.Domain, &it.City, &it.Score); err == nil {
			items = append(items, it)
		}
	}
	writeAPIData(w, items, nil)
}

func apiPageLimit(r *http.Request) int {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		return 100
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func (s *Server) apiCompanyDetail(w http.ResponseWriter, r *http.Request, k *apiKey) {
	var out map[string]any
	rows, err := s.PG.Query(r.Context(), `
		SELECT 'id', id::text FROM companies WHERE id=$1 AND tenant_id=$2
		UNION ALL SELECT 'name', name FROM companies WHERE id=$1 AND tenant_id=$2
		UNION ALL SELECT 'domain', domain FROM companies WHERE id=$1 AND tenant_id=$2
		UNION ALL SELECT 'website', website FROM companies WHERE id=$1 AND tenant_id=$2
		UNION ALL SELECT 'industry', industry FROM companies WHERE id=$1 AND tenant_id=$2
		UNION ALL SELECT 'city', city FROM companies WHERE id=$1 AND tenant_id=$2`,
		r.PathValue("id"), k.TenantID)
	if err != nil {
		writeAPIError(w, 500, "query failed")
		return
	}
	defer rows.Close()
	out = map[string]any{}
	for rows.Next() {
		var kk, vv string
		if err := rows.Scan(&kk, &vv); err == nil {
			out[kk] = vv
		}
	}
	if len(out) == 0 {
		writeAPIError(w, 404, "not found")
		return
	}
	writeAPIData(w, out, nil)
}

func (s *Server) apiSearchList(w http.ResponseWriter, r *http.Request, k *apiKey) {
	rows, err := s.PG.Query(r.Context(), `
		SELECT id::text, COALESCE(NULLIF(keyword,''),query), status, found_count, saved_count, qualified_count,
			to_char(created_at,'YYYY-MM-DD"T"HH24:MI:SS')
		FROM lead_searches WHERE tenant_id=$1 ORDER BY created_at DESC LIMIT 100`, k.TenantID)
	if err != nil {
		writeAPIError(w, 500, "query failed")
		return
	}
	defer rows.Close()
	type item struct {
		ID        string `json:"id"`
		Query     string `json:"query"`
		Status    string `json:"status"`
		Found     int    `json:"found"`
		Saved     int    `json:"saved"`
		Qualified int    `json:"qualified"`
		Created   string `json:"created_at"`
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ID, &it.Query, &it.Status, &it.Found, &it.Saved, &it.Qualified, &it.Created); err == nil {
			items = append(items, it)
		}
	}
	writeAPIData(w, items, nil)
}

func (s *Server) apiSearchCreate(w http.ResponseWriter, r *http.Request, k *apiKey) {
	var in struct {
		Keyword  string   `json:"keyword"`
		Industry string   `json:"industry"`
		City     string   `json:"city"`
		Country  string   `json:"country"`
		Limit    int      `json:"limit"`
		Sources  []string `json:"sources"`
		SeedURLs []string `json:"seed_urls"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeAPIError(w, 400, "invalid json")
		return
	}
	in.Keyword = strings.TrimSpace(in.Keyword)
	if in.Keyword == "" && len(in.SeedURLs) == 0 {
		writeAPIError(w, 400, "keyword or seed_urls required")
		return
	}
	if in.Limit <= 0 {
		in.Limit = 100
	}
	if in.Limit > 50000 {
		in.Limit = 50000
	}
	filters, _ := json.Marshal(search.Filters{
		Country: in.Country, City: in.City, Sources: in.Sources, SeedURLs: in.SeedURLs,
	})
	var searchID string
	err := s.PG.QueryRow(r.Context(), `
		INSERT INTO lead_searches (tenant_id, user_id, keyword, query, industry, location, country, filters, limit_count, status)
		VALUES ($1,NULL,$2,$2,$3,$4,$5,$6,$7,'queued') RETURNING id::text`,
		k.TenantID, in.Keyword, in.Industry, in.City, in.Country, string(filters), in.Limit).Scan(&searchID)
	if err != nil {
		writeAPIError(w, 500, "could not create search")
		return
	}
	if s.Queue == nil {
		_, _ = s.PG.Exec(r.Context(), `UPDATE lead_searches SET status='failed', error='queue unavailable', updated_at=now() WHERE id=$1`, searchID)
		writeAPIError(w, http.StatusServiceUnavailable, "queue unavailable")
		return
	}
	if err := s.Queue.EnqueueSearch(r.Context(), searchID); err != nil {
		_, _ = s.PG.Exec(r.Context(), `UPDATE lead_searches SET status='failed', error='queue unavailable', updated_at=now() WHERE id=$1`, searchID)
		writeAPIError(w, http.StatusServiceUnavailable, "queue unavailable")
		return
	}
	audit.Log(r.Context(), s.PG, k.TenantID, "", "search.create", "search", searchID, audit.IP(r))
	w.WriteHeader(201)
	writeAPIData(w, map[string]any{"id": searchID, "status": "queued"}, nil)
}

func (s *Server) apiSearchDetail(w http.ResponseWriter, r *http.Request, k *apiKey) {
	prog, err := search.Snapshot(r.Context(), s.PG, k.TenantID, r.PathValue("id"))
	if err != nil {
		if err == pgx.ErrNoRows {
			writeAPIError(w, 404, "not found")
			return
		}
		writeAPIError(w, 500, "query failed")
		return
	}
	writeAPIData(w, prog, nil)
}
