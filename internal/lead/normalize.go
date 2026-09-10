// Package lead implements normalization and deduplication of discovered
// candidates before they become companies, contacts and leads.
package lead

import (
	"net/url"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/idna"
)

// Normalized is a cleaned candidate.
type Normalized struct {
	Name      string
	Domain    string
	Website   string
	Email     string
	Phone     string // display form
	PhoneE164 string // canonical form for matching
	WhatsApp  string
	Country   string
	Province  string
	City      string
	Address   string
	Industry  string
}

// NormalizeCandidate cleans all raw fields, preserving meaningful originals.
func NormalizeCandidate(name, website, email, phone, whatsapp, country, province, city, address, industry string) Normalized {
	n := Normalized{}
	n.Name = NormalizeName(name)
	n.Website, n.Domain = NormalizeWebsite(website)
	n.Email = NormalizeEmail(email)
	n.Phone, n.PhoneE164 = NormalizePhone(phone)
	n.WhatsApp, _ = NormalizePhone(whatsapp)
	n.Country = NormalizePlace(country)
	n.Province = NormalizePlace(province)
	n.City = NormalizePlace(city)
	n.Address = strings.Join(strings.Fields(strings.TrimSpace(address)), " ")
	n.Industry = strings.TrimSpace(industry)
	return n
}

// NormalizeName trims, collapses spaces and strips common legal suffixes noise.
func NormalizeName(s string) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	for _, suf := range []string{", PT", ", CV", ", UD"} {
		s = strings.TrimSuffix(s, suf)
	}
	return strings.TrimSpace(s)
}

// NormalizeWebsite returns canonical website URL + bare domain.
func NormalizeWebsite(raw string) (website, domain string) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return "", ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", ""
	}
	domain = NormalizeDomain(u.Hostname())
	if domain == "" {
		return "", ""
	}
	if port := u.Port(); port != "" {
		u.Host = domain + ":" + port
	} else {
		u.Host = domain
	}
	u.Fragment = ""
	website = strings.TrimSuffix(u.String(), "/")
	if website == "https://"+domain {
		website = "https://" + domain
	}
	return website, domain
}

// NormalizeDomain lowercases and strips www. and port. Single-label hosts
// (localhost, intranet names) are kept; garbage with spaces/slashes is not.
func NormalizeDomain(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, err := splitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimSuffix(host, ".")
	if host == "" || strings.ContainsAny(host, " /") {
		return ""
	}
	return host
}

// NormalizeEmail lowercases and validates shape.
func NormalizeEmail(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	parts := strings.Split(s, "@")
	if len(parts) != 2 || parts[0] == "" || !strings.Contains(parts[1], ".") {
		return ""
	}
	return s
}

var nonDigit = regexp.MustCompile(`[^0-9]`)

// NormalizePhone converts Indonesian numbers to E.164 (+62...) and returns
// display + canonical forms. 081296052010 -> +6281296052010.
func NormalizePhone(s string) (display, e164 string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	plus := strings.HasPrefix(s, "+")
	digits := nonDigit.ReplaceAllString(s, "")
	if digits == "" {
		return "", ""
	}
	switch {
	case strings.HasPrefix(digits, "62"):
		// already country-code form — keep as-is
	case strings.HasPrefix(digits, "0"):
		digits = "62" + digits[1:]
	case strings.HasPrefix(digits, "8") && len(digits) >= 9 && len(digits) <= 13:
		digits = "62" + digits
	default:
		// non-ID numbers: keep as-is if plausible
		if len(digits) < 9 || len(digits) > 15 {
			return "", ""
		}
		if plus {
			return "+" + digits, "+" + digits
		}
		return digits, digits
	}
	if len(digits) < 10 || len(digits) > 16 { // 62 + 8..14 digits
		return "", ""
	}
	e164 = "+" + digits
	// display: +62 812-9605-2010 style grouping
	rest := digits[2:]
	display = "+62 " + groupDigits(rest)
	return display, e164
}

