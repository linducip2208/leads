package source

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// discoveryHTTP is a small bounded client for keyless discovery endpoints.
// Real crawling uses the guarded client in internal/crawler.
var discoveryHTTP = &http.Client{Timeout: 20 * time.Second}

// WebsiteSearchSource discovers company websites via a keyless web search
// (DuckDuckGo HTML). Results are candidates only; the crawler verifies them.
type WebsiteSearchSource struct{}

func NewWebsiteSearchSource() *WebsiteSearchSource { return &WebsiteSearchSource{} }

func (w *WebsiteSearchSource) Slug() string { return "website_search" }
func (w *WebsiteSearchSource) Name() string { return "Web Search" }

func (w *WebsiteSearchSource) Search(ctx context.Context, query SearchQuery) (<-chan RawLead, error) {
	out := make(chan RawLead, 64)
	go func() {
		defer close(out)
		terms := query.Keywords()
		if loc := query.Location(); loc != "" {
			terms += " " + loc
		}
		if strings.TrimSpace(terms) == "" {
			return
		}
		seen := map[string]bool{}
		sent := 0
		// a few query shapes to widen coverage
		shapes := []string{terms, terms + " company profile", terms + " contact"}
		for _, shape := range shapes {
			if sent >= query.Limit && query.Limit > 0 {
				return
			}
			for _, u := range duckSearch(ctx, shape) {
				if sent >= query.Limit && query.Limit > 0 {
					return
				}
				host := strings.ToLower(u.Host)
				if host == "" || seen[host] || skipDiscoveryHost(host) {
					continue
				}
				seen[host] = true
				site := "https://" + host
				select {
				case <-ctx.Done():
					return
				case out <- RawLead{
					SourceSlug: w.Slug(),
					Website:    site,
					Domain:     host,
					SourceURL:  site,
					Country:    query.Country,
					Province:   query.Province,
					City:       query.City,
					Industry:   query.Industry,
				}:
					sent++
				}
			}
		}
	}()
	return out, nil
}

// duckSearch queries DuckDuckGo HTML and returns result URLs (best effort).
func duckSearch(ctx context.Context, q string) []*url.URL {
	form := url.Values{"q": {q}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://html.duckduckgo.com/html/", strings.NewReader(form.Encode()))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) LeadForge/1.0")
	resp, err := discoveryHTTP.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return nil
	}
	var out []*url.URL
	doc.Find("a.result__a").Each(func(_ int, s *goquery.Selection) {
		href, ok := s.Attr("href")
		if !ok {
			return
		}
		// DDG wraps in /l/?uddg=<target>
		if strings.HasPrefix(href, "/l/?") {
			if u, err := url.Parse("https://duckduckgo.com" + href); err == nil {
				if target := u.Query().Get("uddg"); target != "" {
					href = target
				}
			}
		}
		u, err := url.Parse(href)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return
		}
		out = append(out, u)
	})
	return out
}

// skipDiscoveryHost drops search engines, social giants and marketplaces that
// are not crawlable company sites.
func skipDiscoveryHost(host string) bool {
	host = strings.TrimPrefix(host, "www.")
	bad := []string{
		"duckduckgo.com", "google.", "bing.com", "yahoo.com", "yandex.",
		"facebook.com", "instagram.com", "linkedin.com", "twitter.com", "x.com",
		"youtube.com", "tiktok.com", "wikipedia.org", "tokopedia.com", "shopee.",
		"bukalapak.com", "lazada.", "amazon.", "tribunnews.com", "detik.com",
		"kompas.com", "liputan6.com", "maps.google.",
	}
	for _, b := range bad {
		if host == b || strings.HasSuffix(host, "."+b) || (strings.HasSuffix(b, ".") && strings.HasPrefix(host, strings.TrimSuffix(b, "."))) {
			return true
		}
	}
	return false
}

// PublicDirectorySource discovers real places via OpenStreetMap Nominatim
// (keyless, usage-policy friendly: 1 req/s). Results include names, addresses
// and — when mapped — websites and phones.
type PublicDirectorySource struct{}

func NewPublicDirectorySource() *PublicDirectorySource { return &PublicDirectorySource{} }

func (p *PublicDirectorySource) Slug() string { return "public_directory" }
func (p *PublicDirectorySource) Name() string { return "Public Directory (OSM)" }

func (p *PublicDirectorySource) Search(ctx context.Context, query SearchQuery) (<-chan RawLead, error) {
	out := make(chan RawLead, 64)
	go func() {
		defer close(out)
		terms := query.Keywords()
		if terms == "" {
			return
		}
		if loc := query.Location(); loc != "" {
			terms += " " + loc
		}
		places, err := nominatimSearch(ctx, terms, query.Limit)
		if err != nil {
			return
		}
		for _, pl := range places {
			select {
			case <-ctx.Done():
				return
			case out <- RawLead{
				SourceSlug: p.Slug(),
				ExternalID: pl.PlaceID,
				Name:       pl.Name,
				Website:    pl.Website,
				Phone:      pl.Phone,
				Address:    pl.Address,
				City:       firstNonEmpty(pl.City, query.City),
				Province:   firstNonEmpty(pl.Province, query.Province),
				Country:    firstNonEmpty(pl.Country, query.Country),
				Industry:   query.Industry,
				SourceURL:  "https://www.openstreetmap.org/" + pl.OSMRef,
				Payload:    map[string]string{"lat": pl.Lat, "lon": pl.Lon, "class": pl.Class},
			}:
			}
		}
	}()
	return out, nil
}

func nominatimSearch(ctx context.Context, q string, limit int) ([]nominatimPlace, error) {
	if limit <= 0 || limit > 50 {
		limit = 50 // Nominatim caps at 50
	}
	params := url.Values{
		"q":              {q},
		"format":         {"jsonv2"},
		"addressdetails": {"1"},
		"extratags":      {"1"},
		"limit":          {fmt.Sprint(limit)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://nominatim.openstreetmap.org/search?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "LeadForge/1.0 (lead discovery; contact: admin@localhost)")
	req.Header.Set("Accept", "application/json")
	resp, err := discoveryHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nominatim: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	return parseNominatim(body)
}
