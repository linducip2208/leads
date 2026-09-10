package imports

import "leadforge/internal/webapp"

// PageMeta is layout metadata passed from the web server.
type PageMeta = webapp.Page

// PastImport is one previous CSV import.
type PastImport struct {
	ID      string
	Name    string
	Status  string
	Found   int
	Saved   int
	Created string
}

// PageData powers the imports page.
type PageData struct {
	Error   string
	Imports []PastImport
}