func groupDigits(s string) string {
	var parts []string
	for len(s) > 4 {
		parts = append(parts, s[:4])
		s = s[4:]
	}
	if s != "" {
		parts = append(parts, s)
	}
	return strings.Join(parts, "-")
}

// PhoneKind classifies an E.164 number: mobile (Indonesian 08…/628…),
// landline (area-code numbers), or unknown. Only mobile numbers are
// WhatsApp candidates — never assume every number is WhatsApp.
func PhoneKind(e164 string) string {
	d := nonDigit.ReplaceAllString(e164, "")
	if strings.HasPrefix(d, "62") {
		rest := d[2:]
		if rest == "" {
			return "unknown"
		}
		if rest[0] == '8' && len(rest) >= 9 && len(rest) <= 13 {
			return "mobile"
		}
		if len(rest) >= 8 && len(rest) <= 12 {
			return "landline"
		}
		return "unknown"
	}
	if len(d) >= 9 && len(d) <= 15 {
		return "unknown"
	}
	return "unknown"
}

// NormalizePlace trims and title-cases short place names.
func NormalizePlace(s string) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if s == "" {
		return ""
	}
	upper := map[string]bool{"DKI": true, "DIY": true, "NTB": true, "NTT": true, "USA": true, "UK": true, "UAE": true}
	words := strings.Split(s, " ")
	for i, w := range words {
		if w == "" {
			continue
		}
		if upper[strings.ToUpper(w)] {
			words[i] = strings.ToUpper(w)
			continue
		}
		if w == strings.ToLower(w) || w == strings.ToUpper(w) {
			words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
		}
	}
	return strings.Join(words, " ")
}

func splitHostPort(hostport string) (string, string, error) {
	if strings.Count(hostport, ":") == 1 {
		if i := strings.LastIndex(hostport, ":"); i >= 0 {
			port := hostport[i+1:]
			if port != "" && nonDigit.ReplaceAllString(port, "") == port {
				return hostport[:i], port, nil
			}
		}
	}
	return hostport, "", errNoPort
}

var errNoPort = errorString("no port")

type errorString string

func (e errorString) Error() string { return string(e) }

// CanonicalDomain lowercases, strips www./port/trailing dot and applies
// IDNA so https://www.Example.COM/ and http://example.com/about both become
// example.com.
func CanonicalDomain(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, err := splitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimSuffix(host, ".")
	if host == "" || strings.ContainsAny(host, " /") {
		return ""
	}
	if ascii, err := idna.Lookup.ToASCII(host); err == nil && ascii != "" {
		host = ascii
	}
	return host
}

// legalEntities are stripped for name comparison so "PT Maju Jaya" and
// "Maju Jaya PT" compare equal.
var legalEntities = map[string]bool{
	"pt": true, "cv": true, "ud": true, "perseroan": true, "terbatas": true,
	"tbk": true, "ltd": true, "inc": true, "corp": true, "corporation": true,
	"gmbh": true, "pte": true, "llc": true, "co": true, "firma": true, "fa": true,
}

var tokenSplitter = regexp.MustCompile(`[^a-z0-9]+`)

// LegalTokens tokenizes a company name minus legal-entity words.
func LegalTokens(name string) []string {
	seen := map[string]bool{}
	var out []string
	for _, tok := range tokenSplitter.Split(strings.ToLower(name), -1) {
		if tok == "" || legalEntities[tok] || seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	sort.Strings(out)
	return out
}

// NameSimilarity is Jaccard similarity over legal-stripped tokens (0-1).
func NameSimilarity(a, b string) float64 {
	ta, tb := LegalTokens(a), LegalTokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	set := map[string]bool{}
	for _, t := range ta {
		set[t] = true
	}
	inter := 0
	for _, t := range tb {
		if set[t] {
			inter++
		}
	}
	union := len(ta) + len(tb) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}
