package companies

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
	Keyword  string
	Industry string
	City     string
	Owner    string
	MinScore int
}

// Item is one company row.
type Item struct {
	ID           string
	Name         string
	Industry     string
	City         string
	Province     string
	Website      string
	Contacts     int
	Score        int
	Owner        string
	Source       string
	LastActivity string
}

// ListData powers the companies table.
type ListData struct {
	Filter     Filter
	Items      []Item
	NextCursor string
	HasMore    bool
	Owners     []Option
	Industries []Option
}

// ContactRow is a company contact.
type ContactRow struct {
	ID    string
	Name  string
	Title string
	Email string
	Phone string
}

// LeadRow is a company lead.
type LeadRow struct {
	ID     string
	Score  int
	Status string
}

// DealRow is a company deal.
type DealRow struct {
	ID     string
	Title  string
	Stage  string
	Value  string
	Status string
}

// FormData echoes the company form.
type FormData struct {
	Name     string
	Industry string
	Website  string
	Phone    string
	Country  string
	Province string
	City     string
	Address  string
	OwnerID  string
	Error    string
}

// DetailData powers company detail.
type DetailData struct {
	ID         string
	Name       string
	Contacts   []ContactRow
	Leads      []LeadRow
	Deals      []DealRow
	Form       FormData
	Owners     []Option
	Score      int
	Source     string
	Created    string
	EnrichedAt string
}

func (f Filter) query(cursor string) string {
	q := ""
	add := func(k, v string) {
		if v != "" {
			if q != "" {
				q += "&"
			}
			q += k + "=" + url.QueryEscape(v)
		}
	}
	add("q", f.Keyword)
	add("industry", f.Industry)
	add("city", f.City)
	add("owner", f.Owner)
	if f.MinScore > 0 {
		if q != "" {
			q += "&"
		}
		q += "min_score=" + itoa(f.MinScore)
	}
	if cursor != "" {
		if q != "" {
			q += "&"
		}
		q += "cursor=" + url.QueryEscape(cursor)
	}
	return q
}

// ListURL rebuilds the list URL.
func (f Filter) ListURL(cursor string) string {
	if q := f.query(cursor); q != "" {
		return "/companies?" + q
	}
	return "/companies"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
