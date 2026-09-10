package settings

import "leadforge/internal/webapp"

// PageMeta is layout metadata passed from the web server.
type PageMeta = webapp.Page

// WorkspaceData powers workspace settings.
type WorkspaceData struct {
	Name  string
	Slug  string
	Error string
}

// EmailAccountRow is one sending account.
type EmailAccountRow struct {
	ID        string
	Name      string
	FromEmail string
	Provider  string
	Daily     int
	Hourly    int
	Active    bool
	Status    string
	LastError string
}

// EmailAccountsData powers the email accounts page.
type EmailAccountsData struct {
	Accounts []EmailAccountRow
	Error    string
}
