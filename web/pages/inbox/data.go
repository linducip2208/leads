package inbox

import "leadforge/internal/webapp"

// PageMeta is layout metadata passed from the web server.
type PageMeta = webapp.Page

// Item is one conversation row.
type Item struct {
	ID      string
	Contact string
	Email   string
	Subject string
	Unread  int
	Status  string
	Updated string
}

// ListData powers the inbox.
type ListData struct {
	Items  []Item
	Filter string
}

// Message is one email in a thread.
type Message struct {
	ID        string
	Direction string
	From      string
	To        string
	Subject   string
	Body      string
	When      string
}

// DetailData powers the conversation view.
type DetailData struct {
	ID        string
	Contact   string
	ContactID string
	Email     string
	Subject   string
	Status    string
	Messages  []Message
}
