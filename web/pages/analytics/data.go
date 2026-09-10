package analytics

import "leadforge/internal/webapp"

// PageMeta is layout metadata passed from the web server.
type PageMeta = webapp.Page

// Bar is a labeled horizontal bar row.
type Bar struct {
	Label string
	Value string
	Count int
	Pct   float64
}

// DayPoint is a per-day count.
type DayPoint struct {
	Label string
	Count int
}

// Stat is a KPI tile.
type Stat struct {
	Label string
	Value string
}

// PageData powers analytics pages.
type PageData struct {
	Title string
	Stats []Stat
	Bars  []Bar
	Days  []DayPoint
	Note  string
}
