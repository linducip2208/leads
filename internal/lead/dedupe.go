package lead

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Match describes a possible duplicate company.
type Match struct {
	CompanyID  string
	Kind       string // domain | phone | email | external | name_address | name | fuzzy
	Confidence int    // 0-100
	AutoLink   bool   // true when confident enough to link automatically
}

// autoThreshold links automatically at/above this confidence.
const autoThreshold = 60

// FindDuplicate searches the tenant's companies (and their contacts) with
// layered signals: exact canonical domain (100), external id + source (100),
// phone (90), email-domain + name (90), name + city (80), fuzzy/same name
// (55). High-confidence matches auto-link; the rest are flagged. Never merges.
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
	canon := CanonicalDomain(n.Domain)

	// 1. exact canonical domain — strongest company signal
	if canon != "" {
		var id string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM companies
			WHERE tenant_id=$1 AND (domain=$2 OR canonical_domain=$2) LIMIT 1`,
			tenantID, canon).Scan(&id); err == nil {
			consider(&Match{CompanyID: id, Kind: "domain", Confidence: 100})
		}
	}
	// 2. external source id
	if externalID != "" && sourceSlug != "" {
		var id string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM companies WHERE tenant_id=$1 AND source=$2 AND external_id=$3 LIMIT 1`,
			tenantID, sourceSlug, externalID).Scan(&id); err == nil {
			consider(&Match{CompanyID: id, Kind: "external", Confidence: 100})
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
	// 4. same email domain + similar name
	if n.Email != "" {
		var id, cname string
		if err := pool.QueryRow(ctx, `SELECT c.id::text, c.name FROM contacts ct
			JOIN companies c ON c.id = ct.company_id
			WHERE ct.tenant_id=$1 AND ct.email=$2 AND ct.company_id IS NOT NULL LIMIT 1`,
			tenantID, n.Email).Scan(&id, &cname); err == nil {
			conf := 90
			if n.Name != "" && NameSimilarity(n.Name, cname) < 0.5 {
				conf = 70 // same email, different name: likely shared inbox
			}
			consider(&Match{CompanyID: id, Kind: "email", Confidence: conf})
		}
	}
	// 5-6. name signals (fuzzy, legal-entity aware). With pg_trgm we rank by
	// similarity server-side; otherwise a bounded LIKE prefilter applies.
	if n.Name != "" {
		for _, c := range nameCandidates(ctx, pool, tenantID, n.Name) {
			sim := NameSimilarity(n.Name, c.name)
			switch {
			case sim >= 0.99:
				if n.City != "" && strings.EqualFold(c.city, n.City) {
					consider(&Match{CompanyID: c.id, Kind: "name_address", Confidence: 80})
				} else {
					consider(&Match{CompanyID: c.id, Kind: "name", Confidence: 55})
				}
			case sim >= 0.8:
				if n.City != "" && strings.EqualFold(c.city, n.City) {
					consider(&Match{CompanyID: c.id, Kind: "fuzzy", Confidence: 75})
				} else {
					consider(&Match{CompanyID: c.id, Kind: "fuzzy", Confidence: 55})
				}
			case sim >= 0.6 && n.City != "" && strings.EqualFold(c.city, n.City):
				consider(&Match{CompanyID: c.id, Kind: "fuzzy", Confidence: 50})
			}
		}
	}

	if best.CompanyID == "" {
		return nil, nil
	}
	best.AutoLink = best.Confidence >= autoThreshold
	return best, nil
}

type nameCand struct {
	id   string
	name string
	city string
}

// trigramOK caches pg_trgm availability (10 min TTL).
var trigramCache atomic.Int64 // unix nano of expiry, >0 = available

func trgmAvailable(ctx context.Context, pool *pgxpool.Pool) bool {
	if time.Now().UnixNano() < trigramCache.Load() {
		return trigramCache.Load() > 1
	}
	var ok bool
	_ = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname='pg_trgm')`).Scan(&ok)
	if ok {
		trigramCache.Store(time.Now().Add(10 * time.Minute).UnixNano())
	} else {
		trigramCache.Store(1) // negative cache marker (past time, false)
	}
	return ok
}

// nameCandidates prefilters companies by trigram similarity when available,
// else by first/last token LIKE. Bounded at 200 rows either way.
func nameCandidates(ctx context.Context, pool *pgxpool.Pool, tenantID, name string) []nameCand {
	var out []nameCand
	if trgmAvailable(ctx, pool) {
		rows, err := pool.Query(ctx, `SELECT id::text, name, COALESCE(city,'') FROM companies
			WHERE tenant_id=$1 AND lower(name) % lower($2)
			ORDER BY similarity(lower(name), lower($2)) DESC LIMIT 200`, tenantID, name)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var c nameCand
				if err := rows.Scan(&c.id, &c.name, &c.city); err == nil {
					out = append(out, c)
				}
			}
			return out
		}
	}
	toks := LegalTokens(name)
	like1, like2 := "%", "%"
	if len(toks) > 0 {
		like1 = toks[0] + "%"
		like2 = "%" + toks[len(toks)-1]
	}
	rows, err := pool.Query(ctx, `SELECT id::text, name, COALESCE(city,'') FROM companies
		WHERE tenant_id=$1 AND (lower(name) LIKE $2 OR lower(name) LIKE $3) LIMIT 200`,
		tenantID, like1, like2)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var c nameCand
		if err := rows.Scan(&c.id, &c.name, &c.city); err == nil {
			out = append(out, c)
		}
	}
	return out
}
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
