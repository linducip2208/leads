package web

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"leadforge/internal/flash"
	"leadforge/internal/role"
	"leadforge/internal/source"
	"leadforge/internal/webapp"
	"leadforge/web/layouts"
	page "leadforge/web/pages/imports"
)

func (s *Server) importRoutes() {
	s.Router.HandleFunc("GET", "/imports", s.requirePerm(role.LeadCreate, func(w http.ResponseWriter, r *http.Request) {
		s.handleImportPage(w, r)
	}))
	s.Router.HandleFunc("POST", "/imports", s.requirePerm(role.LeadCreate, s.handleImportUpload))
}

func (s *Server) handleImportPage(w http.ResponseWriter, r *http.Request, errMsg ...string) {
	id := webappIdentity(r)
	rows, err := s.PG.Query(r.Context(), `
		SELECT id::text, COALESCE(NULLIF(query,''), keyword), status, found_count, saved_count, created_at
		FROM lead_searches WHERE tenant_id=$1 AND query LIKE 'Import:%' ORDER BY created_at DESC LIMIT 20`, id.TenantID)
	d := &page.PageData{}
	if len(errMsg) > 0 {
		d.Error = errMsg[0]
	}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var it page.PastImport
			var created time.Time
			if err := rows.Scan(&it.ID, &it.Name, &it.Status, &it.Found, &it.Saved, &created); err == nil {
				it.Created = created.Format("02 Jan 15:04")
				d.Imports = append(d.Imports, it)
			}
		}
	}
	p := s.page(w, r, "Imports", "/imports")
	layouts.AppShell(s.Ren, id, p, page.Page(p, d)).Render(r.Context(), w)
}

func (s *Server) handleImportUpload(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
	if err := r.ParseMultipartForm(12 << 20); err != nil {
		s.handleImportPage(w, r, "File too large (max ~10MB).")
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		s.handleImportPage(w, r, "Select a CSV file first.")
		return
	}
	defer f.Close()
	if ext := strings.ToLower(filepath.Ext(hdr.Filename)); ext != ".csv" {
		s.handleImportPage(w, r, "Only .csv files are supported.")
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, 10<<20+1))
	if err != nil || len(data) == 0 {
		s.handleImportPage(w, r, "Could not read the file.")
		return
	}
	rows, err := source.ParseCSV(data)
	if err != nil || len(rows) == 0 {
		s.handleImportPage(w, r, "No data rows found in the CSV.")
		return
	}
	if len(rows) > 5000 {
		s.handleImportPage(w, r, "Too many rows (max 5,000). Split the file and retry.")
		return
	}

	var searchID string
	err = s.PG.QueryRow(r.Context(), `
		INSERT INTO lead_searches (tenant_id, user_id, keyword, query, country, filters, limit_count, status, started_at)
		VALUES ($1,$2,$3,$4,'', '{}', $5,'running', now()) RETURNING id::text`,
		id.TenantID, id.UserID, hdr.Filename, "Import: "+hdr.Filename, len(rows)).Scan(&searchID)
	if err != nil {
		s.handleImportPage(w, r, "Could not start the import.")
		return
	}
	job, err := s.Runner.Load(r.Context(), searchID)
	if err != nil {
		s.handleImportPage(w, r, "Could not start the import.")
		return
	}
	ch, err := source.NewCSVSource().Search(r.Context(), source.SearchQuery{CSVData: data, CSVName: hdr.Filename, Limit: len(rows)})
	if err != nil {
		_, _ = s.PG.Exec(r.Context(), `UPDATE lead_searches SET status='failed', error='parse error' WHERE id=$1`, searchID)
		s.handleImportPage(w, r, "Could not parse the CSV.")
		return
	}
	eg, ctx := errgroup.WithContext(r.Context())
	eg.SetLimit(4)
	for cand := range ch {
		c := cand
		eg.Go(func() error {
			s.Runner.Process(ctx, job, c)
			return nil
		})
	}
	_ = eg.Wait()
	s.Runner.Finish(r.Context(), job, "completed", "")
	webapp.RedirectFlash(w, r, "/searches/"+searchID, flash.Success, "Import finished — results are in Leads.")
}
