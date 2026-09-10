package scoring

import "leadforge/internal/webapp"

// PageMeta is layout metadata passed from the web server.
type PageMeta = webapp.Page

// Rule is one scoring rule row.
type Rule struct {
	ID       string
	Name     string
	Signal   string
	Operator string
	Value    string
	Weight   int
	Active   bool
	Builtin  bool
}

// PageData powers the scoring page.
type PageData struct {
	Defaults []Rule
	Custom   []Rule
	Error    string
}
