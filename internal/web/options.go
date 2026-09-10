package web

import (
	"net/http"
	"time"
)

// KV is a generic value/label pair for select options.
type KV struct {
	Value string
	Label string
}

// ownerKVs lists tenant users for owner selects.
func (s *Server) ownerKVs(r *http.Request) []KV {
	rows, err := s.PG.Query(r.Context(), `SELECT id::text, name FROM users WHERE tenant_id=$1 AND status='active' ORDER BY name`, webappIdentity(r).TenantID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []KV
	for rows.Next() {
		var kv KV
		if err := rows.Scan(&kv.Value, &kv.Label); err == nil {
			out = append(out, kv)
		}
	}
	return out
}

// distinctKVs runs a single-column distinct query for filter options.
func (s *Server) distinctKVs(r *http.Request, q string) []KV {
	rows, err := s.PG.Query(r.Context(), q, webappIdentity(r).TenantID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []KV
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err == nil && v != "" {
			out = append(out, KV{Value: v, Label: v})
		}
	}
	return out
}

// listKVs lists tenant lists.
func (s *Server) listKVs(r *http.Request) []KV {
	return s.pairKVs(r, `SELECT id::text, name FROM lists WHERE tenant_id=$1 ORDER BY name`)
}

// pairKVs runs a two-column value/label query.
func (s *Server) pairKVs(r *http.Request, q string) []KV {
	rows, err := s.PG.Query(r.Context(), q, webappIdentity(r).TenantID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []KV
	for rows.Next() {
		var kv KV
		if err := rows.Scan(&kv.Value, &kv.Label); err == nil {
			out = append(out, kv)
		}
	}
	return out
}

// splitCursor parses "RFC3339|uuid" keyset cursors.
func splitCursor(cur string) (time.Time, string, bool) {
	for i := 0; i < len(cur); i++ {
		if cur[i] == '|' {
			ts, err := time.Parse(time.RFC3339, cur[:i])
			if err != nil || cur[i+1:] == "" {
				return time.Time{}, "", false
			}
			return ts, cur[i+1:], true
		}
	}
	return time.Time{}, "", false
}
