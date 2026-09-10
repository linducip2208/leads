package activities

import "leadforge/internal/webapp"

// PageMeta is layout metadata passed from the web server.
type PageMeta = webapp.Page

// Item is one activity entry.
type Item struct {
	Kind    string
	Subject string
	Who     string
	When    string
	LeadID  string
}

// FeedData powers the activities feed.
type FeedData struct {
	Items []Item
	Kind  string
	Kinds []string
}
