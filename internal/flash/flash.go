// Package flash provides one-shot flash messages stored in a signed cookie.
package flash

import (
	"encoding/json"
	"net/http"
)

const cookieName = "leadforge_flash"

// Kind levels.
const (
	Success = "success"
	Error   = "error"
	Info    = "info"
	Warning = "warning"
)

// Message is a flash notification.
type Message struct {
	Kind string `json:"k"`
	Text string `json:"t"`
}

// Set stores a flash message on the response.
func Set(w http.ResponseWriter, kind, text string) {
	b, _ := json.Marshal([]Message{{Kind: kind, Text: text}})
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: string(b), Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 60,
	})
}

// Pop reads and clears flash messages from the request.
func Pop(r *http.Request) []Message {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	var msgs []Message
	if err := json.Unmarshal([]byte(c.Value), &msgs); err != nil {
		return nil
	}
	return msgs
}

// ClearCookie emits an expiring flash cookie (call on GET render).
func ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
	})
}
