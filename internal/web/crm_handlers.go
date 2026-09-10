package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"leadforge/internal/role"
	"leadforge/web/layouts"
	"leadforge/web/pages/pipeline"
)

func (s *Server) crmRoutes() {
	s.Router.HandleFunc("GET", "/pipeline", s.requirePerm(role.DealManage, s.handlePipeline))
	s.Router.HandleFunc("POST", "/pipeline/move", s.requirePerm(role.DealManage, s.handlePipelineMove))
	s.Router.HandleFunc("GET", "/deals", s.requirePerm(role.DealManage, s.handleDealList))
	s.Router.HandleFunc("GET", "/deals/new", s.requirePerm(role.DealManage, s.handleDealNew))
	s.Router.HandleFunc("POST", "/deals", s.requirePerm(role.DealManage, s.handleDealCreate))
	s.Router.HandleFunc("GET", "/deals/{id}", s.requirePerm(role.DealManage, s.handleDealDetail))
	s.Router.HandleFunc("POST", "/deals/{id}", s.requirePerm(role.DealManage, s.handleDealUpdate))
	s.Router.HandleFunc("POST", "/deals/{id}/stage", s.requirePerm(role.DealManage, s.handleDealStage))
	s.Router.HandleFunc("GET", "/tasks", s.requirePerm(role.TaskManage, s.handleTaskList))
	s.Router.HandleFunc("POST", "/tasks", s.requirePerm(role.TaskManage, s.handleTaskCreate))
	s.Router.HandleFunc("POST", "/tasks/{id}/toggle", s.requirePerm(role.TaskManage, s.handleTaskToggle))
	s.Router.HandleFunc("POST", "/tasks/{id}/delete", s.requirePerm(role.TaskManage, s.handleTaskDelete))
	s.Router.HandleFunc("GET", "/activities", s.requirePerm(role.LeadRead, s.handleActivityFeed))
}

var defaultStages = []struct {
	name string
	kind string
	prob int
}{
	{"New", "open", 10},
	{"Qualified", "open", 20},
	{"Contacted", "open", 30},
	{"Replied", "open", 45},
	{"Interested", "open", 55},
	{"Meeting", "open", 65},
	{"Proposal", "open", 75},
	{"Negotiation", "open", 85},
	{"Won", "won", 100},
	{"Lost", "lost", 0},
}

// ensurePipelineCtx returns a pipeline id, creating the default with stages.
func (s *Server) ensurePipelineCtx(ctx context.Context, tenantID string) string {
	var pid string
	_ = s.PG.QueryRow(ctx, `SELECT id::text FROM pipelines WHERE tenant_id=$1 ORDER BY created_at LIMIT 1`, tenantID).Scan(&pid)
	if pid != "" {
		return pid
	}
	tx, err := s.PG.Begin(ctx)
	if err != nil {
		return ""
	}
	defer tx.Rollback(ctx)
	if err := tx.QueryRow(ctx, `INSERT INTO pipelines (tenant_id, name, is_default) VALUES ($1,'Sales Pipeline',true) RETURNING id::text`, tenantID).Scan(&pid); err != nil {
		return ""
	}
	for i, st := range defaultStages {
		_, _ = tx.Exec(ctx, `INSERT INTO pipeline_stages (pipeline_id, name, position, kind, probability) VALUES ($1,$2,$3,$4,$5)`,
			pid, st.name, i, st.kind, st.prob)
	}
	_ = tx.Commit(ctx)
	return pid
}

// stageTotal sums open deal values in a stage.
func stageTotal(r *http.Request, pool *pgxpool.Pool, tenantID, stageID string) string {
	var total float64
	_ = pool.QueryRow(r.Context(), `SELECT COALESCE(SUM(value),0) FROM deals WHERE tenant_id=$1 AND stage_id=$2::uuid AND status='open'`, tenantID, stageID).Scan(&total)
	return compactMoney(total)
}

