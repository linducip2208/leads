package campaigns

import "leadforge/internal/webapp"

// PageMeta is layout metadata passed from the web server.
type PageMeta = webapp.Page

// Option is a select option.
type Option struct {
	Value string
	Label string
}

// Item is one campaign row.
type Item struct {
	ID      string
	Name    string
	Status  string
	Total   int
	Sent    int
	Replies int
	Bounces int
	Unsubs  int
	Created string
	Account string
}

// ListData powers the campaigns table.
type ListData struct {
	Items []Item
}

// FormData echoes the new-campaign form.
type FormData struct {
	Name         string
	AudienceType string
	AudienceID   string
	TemplateID   string
	AccountID    string
	ScheduledAt  string
	Error        string
}

// NewData powers campaign creation.
type NewData struct {
	Form      FormData
	Segments  []Option
	Lists     []Option
	Templates []Option
	Accounts  []Option
}

// Step is one sequence step.
type Step struct {
	ID        string
	Position  int
	Kind      string
	DayOff    int
	WaitDays  int
	Subject   string
	Body      string
	StopReply bool
}

// ContactRow is one audience member.
type ContactRow struct {
	ID       string
	Name     string
	Email    string
	Status   string
	Step     int
	NextSend string
}

// DetailData powers campaign detail (sequence editor + audience).
type DetailData struct {
	ID        string
	Name      string
	Status    string
	Account   string
	AccountID string
	Audience  string
	Sent      int
	Total     int
	Opens     int
	Clicks    int
	Replies   int
	Bounces   int
	Unsubs    int
	Scheduled string
	Started   string
	Steps     []Step
	Contacts  []ContactRow
	Accounts  []Option
}
