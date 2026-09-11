package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"leadforge/internal/crawler"
)

// CustomAPISource fetches candidates from a tenant-configured JSON endpoint.
// Expected payload: {"results":[{...}]} or a bare JSON array of objects with
// keys: name/company, website/domain/url, email, phone, address, city,
// province/state, country, industry.
type CustomAPISource struct {
	Guard crawler.Guard
}

func NewCustomAPISource(allowPrivate ...bool) *CustomAPISource {
	g := crawler.Guard{}
	if len(allowPrivate) > 0 {
		g.AllowPrivate = allowPrivate[0]
	}
	return &CustomAPISource{Guard: g}
}

func (c *CustomAPISource) Slug() string { return "custom_api" }
func (c *CustomAPISource) Name() string { return "Custom API" }

func (c *CustomAPISource) Info() SourceInfo {
	return SourceInfo{Slug: c.Slug(), Name: c.Name(),
		Description:        "Tenant-configured JSON endpoint.",
		Priority:           70,
		Confidence:         70,
		RequiresCredential: false}
}

func (c *CustomAPISource) Search(ctx context.Context, query SearchQuery) (<-chan RawLead, error) {
	if strings.TrimSpace(query.APIURL) == "" {
		return nil, fmt.Errorf("custom api source: no endpoint configured")
	}
	u, err := c.Guard.ValidateAndCheckURL(ctx, query.APIURL)
	if err != nil {
		return nil, fmt.Errorf("custom api source: endpoint rejected: %w", err)
	}
	out := make(chan RawLead, 64)
	go func() {
		defer close(out)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return
		}
		if key := query.APIConfig["api_key"]; key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		req.Header.Set("Accept", "application/json")
		client := c.Guard.NewClient(crawler.Options{Timeout: 30 * time.Second, MaxBodyBytes: 10 << 20})
		resp, err := client.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			if resp != nil {
				resp.Body.Close()
			}
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		if err != nil {
			return
		}
		for _, m := range parseCustomAPI(body) {
			lead := RawLead{
				SourceSlug: c.Slug(),
				Name:       firstNonEmpty(m["name"], m["company"], m["company_name"]),
				Website:    firstNonEmpty(m["website"], m["domain"], m["url"]),
				Email:      m["email"],
				Phone:      firstNonEmpty(m["phone"], m["mobile"]),
				Address:    m["address"],
				City:       firstNonEmpty(m["city"], query.City),
				Province:   firstNonEmpty(m["province"], m["state"], query.Province),
				Country:    firstNonEmpty(m["country"], query.Country),
				Industry:   firstNonEmpty(m["industry"], query.Industry),
				SourceURL:  u.String(),
			}
			if lead.Website != "" && !strings.Contains(lead.Website, "://") {
				lead.Website = "https://" + lead.Website
			}
			if lead.Name == "" && lead.Website == "" {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case out <- lead:
			}
		}
	}()
	return out, nil
}

func parseCustomAPI(body []byte) []map[string]string {
	var arr []map[string]any
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "{") {
		var wrapper struct {
			Results []map[string]any `json:"results"`
			Data    []map[string]any `json:"data"`
			Items   []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(body, &wrapper); err != nil {
			return nil
		}
		arr = append(append(wrapper.Results, wrapper.Data...), wrapper.Items...)
	} else {
		if err := json.Unmarshal(body, &arr); err != nil {
			return nil
		}
	}
	out := make([]map[string]string, 0, len(arr))
	for _, m := range arr {
		row := map[string]string{}
		for k, v := range m {
			row[strings.ToLower(k)] = strings.TrimSpace(fmt.Sprint(v))
		}
		out = append(out, row)
	}
	return out
}

// GooglePlacesSource queries the official Places API (Text Search, then
// Details for website/phone). Disabled without an API key; the tenant key
// (Settings → Integrations) wins over the global env key.
type GooglePlacesSource struct {
	apiKey string
}

func NewGooglePlacesSource(apiKey string) *GooglePlacesSource {
	return &GooglePlacesSource{apiKey: strings.TrimSpace(apiKey)}
}

