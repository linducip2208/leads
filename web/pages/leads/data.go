package leads

import (
	"net/url"

	"leadforge/internal/webapp"
)

// PageMeta is layout metadata passed from the web server.
type PageMeta = webapp.Page

// Option is a select option.
type Option struct {
	Value string
	Label string
}

// Filter carries list filters.
type Filter struct {
	Tab         string
	Keyword     string
	Industry    string
	Country     string
	Province    string
	City        string
	Source      string
	Owner       string
	MinScore    int
	HasWebsite  bool
	HasEmail    bool
	HasPhone    bool
	HasWhatsApp bool
	From        string
	To          string
	SearchID    string
}

// TabCount is a status tab with count.
type TabCount struct {
	Value  string
	Label  string
	Count  int
	Active bool
}

// Item is one lead row.
type Item struct {
	ID           string
	Company      string
	CompanyID    string
	Contact      string
	Industry     string
	City         string
	Province     string
	Email        string
	Phone        string
	Score        int
	Status       string
	Owner        string
	Source       string
	LastActivity string
	Created      string
	Quality      int
	Opps         int
	Contactable  bool
}

// ListData powers the leads table.
type ListData struct {
	Filter     Filter
	Tabs       []TabCount
	Items      []Item
	NextCursor string
	HasMore    bool
	Owners     []Option
	Sources    []Option
	Industries []Option
	Lists      []Option
}

// ContactRow is a related contact.
type ContactRow struct {
	ID       string
	Name     string
	Title    string
	Email    string
	Phone    string
	LinkedIn string
	Score    int
}

// ScoreItem is one scoring breakdown row.
type ScoreItem struct {
	Rule   string
	Points int
}

// ActivityRow is a timeline entry.
type ActivityRow struct {
	Kind    string
	Subject string
	Who     string
	When    string
}

// DealRow is a related deal.
type DealRow struct {
	ID     string
	Title  string
	Stage  string
	Value  string
	Status string
}

// EnrichRow is an enrichment run summary.
type EnrichRow struct {
	Kind       string
	Status     string
	Confidence int
	When       string
}

// OppRow is a detected opportunity.
type OppRow struct {
	Title      string
	Confidence int
	Reason     string
}

// DetailData powers lead detail + drawer.
type DetailData struct {
	ID            string
	Company       string
	CompanyID     string
	Website       string
	Domain        string
	Location      string
	Industry      string
	Score         int
	ScoreLabel    string
	Status        string
	Owner         string
	OwnerID       string
	Source        string
	SearchID      string
	Created       string
	Contact       ContactRow
	Contacts      []ContactRow
	Breakdown     []ScoreItem
	Activities    []ActivityRow
	Deals         []DealRow
	Enrichments   []EnrichRow
	Opportunities []OppRow
	Owners        []Option
	HasEmail      bool
	HasWhatsApp   bool
	Email         string
	WhatsAppLink  string
}

// filterQuery encodes the filter as a query string (cursor optional).
func (f Filter) filterQuery(cursor string) string {
	q := "tab=" + url.QueryEscape(f.Tab)
	add := func(k, v string) {
		if v != "" {
			q += "&" + k + "=" + url.QueryEscape(v)
		}
	}
	add("q", f.Keyword)
	add("industry", f.Industry)
	add("country", f.Country)
	add("province", f.Province)
	add("city", f.City)
	add("source", f.Source)
	add("owner", f.Owner)
	if f.MinScore > 0 {
		q += "&min_score=" + itoa(f.MinScore)
	}
	if f.HasWebsite {
		q += "&has_website=1"
	}
	if f.HasEmail {
		q += "&has_email=1"
	}
	if f.HasPhone {
		q += "&has_phone=1"
	}
	if f.HasWhatsApp {
		q += "&has_whatsapp=1"
	}
	add("from", f.From)
	add("to", f.To)
	add("search", f.SearchID)
	if cursor != "" {
		q += "&cursor=" + cursor
	}
	return q
}

func tabURL(f Filter, tab string) string {
	nf := f
	nf.Tab = tab
	return "/leads?" + nf.filterQuery("")
}

func moreURL(f Filter, cursor string) string {
	return "/leads?" + f.filterQuery(cursor)
}

func bulkRedirect(f Filter) string {
	return "/leads?" + f.filterQuery("")
}

func exportQuery(f Filter) string {
	return f.filterQuery("")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
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
