// Package csrf implements the synchronizer-token pattern bound to the session.
package csrf

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

const fieldName = "_token"

// Manager issues and validates CSRF tokens bound to a session id.
type Manager struct {
	secret []byte
}

// New builds a CSRF manager from the app secret.
func New(secret string) *Manager {
	return &Manager{secret: []byte(secret)}
}

// Token returns (creating if needed) a CSRF token for the session.
// Token format: random-nonce.hmac(nonce, sessionID)
func (m *Manager) Token(sessionID string) string {
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	n := hex.EncodeToString(nonce[:])
	mac := m.mac(n, sessionID)
	return n + "." + mac
}

// Verify checks the submitted token against the session.
func (m *Manager) Verify(r *http.Request, sessionID string) bool {
	if err := r.ParseForm(); err != nil {
		return false
	}
	tok := r.PostFormValue(fieldName)
	if tok == "" {
		tok = r.Header.Get("X-CSRF-Token")
	}
	if tok == "" {
		return false
	}
	n, mac, ok := strings.Cut(tok, ".")
	if !ok || n == "" || mac == "" {
		return false
	}
	expect := m.mac(n, sessionID)
	return hmac.Equal([]byte(mac), []byte(expect))
}

func (m *Manager) mac(nonce, sessionID string) string {
	h := hmac.New(sha256.New, m.secret)
	h.Write([]byte(nonce))
	h.Write([]byte("|"))
	h.Write([]byte(sessionID))
	return hex.EncodeToString(h.Sum(nil))
}

// Field renders the hidden input for forms.
func Field(token string) string {
	if token == "" {
		return ""
	}
	return `<input type="hidden" name="` + fieldName + `" value="` + token + `">`
}

// SecureMethods lists methods that require CSRF validation.
func SecureMethods(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}
