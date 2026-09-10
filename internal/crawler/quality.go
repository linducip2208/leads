package crawler

import (
	"net/url"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// QualityScore rates extraction 0-100: name +15, description +10, email +20,
// phone +20, address +10, social +10, multiple useful pages +10, tech +5.
func QualityScore(d *Extracted, pages int) int {
	score := 0
	if d.CompanyName != "" {
		score += 15
	}
	if len(d.Description) > 20 {
		score += 10
	}
	if len(d.Emails) > 0 {
		score += 20
	}
	if len(d.Phones) > 0 {
		score += 20
	}
	if len(d.Address) > 8 {
		score += 10
	}
	if len(d.Socials) > 0 {
		score += 10
	}
	if pages > 1 {
		score += 10
	}
	if len(d.Technologies) > 0 {
		score += 5
	}
	if score > 100 {
		score = 100
	}
	return score
}

// needsBrowser decides the headless fallback: explicit JS shell, or thin
// content (quality < 40 with pages fetched but nothing useful).
func needsBrowser(d *Extracted, pages int) bool {
	if d.JSRequired {
		return true
	}
	return pages > 0 && QualityScore(d, pages) < 40 && d.CompanyName == "" && len(d.Emails) == 0
}

// skipPath reports URL paths never worth crawling.
func skipPath(path string) bool {
	p := strings.ToLower(path)
	bad := []string{
		"login", "signin", "sign-in", "signup", "sign-up", "register",
		"checkout", "/cart", "privacy", "terms", "cookie", "cookies",
		"wp-admin", "wp-login", "/account", "/search?", "?s=",
		".pdf", ".zip", ".rar", ".iso", ".exe", ".dmg", ".mp4", ".mp3",
		".avi", ".mov", ".wav", ".flac", ".png", ".jpg", ".jpeg",
		".gif", ".webp", ".svg", ".ico", ".css", ".js",
	}
	for _, b := range bad {
		if strings.Contains(p, b) {
			return true
		}
	}
	return false
}

// linkScore prioritizes contact/about-style pages for discovery.
func linkScore(path string) int {
	p := strings.ToLower(path)
	switch {
	case p == "" || p == "/":
		return 50
	case strings.Contains(p, "contact"):
		return 100
	case strings.Contains(p, "about"):
		return 90
	case strings.Contains(p, "team"):
		return 80
	case strings.Contains(p, "service") || strings.Contains(p, "product") || strings.Contains(p, "solution"):
		return 70
	case strings.Contains(p, "location") || strings.Contains(p, "company"):
		return 60
	case strings.Contains(p, "blog") || strings.Contains(p, "news") || strings.Contains(p, "career"):
		return 10
	default:
		return 30
	}
}

// priorityLinks extracts top-N same-site discovery links from HTML.
func priorityLinks(baseURL, html string, n int) []string {
	if n <= 0 {
		return nil
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil
	}
	type cand struct {
		url   string
		score int
	}
	seen := map[string]bool{}
	var out []cand
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		u, err := base.Parse(strings.TrimSpace(href))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return
		}
		if !sameHostSuffix(u.Host, base.Host) {
			return
		}
		u.Fragment = ""
		key := u.String()
		if seen[key] || skipPath(u.Path) {
			return
		}
		seen[key] = true
		out = append(out, cand{url: key, score: linkScore(u.Path)})
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	var res []string
	for i := 0; i < len(out) && i < n; i++ {
		res = append(res, out[i].url)
	}
	return res
}

func sameHostSuffix(host, base string) bool {
	host = strings.ToLower(host)
	base = strings.ToLower(base)
	if host == base {
		return true
	}
	trimmed := strings.TrimPrefix(base, "www.")
	if host == trimmed || host == "www."+trimmed {
		return true
	}
	return strings.HasSuffix(host, "."+trimmed)
}

// acceptableContent reports crawlable content types.
func acceptableContent(ct string) bool {
	if ct == "" {
		return true // unknown: fetch, body cap protects
	}
	ct = strings.ToLower(ct)
	return strings.Contains(ct, "html") || strings.Contains(ct, "text") || strings.Contains(ct, "xml")
}

// linkPath extracts the path+query of a URL for scoring.
func linkPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.RawQuery != "" {
		return u.Path + "?" + u.RawQuery
	}
	return u.Path
}
