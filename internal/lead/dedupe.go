package lead

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Match describes a possible duplicate company.
type Match struct {
	CompanyID  string
	Kind       string // domain | phone | email | external | name_address | name
	Confidence int    // 0-100
	AutoLink   bool   // true when confident enough to link automatically
}

// autoThreshold links automatically at/above this confidence.
const autoThreshold = 60

// FindDuplicate searches the tenant's companies (and their contacts) for a
// record matching the normalized candidate. High-confidence matches may be
// auto-linked; low-confidence ones are returned for flagging. Never merges.
func FindDuplicate(ctx context.Context, pool *pgxpool.Pool, tenantID string, n Normalized, externalID, sourceSlug string) (*Match, error) {
	if tenantID == "" {
		return nil, nil
	}
	best := &Match{}
	consider := func(m *Match) {
		if m != nil && m.Confidence > best.Confidence {
			best = m
		}
	}

	// 1. exact domain — strongest company signal
	if n.Domain != "" {
		var id string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM companies WHERE tenant_id=$1 AND domain=$2 LIMIT 1`,
			tenantID, n.Domain).Scan(&id); err == nil {
			consider(&Match{CompanyID: id, Kind: "domain", Confidence: 95})
		}
	}
	// 2. external source id
	if externalID != "" && sourceSlug != "" {
		var id string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM companies WHERE tenant_id=$1 AND source=$2 AND external_id=$3 LIMIT 1`,
			tenantID, sourceSlug, externalID).Scan(&id); err == nil {
			consider(&Match{CompanyID: id, Kind: "external", Confidence: 95})
		}
	}
	// 3. phone (canonical or display)
	if n.PhoneE164 != "" || n.Phone != "" {
		var id string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM companies WHERE tenant_id=$1 AND (phone=$2 OR whatsapp=$2 OR phone=$3 OR whatsapp=$3) AND (phone<>'' OR whatsapp<>'') LIMIT 1`,
			tenantID, n.PhoneE164, n.Phone).Scan(&id); err == nil {
			consider(&Match{CompanyID: id, Kind: "phone", Confidence: 90})
		}
	}
	// 4. email via contacts
	if n.Email != "" {
		var id string
		if err := pool.QueryRow(ctx, `SELECT company_id::text FROM contacts WHERE tenant_id=$1 AND email=$2 AND company_id IS NOT NULL LIMIT 1`,
			tenantID, n.Email).Scan(&id); err == nil {
			consider(&Match{CompanyID: id, Kind: "email", Confidence: 90})
		}
	}
	// 5. normalized name (+address/city when available)
	if n.Name != "" {
		lname := strings.ToLower(n.Name)
		if n.City != "" {
			var id string
			if err := pool.QueryRow(ctx, `SELECT id::text FROM companies WHERE tenant_id=$1 AND lower(name)=$2 AND city ILIKE $3 LIMIT 1`,
				tenantID, lname, n.City).Scan(&id); err == nil {
				consider(&Match{CompanyID: id, Kind: "name_address", Confidence: 75})
			}
		} else {
			var id string
			if err := pool.QueryRow(ctx, `SELECT id::text FROM companies WHERE tenant_id=$1 AND lower(name)=$2 LIMIT 1`,
				tenantID, lname).Scan(&id); err == nil {
				consider(&Match{CompanyID: id, Kind: "name", Confidence: 55})
			}
		}
	}

	if best.CompanyID == "" {
		return nil, nil
	}
	best.AutoLink = best.Confidence >= autoThreshold
	return best, nil
}

// DuplicateScore is a small helper for UI display of raw duplicate flags.
func DuplicateScore(confidence int) string {
	switch {
	case confidence >= 90:
		return "Very likely duplicate"
	case confidence >= 70:
		return "Likely duplicate"
	case confidence >= 50:
		return "Possible duplicate"
	default:
		return "Weak match"
	}
}
