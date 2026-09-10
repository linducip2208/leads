package lists

import "leadforge/internal/webapp"

// PageMeta is layout metadata passed from the web server.
type PageMeta = webapp.Page

// Item is one list row.
type Item struct {
	ID      string
	Name    string
	Desc    string
	Members int
	Created string
}

// PageData powers the lists page.
type PageData struct {
	Items []Item
	Error string
}

// MemberRow is a list member lead.
type MemberRow struct {
	LeadID  string
	Company string
	City    string
	Score   int
	Status  string
}

// DetailData powers list detail.
type DetailData struct {
	List    Item
	Members []MemberRow
}
