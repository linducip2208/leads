package deals

import "leadforge/internal/webapp"

// PageMeta is layout metadata passed from the web server.
type PageMeta = webapp.Page

// Option is a select option.
type Option struct {
	Value string
	Label string
}

// Item is one deal row.
type Item struct {
	ID       string
	Title    string
	Company  string
	Stage    string
	Value    string
	Prob     int
	Close    string
	Owner    string
	Status   string
	Pipeline string
}

// ListData powers the deals table.
type ListData struct {
	Items  []Item
	Status string
}

// FormData echoes the deal form.
type FormData struct {
	Title       string
	CompanyID   string
	CompanyName string
	ContactID   string
	LeadID      string
	Value       string
	Currency    string
	Probability int
	PipelineID  string
	StageID     string
	Expected    string
	OwnerID     string
	Error       string
}

// DetailData powers deal detail.
type DetailData struct {
	ID          string
	Title       string
	Company     string
	CompanyID   string
	Contact     string
	Value       string
	Currency    string
	Prob        int
	Stage       string
	StageID     string
	PipelineID  string
	Expected    string
	ValueNum    string
	ExpectedISO string
	Owner       string
	Status      string
	LostReason  string
	Created     string
	Stages      []Option
	Owners      []Option
	Timeline    []TimeRow
}

// TimeRow is a deal activity entry.
type TimeRow struct {
	Kind string
	Body string
	Who  string
	When string
}

// NewData powers the new-deal form.
type NewData struct {
	Form      FormData
	Companies []Option
	Stages    []Option
	Owners    []Option
	Leads     []Option
}
