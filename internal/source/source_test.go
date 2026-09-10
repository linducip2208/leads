package source

import (
	"context"
	"strings"
	"testing"
)

func TestParseCSV(t *testing.T) {
	data := "Name,Website,Email,Phone,City\nPT Maju,majukonstruksi.co.id,info@majukonstruksi.co.id,08123456789,Jakarta\n"
	rows, err := ParseCSV([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0]["name"] != "PT Maju" || rows[0]["email"] != "info@majukonstruksi.co.id" {
		t.Fatalf("row = %v", rows[0])
	}
}

func TestCSVSource(t *testing.T) {
	data := "company,domain\nA,a.co.id\nB,b.co.id\n"
	ch, err := NewCSVSource().Search(context.Background(), SearchQuery{CSVData: []byte(data), Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var got []RawLead
	for l := range ch {
		got = append(got, l)
	}
	if len(got) != 2 || got[0].Website != "https://a.co.id" {
		t.Fatalf("got %+v", got)
	}
}

func TestManualSourceSkipsBadURLs(t *testing.T) {
	ch, err := NewManualSource().Search(context.Background(), SearchQuery{
		SeedURLs: []string{"https://example.co.id", "not a url target with spaces and no dot?", "ftp://x.com", ""},
		Limit:    10,
	})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for l := range ch {
		n++
		if !strings.HasPrefix(l.Website, "https://") {
			t.Errorf("bad website %q", l.Website)
		}
	}
	if n != 1 {
		t.Fatalf("n = %d, want 1", n)
	}
}

func TestParseNominatim(t *testing.T) {
	body := `[{"place_id":1,"osm_type":"node","osm_id":2,"lat":"-6.2","lon":"106.8","class":"shop",
"display_name":"Toko Maju, Jl. Sudirman, Jakarta, Indonesia",
"address":{"road":"Jl. Sudirman","city":"Jakarta","country":"Indonesia"},
"extratags":{"website":"https://tokomaju.co.id","phone":"+62215550111"}}]`
	places, err := parseNominatim([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(places) != 1 {
		t.Fatalf("places = %d", len(places))
	}
	p := places[0]
	if p.Name != "Toko Maju" || p.Website != "https://tokomaju.co.id" || p.Phone != "+62215550111" {
		t.Fatalf("place = %+v", p)
	}
}
