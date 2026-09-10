// Package audit records best-effort audit trails. Failures never fail requests.
package audit

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Log writes one audit row; errors are swallowed by design.
func Log(ctx context.Context, pool *pgxpool.Pool, tenantID, userID, action, entity, entityID, ip string) {
	if pool == nil {
		return
	}
	_, _ = pool.Exec(ctx, `
		INSERT INTO audit_logs (tenant_id, user_id, action, entity_type, entity_id, ip)
		VALUES (NULLIF($1,'')::uuid, NULLIF($2,'')::uuid, $3, $4, $5, $6)`,
		tenantID, userID, action, entity, entityID, ip)
}

// IP extracts the client IP for audit purposes.
func IP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return fwd
	}
	return r.RemoteAddr
}
