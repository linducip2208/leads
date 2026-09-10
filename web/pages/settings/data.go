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

// APIKeyRow is one API key (hash never leaves the server).
type APIKeyRow struct {
	ID       string
	Name     string
	Prefix   string
	Scopes   string
	LastUsed string
	Expires  string
	Created  string
}

// APIKeysData powers the API keys page.
type APIKeysData struct {
	Keys    []APIKeyRow
	NewKey  string
	NewName string
	Error   string
}

// WebhookRow is one outbound webhook.
type WebhookRow struct {
	ID      string
	URL     string
	Events  string
	Active  bool
	Created string
}

// DeliveryRow is one delivery attempt.
type DeliveryRow struct {
	Event  string
	Status string
	Code   string
	When   string
	Error  string
}

// WebhooksData powers the webhooks page.
type WebhooksData struct {
	Hooks      []WebhookRow
	Deliveries []DeliveryRow
	Error      string
}
