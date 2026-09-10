package crawler

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// Extracted holds company data parsed from crawled pages (merged across pages).
type Extracted struct {
	Title        string
	CompanyName  string
	Description  string
	Emails       []string
	Phones       []string
	WhatsApps    []string
	Address      string
	Socials      map[string]string
	Technologies []string
	// JSRequired flags pages that render content client-side (SPA shells).
	// Chromedp fallback can target these; the default crawler records and skips.
	JSRequired bool
}

var (
	emailRe = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
	// Indonesian + international phone shapes; validated for digit count later.
	phoneRe = regexp.MustCompile(`(?:\+?62|0)[\s\-.]?(?:\d[\s\-.]?){8,13}\d`)
	waRe    = regexp.MustCompile(`(?:wa\.me|api\.whatsapp\.com/send)[^"'\s]*?(?:phone|text=|/)([0-9+\s\-.%()]{8,20})`)
	// "(021) 555-1234" -> "021 555-1234" so area codes in parentheses match.
	parenArea = regexp.MustCompile(`\((0\d{1,4})\)`)
)

var emailBlocklistSuffix = []string{
	".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".css", ".js",
}

var socialMatchers = map[string][]string{
	"linkedin":  {"linkedin.com/company/", "linkedin.com/in/"},
	"instagram": {"instagram.com/"},
	"facebook":  {"facebook.com/", "fb.com/"},
	"x":         {"x.com/", "twitter.com/"},
	"youtube":   {"youtube.com/", "youtu.be/"},
}

var techSignatures = map[string][]string{
	"WordPress":        {"wp-content", "wp-includes"},
	"Shopify":          {"cdn.shopify.com"},
	"WooCommerce":      {"woocommerce"},
	"React":            {"__NEXT_DATA__", "react-dom", "_next/static"},
	"Next.js":          {"__NEXT_DATA__", "_next/static"},
	"Vue":              {"vue.runtime", "__VUE__"},
	"Laravel":          {"laravel"},
	"HubSpot":          {"hs-scripts", "hubspot"},
	"Google Analytics": {"googletagmanager", "google-analytics"},
	"Meta Pixel":       {"connect.facebook.net"},
	"Tailwind":         {"tailwind"},
	"Bootstrap":        {"bootstrap"},
	"jQuery":           {"jquery"},
	"Wix":              {"wixstatic", "wix.com"},
	"Squarespace":      {"squarespace"},
	"Webflow":          {"webflow"},
}

