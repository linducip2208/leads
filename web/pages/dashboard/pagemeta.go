package dashboard

import (
	"time"

	"leadforge/internal/webapp"
)

// PageMeta is the layout metadata passed from the web server.
type PageMeta = webapp.Page

func timeNow() time.Time { return time.Now() }
