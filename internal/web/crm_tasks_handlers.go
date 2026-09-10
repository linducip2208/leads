package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"leadforge/internal/flash"
	"leadforge/internal/webapp"
	"leadforge/web/layouts"
	"leadforge/web/pages/activities"
	"leadforge/web/pages/tasks"
)

var validTaskKind = map[string]bool{"call": true, "email": true, "whatsapp": true, "meeting": true, "follow_up": true, "general": true}
var validTaskPriority = map[string]bool{"low": true, "medium": true, "high": true, "urgent": true}

func (s *Server) handleTaskList(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	filter := r.URL.Query().Get("filter")
	if filter == "" {
		filter = "open"
	}
	leadID := r.URL.Query().Get("lead")
	q := `SELECT t.id::text, t.title, t.kind, t.priority, t.status,
		COALESCE(to_char(t.due_at,'DD Mon HH24:MI'),''), COALESCE(t.due_at < now() AND t.status='open', false),
		COALESCE(u.name,''), t.lead_id::text, COALESCE(c.name,''), t.deal_id::text, COALESCE(dl.title,'')
		FROM tasks t
		LEFT JOIN users u ON u.id = t.assignee_id
		LEFT JOIN leads l ON l.id = t.lead_id
		LEFT JOIN companies c ON c.id = l.company_id
		LEFT JOIN deals dl ON dl.id = t.deal_id
		WHERE t.tenant_id=$1`
	args := []any{id.TenantID}
	switch filter {
	case "open":
		q += " AND t.status='open'"
	case "completed":
		q += " AND t.status='completed'"
	case "cancelled":
		q += " AND t.status='cancelled'"
	case "mine":
		q += " AND t.assignee_id=$2::uuid"
		args = append(args, id.UserID)
	}
	if leadID != "" {
		q += " AND t.lead_id=$" + strconv.Itoa(len(args)+1) + "::uuid"
		args = append(args, leadID)
	}
	q += " ORDER BY t.status='open' DESC, t.due_at NULLS LAST, t.created_at DESC LIMIT 200"
	rows, err := s.PG.Query(r.Context(), q, args...)
	if err != nil {
		s.renderError(w, r, &webapp.HTTPError{Status: 500, Title: "Something went wrong", Message: "Could not load tasks.", Err: err})
		return
	}
	defer rows.Close()
	d := &tasks.ListData{Filter: filter, LeadID: leadID}
	for rows.Next() {
		var it tasks.Item
		var leadIDv, dealIDv *string
		if err := rows.Scan(&it.ID, &it.Title, &it.Kind, &it.Priority, &it.Status, &it.Due,
			&it.Overdue, &it.Assignee, &leadIDv, &it.LeadName, &dealIDv, &it.DealName); err == nil {
			if leadIDv != nil {
				it.LeadID = *leadIDv
			}
			if dealIDv != nil {
				it.DealID = *dealIDv
			}
			d.Items = append(d.Items, it)
		}
	}
	if leadID != "" {
		_ = s.PG.QueryRow(r.Context(), `SELECT c.name FROM leads l JOIN companies c ON c.id=l.company_id WHERE l.id=$1::uuid AND l.tenant_id=$2`, leadID, id.TenantID).Scan(&d.LeadName)
	}
	p := s.page(w, r, "Tasks", "/tasks")
	layouts.AppShell(s.Ren, id, p, tasks.List(p, d)).Render(r.Context(), w)
}

