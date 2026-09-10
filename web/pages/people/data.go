package people

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
	Keyword string
	Company string
	Title   string
	Owner   string
}

// Item is one contact row.
type Item struct {
	ID      string
	Name    string
	Company string
	Title   string
	Email   string
	EStatus string
	Phone   string
	Score   int
	Owner   string
	Source  string
}

// ListData powers the people table.
type ListData struct {
	Filter     Filter
	Items      []Item
	NextCursor string
	HasMore    bool
	Owners     []Option
}

// FormData echoes the contact form.
type FormData struct {
	FirstName string
	LastName  string
	JobTitle  string
	Email     string
	Phone     string
	LinkedIn  string
	CompanyID string
	Error     string
}

// DetailData powers contact detail.
type DetailData struct {
	ID        string
	Name      string
	Company   string
	CompanyID string
	Form      FormData
	Companies []Option
	Score     int
	Source    string
	Created   string
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
	add("company", f.Company)
	add("title", f.Title)
	add("owner", f.Owner)
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
		return "/people?" + q
	}
	return "/people"
}