func (g *GooglePlacesSource) Slug() string { return "google_places" }
func (g *GooglePlacesSource) Name() string { return "Google Places" }

func (g *GooglePlacesSource) Info() SourceInfo {
	return SourceInfo{Slug: g.Slug(), Name: g.Name(),
		Description:        "Official Google Places data (requires API key).",
		Priority:           100,
		Confidence:         90,
		RequiresCredential: true}
}

func (g *GooglePlacesSource) keyFor(query SearchQuery) string {
	if query.GoogleKey != "" {
		return query.GoogleKey
	}
	return g.apiKey
}

func (g *GooglePlacesSource) Search(ctx context.Context, query SearchQuery) (<-chan RawLead, error) {
	key := g.keyFor(query)
	if key == "" {
		return nil, fmt.Errorf("google places source: not configured (add an API key in Settings → Integrations)")
	}
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
		places, err := placesTextSearch(ctx, key, terms)
		if err != nil {
			return
		}
		sent := 0
		for _, pl := range places {
			if sent >= query.Limit && query.Limit > 0 {
				return
			}
			// details for website + phone (bounded: only while under limit)
			if det, err := placeDetails(ctx, key, pl.PlaceID); err == nil {
				if det.Website != "" {
					pl.Website = det.Website
				}
				if det.Phone != "" {
					pl.Phone = det.Phone
				}
			}
			select {
			case <-ctx.Done():
				return
			case out <- RawLead{
				SourceSlug: g.Slug(), SourceConf: 90,
				ExternalID: pl.PlaceID, Name: pl.Name, Website: pl.Website,
				Phone: pl.Phone, Address: pl.Address,
				Country: query.Country, Province: query.Province, City: query.City,
				Industry: query.Industry, SourceURL: "https://www.google.com/maps/place/?q=place_id:" + pl.PlaceID,
				DiscoveredAt: time.Now(),
				Payload:      map[string]string{"rating": pl.Rating},
			}:
				sent++
			}
		}
	}()
	return out, nil
}

type gplace struct {
	PlaceID string
	Name    string
	Address string
	Website string
	Phone   string
	Rating  string
}

func placesTextSearch(ctx context.Context, key, terms string) ([]gplace, error) {
	params := url.Values{"query": {terms}, "key": {key}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://maps.googleapis.com/maps/api/place/textsearch/json?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := discoveryHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Status  string `json:"status"`
		Message string `json:"error_message"`
		Results []struct {
			PlaceID          string  `json:"place_id"`
			Name             string  `json:"name"`
			FormattedAddress string  `json:"formatted_address"`
			Rating           float64 `json:"rating"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if parsed.Status != "OK" && parsed.Status != "ZERO_RESULTS" {
		return nil, fmt.Errorf("google places: %s %s", parsed.Status, parsed.Message)
	}
	var out []gplace
	for _, r := range parsed.Results {
		out = append(out, gplace{PlaceID: r.PlaceID, Name: r.Name, Address: r.FormattedAddress,
			Rating: fmt.Sprintf("%.1f", r.Rating)})
	}
	return out, nil
}

func placeDetails(ctx context.Context, key, placeID string) (gplace, error) {
	var det gplace
	params := url.Values{
		"place_id": {placeID}, "key": {key},
		"fields": {"website,formatted_phone_number"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://maps.googleapis.com/maps/api/place/details/json?"+params.Encode(), nil)
	if err != nil {
		return det, err
	}
	resp, err := discoveryHTTP.Do(req)
	if err != nil {
		return det, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return det, err
	}
	var parsed struct {
		Status string `json:"status"`
		Result struct {
			Website string `json:"website"`
			Phone   string `json:"formatted_phone_number"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return det, err
	}
	if parsed.Status != "OK" {
		return det, fmt.Errorf("details: %s", parsed.Status)
	}
	det.Website = parsed.Result.Website
	det.Phone = parsed.Result.Phone
	return det, nil
}
