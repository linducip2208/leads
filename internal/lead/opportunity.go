package lead

import "strings"

// Opportunity is a rule-detected sales angle. No AI involved.
type Opportunity struct {
	Title      string
	Confidence int
	Reason     string
}

// Completeness scores data quality 0-100 separately from sales fit.
func Completeness(name, website, email, phone, address, industry string, socials, description int) int {
	score := 0
	if name != "" {
		score += 15
	}
	if website != "" {
		score += 15
	}
	if email != "" {
		score += 20
	}
	if phone != "" {
		score += 15
	}
	if address != "" {
		score += 10
	}
	if industry != "" {
		score += 10
	}
	if socials > 0 {
		score += 10
	}
	if description > 20 {
		score += 5
	}
	if score > 100 {
		score = 100
	}
	return score
}

var retailHints = []string{
	"retail", "fashion", "food", "restaurant", "cafe", "coffee", "resto",
	"elektronik", "electronic", "gadget", "mart", "shop", "store", "kosmetik",
	"beauty", "furniture", "otomotif", "automotive",
}

var ecommerceTech = map[string]bool{
	"WooCommerce": true, "Shopify": true, "Wix": true, "Squarespace": true,
}

var crmTech = map[string]bool{
	"HubSpot": true, "Salesforce": true, "Zoho": true, "Odoo": true,
}

// DetectOpportunities applies deterministic product-fit rules.
func DetectOpportunities(industry string, technologies []string, hasWebsite bool) []Opportunity {
	ind := strings.ToLower(industry)
	isRetail := false
	for _, h := range retailHints {
		if strings.Contains(ind, h) {
			isRetail = true
			break
		}
	}
	hasEcom, hasCRM := false, false
	for _, t := range technologies {
		if ecommerceTech[t] {
			hasEcom = true
		}
		if crmTech[t] {
			hasCRM = true
		}
	}
	var out []Opportunity
	if isRetail && hasEcom {
		out = append(out, Opportunity{Title: "Ecommerce exists", Confidence: 80,
			Reason: "Retail company already selling online — pitch optimization, ads or ERP integration."})
	} else if isRetail {
		out = append(out, Opportunity{Title: "Potential Ecommerce Lead", Confidence: 65,
			Reason: "Retail company with no ecommerce signal detected."})
	}
	if hasWebsite && !hasCRM {
		out = append(out, Opportunity{Title: "Potential CRM Lead", Confidence: 60,
			Reason: "Company website found with no CRM/customer-portal signal."})
	}
	return out
}