func (s *Server) handleTaskCreate(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	title := strings.TrimSpace(r.FormValue("title"))
	kind := strings.TrimSpace(r.FormValue("kind"))
	priority := strings.TrimSpace(r.FormValue("priority"))
	if title == "" {
		webapp.RedirectFlash(w, r, "/tasks", flash.Error, "Title is required.")
		return
	}
	if !validTaskKind[kind] {
		kind = "general"
	}
	if !validTaskPriority[priority] {
		priority = "medium"
	}
	var due, leadID, dealID any
	if v := strings.TrimSpace(r.FormValue("due_at")); v != "" {
		if t, err := time.Parse("2006-01-02T15:04", v); err == nil {
			due = t
		}
	}
	if v := strings.TrimSpace(r.FormValue("lead_id")); v != "" {
		leadID = v
	}
	if v := strings.TrimSpace(r.FormValue("deal_id")); v != "" {
		dealID = v
	}
	var taskID string
	err := s.PG.QueryRow(r.Context(), `
		INSERT INTO tasks (tenant_id, kind, title, priority, due_at, lead_id, deal_id, assignee_id, created_by)
		VALUES ($1,$2,$3,$4,$5::timestamptz,$6::uuid,$7::uuid,$8::uuid,$8::uuid) RETURNING id::text`,
		id.TenantID, kind, title, priority, due, leadID, dealID, id.UserID).Scan(&taskID)
	if err != nil {
		webapp.RedirectFlash(w, r, "/tasks", flash.Error, "Could not create the task.")
		return
	}
	if leadID != nil {
		_, _ = s.PG.Exec(r.Context(), `
			INSERT INTO activities (tenant_id, kind, subject, lead_id, user_id)
			VALUES ($1,'task.created',$2,$3::uuid,$4)`, id.TenantID, "Task: "+title, leadID, id.UserID)
	}
	back := "/tasks"
	if leadID != nil {
		back = "/tasks?lead=" + leadID.(string)
	}
	webapp.RedirectFlash(w, r, back, flash.Success, "Task created.")
}

func (s *Server) handleTaskToggle(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	tid := r.PathValue("id")
	_, _ = s.PG.Exec(r.Context(), `
		UPDATE tasks SET status = CASE WHEN status='completed' THEN 'open' ELSE 'completed' END,
			completed_at = CASE WHEN status='completed' THEN NULL ELSE now() END,
			due_at = due_at WHERE id=$1::uuid AND tenant_id=$2`, tid, id.TenantID)
	back := r.Referer()
	if back == "" {
		back = "/tasks"
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (s *Server) handleTaskDelete(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	_, _ = s.PG.Exec(r.Context(), `DELETE FROM tasks WHERE id=$1::uuid AND tenant_id=$2`, r.PathValue("id"), id.TenantID)
	back := r.Referer()
	if back == "" {
		back = "/tasks"
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (s *Server) handleActivityFeed(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	kind := r.URL.Query().Get("kind")
	q := `SELECT a.kind, COALESCE(NULLIF(a.subject,''), a.kind), COALESCE(u.name,'System'),
		to_char(a.created_at,'DD Mon HH24:MI'), a.lead_id::text
		FROM activities a LEFT JOIN users u ON u.id = a.user_id
		WHERE a.tenant_id=$1`
	args := []any{id.TenantID}
	if kind != "" {
		q += " AND a.kind=$2"
		args = append(args, kind)
	}
	q += " ORDER BY a.created_at DESC LIMIT 200"
	rows, err := s.PG.Query(r.Context(), q, args...)
	if err != nil {
		s.renderError(w, r, &webapp.HTTPError{Status: 500, Title: "Something went wrong", Message: "Could not load activities.", Err: err})
		return
	}
	defer rows.Close()
	d := &activities.FeedData{Kind: kind, Kinds: []string{
		"lead.created", "lead.enriched", "lead.qualified", "lead.updated",
		"email.sent", "reply.received", "task.created",
		"deal.created", "deal.stage_changed", "deal.won",
	}}
	for rows.Next() {
		var a activities.Item
		var leadID *string
		if err := rows.Scan(&a.Kind, &a.Subject, &a.Who, &a.When, &leadID); err == nil {
			if leadID != nil {
				a.LeadID = *leadID
			}
			d.Items = append(d.Items, a)
		}
	}
	p := s.page(w, r, "Activities", "/activities")
	layouts.AppShell(s.Ren, id, p, activities.Feed(p, d)).Render(r.Context(), w)
}
