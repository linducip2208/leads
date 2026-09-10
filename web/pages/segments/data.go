package segments

import "leadforge/internal/webapp"

// PageMeta is layout metadata passed from the web server.
type PageMeta = webapp.Page

// Item is one segment row.
type Item struct {
	ID      string
	Name    string
	Summary string
	Members int
	Dynamic bool
	Created string
}

// PageData powers the segments list.
type PageData struct {
	Items []Item
	Error string
}

// MemberRow is a segment member lead.
type MemberRow struct {
	LeadID  string
	Company string
	City    string
	Score   int
	Status  string
}

// DetailData powers segment detail.
type DetailData struct {
	Segment Item
	Members []MemberRow
}
