// Package auth implements registration, login, password reset and email
// verification with argon2id password hashing.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/alexedwards/argon2id"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadforge/internal/role"
)

var (
	ErrInvalidCredentials = errors.New("email atau password salah")
	ErrEmailTaken         = errors.New("email sudah terdaftar")
	ErrUserSuspended      = errors.New("akun dinonaktifkan")
	ErrWeakPassword       = errors.New("password minimal 8 karakter")
	ErrInvalidToken       = errors.New("token tidak valid atau kedaluwarsa")
)

// User is the auth-facing user record.
type User struct {
	ID            string
	TenantID      string
	Email         string
	Name          string
	IsSuperAdmin  bool
	EmailVerified bool
	Status        string
}

// Service provides auth use-cases.
type Service struct {
	pool *pgxpool.Pool
}

// NewService builds an auth service.
func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// Register creates a tenant + owner user atomically (used at signup).
func (s *Service) Register(ctx context.Context, name, email, password, tenantName string) (*User, error) {
	if len(password) < 8 {
		return nil, ErrWeakPassword
	}
	email = strings.ToLower(strings.TrimSpace(email))
	hash, err := argon2id.CreateHash(password, argon2id.DefaultParams)
	if err != nil {
		return nil, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var tenantID, userID string
	slug := Slugify(tenantName)
	// ensure unique slug
	for i := 0; i < 20; i++ {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tenants WHERE slug=$1)`, slug).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			break
		}
		slug = Slugify(tenantName) + "-" + randomSuffix()
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO tenants (name, slug)
		VALUES ($1, $2)
		RETURNING id`, tenantName, slug).Scan(&tenantID)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO users (tenant_id, email, password_hash, name, status)
		VALUES ($1, $2, $3, $4, 'active')
		RETURNING id`, tenantID, email, hash, name).Scan(&userID)
	if err != nil {
		return nil, translatePG(err)
	}
	// system roles are global templates with tenant_id NULL; attach owner role
	if _, err = tx.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id)
		SELECT $1, r.id FROM roles r WHERE r.tenant_id IS NULL AND r.slug = 'owner'`, userID); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &User{ID: userID, TenantID: tenantID, Email: email, Name: name, Status: "active"}, nil
}

// Login verifies credentials and returns the user.
func (s *Service) Login(ctx context.Context, email, password string) (*User, error) {
	var u User
	var hash string
	err := s.pool.QueryRow(ctx, `
		SELECT id, COALESCE(tenant_id::text,''), email, name, password_hash, is_super_admin, status
		FROM users WHERE email = $1`, strings.ToLower(strings.TrimSpace(email))).
		Scan(&u.ID, &u.TenantID, &u.Email, &u.Name, &hash, &u.IsSuperAdmin, &u.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	ok, _ := argon2id.ComparePasswordAndHash(password, hash)
	if !ok {
		return nil, ErrInvalidCredentials
	}
	if u.Status != "active" {
		return nil, ErrUserSuspended
	}
	_, _ = s.pool.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, u.ID)
	return &u, nil
}

