package source

import (
	"encoding/json"
	"strings"
)

// nominatimPlace is a parsed Nominatim result.
type nominatimPlace struct {
	PlaceID  string
	OSMRef   string
	Name     string
	Address  string
	City     string
	Province string
	Country  string
	Website  string
	Phone    string
	Lat      string
	Lon      string
	Class    string
}

type nominatimRaw struct {
	PlaceID     int64             `json:"place_id"`
	OSMType     string            `json:"osm_type"`
	OSMID       int64             `json:"osm_id"`
	Lat         string            `json:"lat"`
	Lon         string            `json:"lon"`
	Class       string            `json:"class"`
	DisplayName string            `json:"display_name"`
	Address     map[string]string `json:"address"`
	ExtraTags   map[string]string `json:"extratags"`
}

func parseNominatim(body []byte) ([]nominatimPlace, error) {
	var raw []nominatimRaw
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make([]nominatimPlace, 0, len(raw))
	for _, r := range raw {
		// skip pure administrative boundaries; keep venues/companies
		if r.Class == "boundary" || r.Class == "place" && isLocalityOnly(r) {
			continue
		}
		name := displayName(r.DisplayName)
		p := nominatimPlace{
			PlaceID: itoa64(r.PlaceID),
			OSMRef:  r.OSMType + "/" + itoa64(r.OSMID),
			Name:    name,
			Address: r.DisplayName,
			Lat:     r.Lat,
			Lon:     r.Lon,
			Class:   r.Class,
		}
		if r.Address != nil {
			p.City = firstNonEmpty(r.Address["city"], r.Address["town"], r.Address["village"], r.Address["municipality"])
			p.Province = firstNonEmpty(r.Address["state"], r.Address["province"], r.Address["region"])
			p.Country = r.Address["country"]
		}
		if r.ExtraTags != nil {
			p.Website = firstNonEmpty(r.ExtraTags["website"], r.ExtraTags["contact:website"])
			p.Phone = firstNonEmpty(r.ExtraTags["phone"], r.ExtraTags["contact:phone"])
		}
		if p.Name == "" {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func isLocalityOnly(r nominatimRaw) bool {
	if r.Address == nil {
		return true
	}
	_, hasRoad := r.Address["road"]
	_, hasHouse := r.Address["house_number"]
	_, hasAmenity := r.Address["amenity"]
	_, hasShop := r.Address["shop"]
	_, hasOffice := r.Address["office"]
	return !hasRoad && !hasHouse && !hasAmenity && !hasShop && !hasOffice
}

func displayName(full string) string {
	if i := strings.Index(full, ","); i > 0 {
		return strings.TrimSpace(full[:i])
	}
	return strings.TrimSpace(full)
}

func itoa64(n int64) string {
	if n == 0 {
		return ""
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
