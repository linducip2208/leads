package icp

import "leadforge/internal/webapp"

// PageMeta is layout metadata passed from the web server.
type PageMeta = webapp.Page

// Item is one ICP row.
type Item struct {
	ID        string
	Name      string
	Summary   string
	IsDefault bool
}

// FormData echoes the ICP form.
type FormData struct {
	ID        string
	Name      string
	Industry  string
	Country   string
	Province  string
	City      string
	Employee  string
	Keywords  string
	MinScore  int
	IsDefault bool
	Error     string
}

// PageData powers the ICP page.
type PageData struct {
	Items []Item
	Form  FormData
}
