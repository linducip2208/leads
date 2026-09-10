package webapp

import (
	"net/http"

	"leadforge/internal/flash"
)

// Renderer holds common page metadata used by templ layouts.
type Renderer struct {
	AppName string
	Env     string
}

// Page is the metadata for a full page render.
type Page struct {
	Title       string
	ActiveNav   string
	Flash       []flash.Message
	CSRFToken   string
	IsSuper     bool
	SearchQuery string
	BrandName   string // tenant white-label override (empty = default)
}

// ErrorPageData renders friendly errors (never raw db errors).
type ErrorPageData struct {
	Code    int
	Title   string
	Message string
}

// HTTPError is an error with an HTTP status and a user-safe message.
type HTTPError struct {
	Status  int
	Title   string
	Message string
	Err     error // internal, logged only
}

func (e *HTTPError) Error() string { return e.Message }

// Common errors.
var (
	ErrNotFound  = &HTTPError{Status: 404, Title: "Page not found", Message: "The page you're looking for doesn't exist or has been moved."}
	ErrForbidden = &HTTPError{Status: 403, Title: "No access", Message: "You don't have permission to do that."}
)

// WriteError renders a friendly error page or JSON for API requests.
func WriteError(w http.ResponseWriter, r *http.Request, he *HTTPError, render func(w http.ResponseWriter, r *http.Request, d ErrorPageData)) {
	if he.Err != nil {
		// internal detail is logged by caller middleware; not exposed
		_ = he.Err
	}
	w.WriteHeader(he.Status)
	if render != nil {
		render(w, r, ErrorPageData{Code: he.Status, Title: he.Title, Message: he.Message})
	}
}

// Redirect with a flash message helper.
func RedirectFlash(w http.ResponseWriter, r *http.Request, url, kind, msg string) {
	if msg != "" {
		flash.Set(w, kind, msg)
	}
	http.Redirect(w, r, url, http.StatusSeeOther)
}
