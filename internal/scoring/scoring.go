// Package scoring implements rule-based lead scoring. No AI required.
// Tenant custom rules (scoring_rules) extend the built-in defaults.
package scoring

import (
	"context"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Signals are facts about a lead used by rules.
type Signals struct {
	Bool map[string]bool   // has_website, has_email, email_verified, has_phone, has_whatsapp
	Text map[string]string // industry, city, province, country, employee_range
}

// Rule is a single scoring rule.
type Rule struct {
	ID       string
	Name     string
	Signal   string
	Operator string // exists | equals | contains | gte | lte | in
	Value    string
	Weight   int
}

// Item is one applied rule in the breakdown.
type Item struct {
	Rule   string
	Points int
}

// DefaultRules are the built-in baseline weights.
func DefaultRules() []Rule {
	return []Rule{
		{Name: "Has website", Signal: "has_website", Operator: "exists", Weight: 10},
		{Name: "Has email", Signal: "has_email", Operator: "exists", Weight: 15},
		{Name: "Verified email", Signal: "email_verified", Operator: "exists", Weight: 20},
		{Name: "Has phone", Signal: "has_phone", Operator: "exists", Weight: 10},
		{Name: "Has WhatsApp", Signal: "has_whatsapp", Operator: "exists", Weight: 10},
		{Name: "Target industry", Signal: "industry_match", Operator: "exists", Weight: 20},
		{Name: "Target location", Signal: "location_match", Operator: "exists", Weight: 10},
		{Name: "Employee match", Signal: "employee_match", Operator: "exists", Weight: 15},
	}
}

// Evaluate sums default + custom rules, clamped to 0-100.
func Evaluate(sig Signals, custom []Rule) (score int, breakdown []Item) {
	for _, r := range append(append([]Rule{}, DefaultRules()...), custom...) {
		if matchRule(sig, r) {
			score += r.Weight
			breakdown = append(breakdown, Item{Rule: r.Name, Points: r.Weight})
		}
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return score, breakdown
}

func matchRule(sig Signals, r Rule) bool {
	switch r.Operator {
	case "exists":
		return sig.Bool[r.Signal]
	case "equals":
		return strings.EqualFold(strings.TrimSpace(sig.Text[r.Signal]), strings.TrimSpace(r.Value))
	case "contains":
		return strings.Contains(strings.ToLower(sig.Text[r.Signal]), strings.ToLower(r.Value))
	case "in":
		got := strings.ToLower(strings.TrimSpace(sig.Text[r.Signal]))
		for _, v := range strings.Split(r.Value, ",") {
			if strings.TrimSpace(strings.ToLower(v)) == got && got != "" {
				return true
			}
		}
		return false
	case "gte", "lte":
		got, err1 := strconv.Atoi(strings.TrimSpace(sig.Text[r.Signal]))
		want, err2 := strconv.Atoi(strings.TrimSpace(r.Value))
		if err1 != nil || err2 != nil {
			return false
		}
		if r.Operator == "gte" {
			return got >= want
		}
		return got <= want
	default:
		return false
	}
}

// Label maps a score to Low/Medium/Strong/Hot.
func Label(score int) string {
	switch {
	case score >= 90:
		return "Hot"
	case score >= 70:
		return "Strong"
	case score >= 50:
		return "Medium"
	default:
		return "Low"
	}
}

// StatusForScore maps a score to a lead status.
func StatusForScore(score int) string {
	switch {
	case score >= 90:
		return "hot"
	case score >= 50:
		return "qualified"
	default:
		return "new"
	}
}

// LoadTenantRules fetches active custom rules for a tenant in position order.
func LoadTenantRules(ctx context.Context, pool *pgxpool.Pool, tenantID string) ([]Rule, error) {
	rows, err := pool.Query(ctx, `
		SELECT id::text, name, signal, operator, value, weight
		FROM scoring_rules WHERE tenant_id=$1 AND is_active ORDER BY position, created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		var r Rule
		if err := rows.Scan(&r.ID, &r.Name, &r.Signal, &r.Operator, &r.Value, &r.Weight); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
