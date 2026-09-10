// Package session provides secure cookie-backed server sessions stored in postgres.
package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const cookieName = "leadforge_session"

// Manager creates, loads and destroys sessions.
type Manager struct {
	pool   *pgxpool.Pool
	secret string
	ttl    time.Duration
	secure bool
}

// NewManager builds a session manager. In production Secure cookies are enforced.
func NewManager(pool *pgxpool.Pool, secret string, ttl time.Duration, isProd bool) *Manager {
	return &Manager{pool: pool, secret: secret, ttl: ttl, secure: isProd}
}

type row struct {
	ID       string
	UserID   string
	TenantID *string
}

// Start creates a new session for the user and sets the cookie.
func (m *Manager) Start(w http.ResponseWriter, r *http.Request, userID, tenantID string) error {
	token := randomToken()
	id := hashToken(token, m.secret)
	_, err := m.pool.Exec(r.Context(), `
		INSERT INTO sessions (id, user_id, tenant_id, ip, user_agent, expires_at)
		VALUES ($1,$2,NULLIF($3,'')::uuid,$4,$5, now() + make_interval(secs => $6))`,
		id, userID, tenantID, clientIP(r), r.UserAgent(), int64(m.ttl.Seconds()))
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(m.ttl),
	})
	return nil
}

// Data is the authenticated identity resolved from a request.
type Data struct {
	UserID   string
	TenantID string
}

// Get resolves the session from the request cookie. Returns nil when anonymous.
func (m *Manager) Get(ctx context.Context, r *http.Request) (*Data, error) {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return nil, nil
	}
	id := hashToken(c.Value, m.secret)
	var d Data
	err = m.pool.QueryRow(ctx, `
		SELECT s.user_id::text, COALESCE(s.tenant_id::text,'')
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.id = $1 AND s.expires_at > now() AND u.status = 'active'`,
		id).Scan(&d.UserID, &d.TenantID)
	if err != nil {
		return nil, nil // treat any error as anonymous
	}
	// sliding refresh
	_, _ = m.pool.Exec(ctx, `UPDATE sessions SET last_seen_at = now() WHERE id = $1 AND expires_at - now() < make_interval(days => 7)`, id)
	return &d, nil
}

// Destroy removes the session and clears the cookie.
func (m *Manager) Destroy(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		id := hashToken(c.Value, m.secret)
		_, _ = m.pool.Exec(r.Context(), `DELETE FROM sessions WHERE id = $1`, id)
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", HttpOnly: true,
		Secure: m.secure, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

// VerifyTokenConstantTime is used for signed values like CSRF.
func VerifyTokenConstantTime(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func hashToken(token, secret string) string {
	h := sha256.Sum256([]byte(secret + ":" + token))
	return hex.EncodeToString(h[:])
}

func randomToken() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func clientIP(r *http.Request) string {
	return r.Header.Get("X-Forwarded-For")
}

// Sweep removes expired sessions; called by scheduler.
func (m *Manager) Sweep(ctx context.Context) {
	_, _ = m.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now() - interval '7 days'`)
	_, _ = m.pool.Exec(ctx, `DELETE FROM password_reset_tokens WHERE expires_at < now()`)
	_, _ = m.pool.Exec(ctx, `DELETE FROM email_verification_tokens WHERE expires_at < now()`)
}