func compactMoney(v float64) string {
	switch {
	case v >= 1e9:
		return strconv.FormatFloat(v/1e9, 'f', 1, 64) + "B"
	case v >= 1e6:
		return strconv.FormatFloat(v/1e6, 'f', 1, 64) + "M"
	case v >= 1e3:
		return strconv.FormatFloat(v/1e3, 'f', 1, 64) + "K"
	default:
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
}

func (s *Server) handlePipeline(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	pid := s.ensurePipelineCtx(r.Context(), id.TenantID)
	if q := r.URL.Query().Get("pipeline"); q != "" {
		var exists bool
		_ = s.PG.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM pipelines WHERE id=$1::uuid AND tenant_id=$2)`, q, id.TenantID).Scan(&exists)
		if exists {
			pid = q
		}
	}
	var pname string
	_ = s.PG.QueryRow(r.Context(), `SELECT name FROM pipelines WHERE id=$1::uuid`, pid).Scan(&pname)
	d := &pipeline.BoardData{PipelineID: pid, PipelineName: pname}
	prows, _ := s.PG.Query(r.Context(), `SELECT id::text, name FROM pipelines WHERE tenant_id=$1 ORDER BY created_at`, id.TenantID)
	if prows != nil {
		defer prows.Close()
		for prows.Next() {
			var o pipeline.Option
			if err := prows.Scan(&o.Value, &o.Label); err == nil {
				d.Pipelines = append(d.Pipelines, o)
			}
		}
	}
	srows, _ := s.PG.Query(r.Context(), `SELECT id::text, name, kind FROM pipeline_stages WHERE pipeline_id=$1::uuid ORDER BY position`, pid)
	if srows != nil {
		defer srows.Close()
		for srows.Next() {
			var st pipeline.StageCol
			if err := srows.Scan(&st.ID, &st.Name, &st.Kind); err != nil {
				continue
			}
			drows, _ := s.PG.Query(r.Context(), `
				SELECT dl.id::text, dl.title, COALESCE(c.name,''), dl.value::text || ' ' || dl.currency, COALESCE(u.name,'')
				FROM deals dl LEFT JOIN companies c ON c.id = dl.company_id LEFT JOIN users u ON u.id = dl.owner_id
				WHERE dl.tenant_id=$1 AND dl.stage_id=$2::uuid ORDER BY dl.updated_at DESC LIMIT 100`, id.TenantID, st.ID)
			if drows != nil {
				for drows.Next() {
					var c pipeline.DealCard
					if err := drows.Scan(&c.ID, &c.Title, &c.Company, &c.Value, &c.Owner); err == nil {
						st.Deals = append(st.Deals, c)
					}
				}
				drows.Close()
			}
			st.Count = len(st.Deals)
			st.Total = stageTotal(r, s.PG, id.TenantID, st.ID)
			d.Stages = append(d.Stages, st)
		}
	}
	p := s.page(w, r, "Pipeline", "/pipeline")
	layouts.AppShell(s.Ren, id, p, pipeline.Board(p, d)).Render(r.Context(), w)
}

func (s *Server) handlePipelineMove(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	dealID := strings.TrimSpace(r.FormValue("deal_id"))
	stageID := strings.TrimSpace(r.FormValue("stage_id"))
	var kind, sname string
	err := s.PG.QueryRow(r.Context(), `
		SELECT ps.kind, ps.name FROM pipeline_stages ps
		JOIN pipelines p ON p.id = ps.pipeline_id
		WHERE ps.id=$1::uuid AND p.tenant_id=$2`, stageID, id.TenantID).Scan(&kind, &sname)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	status := "open"
	var closed any
	if kind == "won" || kind == "lost" {
		status = kind
		closed = time.Now()
	}
	res, err := s.PG.Exec(r.Context(), `
		UPDATE deals SET stage_id=$3::uuid, status=$4, closed_at=$5::timestamptz, updated_at=now()
		WHERE id=$1::uuid AND tenant_id=$2`, dealID, id.TenantID, stageID, status, closed)
	if err != nil || res.RowsAffected() == 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	_, _ = s.PG.Exec(r.Context(), `
		INSERT INTO deal_activities (tenant_id, deal_id, kind, body, user_id)
		VALUES ($1,$2::uuid,'stage_changed',$3,$4)`, id.TenantID, dealID, "Moved to "+sname, id.UserID)
	w.Header().Set("HX-Trigger", "kanbanMoved")
	w.WriteHeader(http.StatusOK)
}