// Extract parses a single HTML page.
func Extract(pageURL string, body []byte) *Extracted {
	out := &Extracted{Socials: map[string]string{}}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return out
	}
	base, _ := url.Parse(pageURL)

	out.Title = strings.TrimSpace(doc.Find("title").First().Text())
	if meta := metaContent(doc, "og:site_name"); meta != "" {
		out.CompanyName = meta
	}
	if out.CompanyName == "" {
		if meta := metaContent(doc, "application-name"); meta != "" {
			out.CompanyName = meta
		}
	}
	if out.CompanyName == "" {
		out.CompanyName = strings.TrimSpace(doc.Find("h1").First().Text())
	}
	if meta := metaContent(doc, "description"); meta != "" {
		out.Description = meta
	} else if meta := metaContent(doc, "og:description"); meta != "" {
		out.Description = meta
	}

	seen := map[string]bool{}
	addEmail := func(e string) {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" || seen["e:"+e] || !strings.Contains(e, ".") {
			return
		}
		for _, suf := range emailBlocklistSuffix {
			if strings.HasSuffix(e, suf) {
				return
			}
		}
		seen["e:"+e] = true
		out.Emails = append(out.Emails, e)
	}
	// mailto links first (highest confidence)
	doc.Find("a[href^='mailto:']").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		addr := strings.TrimPrefix(href, "mailto:")
		if i := strings.Index(addr, "?"); i >= 0 {
			addr = addr[:i]
		}
		addEmail(addr)
	})
	// visible text + common contact containers
	text := doc.Find("body").Text()
	for _, m := range emailRe.FindAllString(text, 40) {
		addEmail(m)
	}

	seenPh := map[string]bool{}
	addPhone := func(p string) {
		p = strings.TrimSpace(p)
		digits := digitCount(p)
		if digits < 9 || digits > 15 || seenPh[p] {
			return
		}
		seenPh[p] = true
		out.Phones = append(out.Phones, p)
	}
	doc.Find("a[href^='tel:']").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		addPhone(strings.TrimPrefix(href, "tel:"))
	})
	phoneText := parenArea.ReplaceAllString(text, "$1")
	for _, m := range phoneRe.FindAllString(phoneText, 40) {
		addPhone(strings.TrimSpace(m))
	}

	// WhatsApp links
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		lh := strings.ToLower(href)
		if strings.Contains(lh, "wa.me/") || strings.Contains(lh, "whatsapp.com") {
			u, err := url.Parse(href)
			if err != nil {
				return
			}
			num := strings.Trim(u.Path, "/")
			if m := waRe.FindStringSubmatch(href); len(m) > 1 && strings.TrimSpace(m[1]) != "" {
				num = m[1]
			}
			num = strings.Map(func(r rune) rune {
				if r >= '0' && r <= '9' || r == '+' {
					return r
				}
				return -1
			}, num)
			if digitCount(num) >= 9 {
				out.WhatsApps = appendUnique(out.WhatsApps, num)
			}
		}
	})

	// socials
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		lh := strings.ToLower(href)
		for key, pats := range socialMatchers {
			if _, taken := out.Socials[key]; taken {
				continue
			}
			for _, p := range pats {
				if strings.Contains(lh, p) {
					abs := href
					if base != nil {
						if u, err := base.Parse(href); err == nil {
							abs = u.String()
						}
					}
					out.Socials[key] = abs
					break
				}
			}
		}
	})

	// address-ish blocks
	for _, sel := range []string{"address", "[itemtype*='PostalAddress']", ".address", ".contact-address", "#contact address"} {
		if t := strings.TrimSpace(doc.Find(sel).First().Text()); len(t) > 8 && len(t) < 500 {
			out.Address = collapseSpace(t)
			break
		}
	}

	// technology fingerprints from markup
	html := string(body)
	lh := strings.ToLower(html)
	for tech, sigs := range techSignatures {
		for _, sig := range sigs {
			if strings.Contains(lh, strings.ToLower(sig)) {
				out.Technologies = appendUnique(out.Technologies, tech)
				break
			}
		}
	}

	// JS-rendered shell detection: mount node present but almost no content.
	textLen := len(strings.Fields(text))
	hasMount := strings.Contains(lh, `id="root"`) || strings.Contains(lh, `id="app"`) ||
		strings.Contains(lh, "__next_data__") || strings.Contains(lh, "ng-app") ||
		strings.Contains(lh, "data-reactroot")
	mentionsJS := strings.Contains(lh, "enable javascript") || strings.Contains(lh, "requires javascript")
	out.JSRequired = mentionsJS || (hasMount && textLen < 30 && len(out.Emails) == 0)
	return out
}

// Merge folds another page extraction into this one (union semantics).
func (e *Extracted) Merge(o *Extracted) {
	if e.CompanyName == "" {
		e.CompanyName = o.CompanyName
	}
	if e.Description == "" {
		e.Description = o.Description
	}
	if e.Address == "" {
		e.Address = o.Address
	}
	for _, v := range o.Emails {
		e.Emails = appendUnique(e.Emails, v)
	}
	for _, v := range o.Phones {
		e.Phones = appendUnique(e.Phones, v)
	}
	for _, v := range o.WhatsApps {
		e.WhatsApps = appendUnique(e.WhatsApps, v)
	}
	for _, v := range o.Technologies {
		e.Technologies = appendUnique(e.Technologies, v)
	}
	for k, v := range o.Socials {
		if _, ok := e.Socials[k]; !ok {
			e.Socials[k] = v
		}
	}
	e.JSRequired = e.JSRequired || o.JSRequired
}

// PrimaryEmail/Phone/WhatsApp pick the first value or "".
func (e *Extracted) PrimaryEmail() string    { return firstOf(e.Emails) }
func (e *Extracted) PrimaryPhone() string    { return firstOf(e.Phones) }
func (e *Extracted) PrimaryWhatsApp() string { return firstOf(e.WhatsApps) }

func firstOf(s []string) string {
	if len(s) > 0 {
		return s[0]
	}
	return ""
}

func metaContent(doc *goquery.Document, key string) string {
	if v, ok := doc.Find(`meta[property="` + key + `"]`).First().Attr("content"); ok {
		return strings.TrimSpace(v)
	}
	if v, ok := doc.Find(`meta[name="` + key + `"]`).First().Attr("content"); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func digitCount(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
