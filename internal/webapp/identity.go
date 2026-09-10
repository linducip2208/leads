// Package webapp carries per-request identity (user, tenant, permissions).
package webapp

import (
	"context"

	"leadforge/internal/role"
)

type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxTenant
	ctxIsSuper
)

// Identity is the authenticated user within a tenant workspace.
type Identity struct {
	UserID       string
	TenantID     string
	Name         string
	Email        string
	IsSuperAdmin bool
	Perms        role.Permissions
}

// Can reports permission.
func (id *Identity) Can(perm string) bool {
	if id == nil {
		return false
	}
	return id.Perms.Has(perm)
}

// WithIdentity stores identity in context.
func WithIdentity(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, ctxUser, id)
}

// IdentityFrom retrieves identity from context.
func IdentityFrom(ctx context.Context) *Identity {
	if v, ok := ctx.Value(ctxUser).(*Identity); ok {
		return v
	}
	return nil
}