// GetUser loads a user by id.
func (s *Service) GetUser(ctx context.Context, id string) (*User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT id, COALESCE(tenant_id::text,''), email, name, is_super_admin, status
		FROM users WHERE id = $1`, id).
		Scan(&u.ID, &u.TenantID, &u.Email, &u.Name, &u.IsSuperAdmin, &u.Status)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// CreatePasswordReset issues a reset token valid for 1 hour.
func (s *Service) CreatePasswordReset(ctx context.Context, email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE email=$1)`, email).Scan(&exists); err != nil {
		return "", err
	}
	if !exists {
		// do not leak account existence
		return "", nil
	}
	token := randomToken()
	hash := HashToken(token)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO password_reset_tokens (email, token_hash, expires_at)
		VALUES ($1,$2, now() + interval '1 hour')`, email, hash)
	return token, err
}

// ResetPassword consumes the token and sets the new password.
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	if len(newPassword) < 8 {
		return ErrWeakPassword
	}
	var email string
	err := s.pool.QueryRow(ctx, `
		UPDATE password_reset_tokens SET expires_at = now() - interval '1 second'
		WHERE token_hash = $1 AND expires_at > now()
		RETURNING email`, HashToken(token)).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidToken
	}
	if err != nil {
		return err
	}
	hash, err := argon2id.CreateHash(newPassword, argon2id.DefaultParams)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `UPDATE users SET password_hash = $1 WHERE email = $2`, hash, email)
	return err
}

// MarkEmailVerified consumes a verification token.
func (s *Service) MarkEmailVerified(ctx context.Context, token string) error {
	var userID string
	err := s.pool.QueryRow(ctx, `
		UPDATE email_verification_tokens SET expires_at = now() - interval '1 second'
		WHERE token_hash = $1 AND expires_at > now()
		RETURNING user_id::text`, HashToken(token)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidToken
	}
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `UPDATE users SET email_verified_at = now() WHERE id = $1`, userID)
	return err
}

// CreateEmailVerification issues a verification token for the user.
func (s *Service) CreateEmailVerification(ctx context.Context, userID string) (string, error) {
	token := randomToken()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO email_verification_tokens (user_id, token_hash, expires_at)
		VALUES ($1,$2, now() + interval '24 hours')`, userID, HashToken(token))
	return token, err
}

// Permissions loads role permissions for the user.
func (s *Service) Permissions(ctx context.Context, u *User) (role.Permissions, error) {
	if u.IsSuperAdmin {
		return role.Permissions{"*": true}, nil
	}
	perms := role.Permissions{}
	if u.TenantID == "" {
		return perms, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT p.slug
		FROM user_roles ur
		JOIN role_permissions rp ON rp.role_id = ur.role_id
		JOIN permissions p ON p.id = rp.permission_id
		WHERE ur.user_id = $1`, u.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, err
		}
		perms[slug] = true
	}
	return perms, rows.Err()
}

// EnsureSystemRoles inserts global role templates + permission links (idempotent).
func (s *Service) EnsureSystemRoles(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for slug, name := range map[string]string{
		role.RoleSuperAdmin:   "Super Admin",
		role.RoleOwner:        "Owner",
		role.RoleAdmin:        "Admin",
		role.RoleSalesManager: "Sales Manager",
		role.RoleSales:        "Sales",
		role.RoleResearcher:   "Researcher",
		role.RoleViewer:       "Viewer",
	} {
		var id string
		// NOTE: tenant_id IS NULL never conflicts in Postgres, so check-then-insert.
		err := tx.QueryRow(ctx, `SELECT id::text FROM roles WHERE tenant_id IS NULL AND slug=$1`, slug).Scan(&id)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err := tx.QueryRow(ctx, `
				INSERT INTO roles (tenant_id, slug, name, is_system)
				VALUES (NULL, $1, $2, true)
				ON CONFLICT DO NOTHING RETURNING id::text`, slug, name).Scan(&id); err != nil {
				if !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
				// raced insert won; re-read
				if err := tx.QueryRow(ctx, `SELECT id::text FROM roles WHERE tenant_id IS NULL AND slug=$1`, slug).Scan(&id); err != nil {
					return err
				}
			}
		} else {
			_, _ = tx.Exec(ctx, `UPDATE roles SET name=$2 WHERE id=$1`, id, name)
		}
		perms := role.RoleTemplates[slug]
		for _, p := range perms {
			if _, err := tx.Exec(ctx, `
				INSERT INTO role_permissions (role_id, permission_id)
				SELECT $1, id FROM permissions WHERE slug = $2
				ON CONFLICT DO NOTHING`, id, p); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

func translatePG(err error) error {
	if err != nil && strings.Contains(err.Error(), "duplicate key") && strings.Contains(err.Error(), "users_email") {
		return ErrEmailTaken
	}
	return err
}

// Slugify converts a name into a url-safe slug.
func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := true
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "workspace"
	}
	if len(out) > 48 {
		out = out[:48]
	}
	return out
}

func randomSuffix() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func randomToken() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
