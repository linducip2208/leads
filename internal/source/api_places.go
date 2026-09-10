package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// CustomAPISource fetches candidates from a tenant-configured JSON endpoint.
// Expected payload: {"results":[{...}]} or a bare JSON array of objects with
// keys: name/company, website/domain/url, email, phone, address, city,
// province/state, country, industry.
type CustomAPISource struct{}

func NewCustomAPISource() *CustomAPISource { return &CustomAPISource{} }

func (c *CustomAPISource) Slug() string { return "custom_api" }
func (c *CustomAPISource) Name() string { return "Custom API" }

func (c *CustomAPISource) Search(ctx context.Context, query SearchQuery) (<-chan RawLead, error) {
	if strings.TrimSpace(query.APIURL) == "" {
		return nil, fmt.Errorf("custom api source: no endpoint configured")
	}
	if !strings.HasPrefix(query.APIURL, "https://") && !strings.HasPrefix(query.APIURL, "http://") {
		return nil, fmt.Errorf("custom api source: endpoint must be http(s)")
	}
	out := make(chan RawLead, 64)
	go func() {
		defer close(out)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, query.APIURL, nil)
		if err != nil {
			return
		}
		if key := query.APIConfig["api_key"]; key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		req.Header.Set("Accept", "application/json")
		client := &http.Client{Timeout: 30 * time.Second}
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
				SourceURL:  query.APIURL,
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

// GooglePlacesSource is an adapter for the official Google Places API. It
// stays dormant (returns a clear error) until an API key is configured, so the
// application always boots without one.
type GooglePlacesSource struct {
	apiKey string
}

func NewGooglePlacesSource(apiKey string) *GooglePlacesSource {
	return &GooglePlacesSource{apiKey: strings.TrimSpace(apiKey)}
}

func (g *GooglePlacesSource) Slug() string { return "google_places" }
func (g *GooglePlacesSource) Name() string { return "Google Places" }

func (g *GooglePlacesSource) Search(ctx context.Context, query SearchQuery) (<-chan RawLead, error) {
	if g.apiKey == "" {
		return nil, fmt.Errorf("google places source: no API key configured (set GOOGLE_PLACES_API_KEY to enable)")
	}
	// Full Places Text Search implementation is intentionally deferred until a
	// key is provided; the adapter contract is ready.
	return nil, fmt.Errorf("google places source: adapter scaffolded, implementation pending API key")
}
