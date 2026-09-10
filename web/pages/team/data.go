package team

import "leadforge/internal/webapp"

// PageMeta is layout metadata passed from the web server.
type PageMeta = webapp.Page

// Member is one workspace user.
type Member struct {
	ID     string
	Name   string
	Email  string
	Status string
	Roles  string
	Joined string
}

// MembersData powers the members page.
type MembersData struct {
	Members []Member
	Roles   []Option
	Error   string
}

// Option is a select option.
type Option struct {
	Value string
	Label string
}

// RoleRow is one role with its permissions.
type RoleRow struct {
	ID      string
	Slug    string
	Name    string
	System  bool
	Members int
	Perms   []string
}

// RolesData powers the roles page.
type RolesData struct {
	Roles []RoleRow
	Perms []string
	Error string
}
