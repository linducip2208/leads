package finder

import "leadforge/internal/webapp"

// PageMeta is layout metadata passed from the web server.
type PageMeta = webapp.Page

// Option is a select option.
type Option struct {
	Value string
	Label string
}

// SourceOpt is a discovery source with live status.
type SourceOpt struct {
	Value       string
	Label       string
	Description string
	Status      string // ready | needs_key | n/a
	Hint        string
}

// FormData echoes the finder form with validation errors.
type FormData struct {
	Keyword     string
	Industry    string
	Country     string
	Province    string
	City        string
	CompanySize string
	HasWebsite  bool
	HasEmail    bool
	HasPhone    bool
	HasWhatsApp bool
	MinScore    int
	ResultLimit int
	CustomLimit string
	ICPID       string
	SeedURLs    string
	Sources     []string
	Error       string
}

// HasSource reports whether a source slug was checked.
func (f FormData) HasSource(slug string) bool {
	for _, s := range f.Sources {
		if s == slug {
			return true
		}
	}
	return false
}

// PageData powers the finder page.
type PageData struct {
	ICPs    []Option
	Limits  []Option
	Sources []SourceOpt
	Form    FormData
}
