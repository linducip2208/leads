package tasks

import "leadforge/internal/webapp"

// PageMeta is layout metadata passed from the web server.
type PageMeta = webapp.Page

// Option is a select option.
type Option struct {
	Value string
	Label string
}

// Item is one task row.
type Item struct {
	ID       string
	Title    string
	Kind     string
	Priority string
	Status   string
	Due      string
	Overdue  bool
	Assignee string
	LeadID   string
	LeadName string
	DealID   string
	DealName string
}

// ListData powers the tasks page.
type ListData struct {
	Items    []Item
	Filter   string
	LeadID   string
	LeadName string
	Kinds    []Option
}

// FormData echoes the task form.
type FormData struct {
	Title    string
	Kind     string
	Priority string
	Due      string
	LeadID   string
	DealID   string
	Error    string
}
