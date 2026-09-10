// Package role implements permission constants, role templates and permission checks.
package role

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Permission slugs.
const (
	TenantManage     = "tenant.manage"
	TeamManage       = "team.manage"
	BillingManage    = "billing.manage"
	IntegrationMange = "integration.manage"
	LeadRead         = "lead.read"
	LeadCreate       = "lead.create"
	LeadUpdate       = "lead.update"
	LeadDelete       = "lead.delete"
	LeadExport       = "lead.export"
	SearchCreate     = "search.create"
	SearchManage     = "search.manage"
	SegmentManage    = "segment.manage"
	CampaignCreate   = "campaign.create"
	CampaignLaunch   = "campaign.launch"
	DealManage       = "deal.manage"
	TaskManage       = "task.manage"
	AIUse            = "ai.use"
	AdminPlatform    = "admin.platform"
)

// System role slugs.
const (
	RoleSuperAdmin   = "super_admin"
	RoleOwner        = "owner"
	RoleAdmin        = "admin"
	RoleSalesManager = "sales_manager"
	RoleSales        = "sales"
	RoleResearcher   = "researcher"
	RoleViewer       = "viewer"
)

// RoleTemplates maps system roles to their permission sets.
var RoleTemplates = map[string][]string{
	RoleSuperAdmin: {"*"},
	RoleOwner: {
		TenantManage, TeamManage, BillingManage, IntegrationMange,
		LeadRead, LeadCreate, LeadUpdate, LeadDelete, LeadExport,
		SearchCreate, SearchManage, SegmentManage,
		CampaignCreate, CampaignLaunch, DealManage, TaskManage, AIUse,
	},
	RoleAdmin: {
		TeamManage, IntegrationMange,
		LeadRead, LeadCreate, LeadUpdate, LeadDelete, LeadExport,
		SearchCreate, SearchManage, SegmentManage,
		CampaignCreate, CampaignLaunch, DealManage, TaskManage, AIUse,
	},
	RoleSalesManager: {
		LeadRead, LeadCreate, LeadUpdate, LeadExport,
		SearchCreate, SearchManage, SegmentManage,
		CampaignCreate, CampaignLaunch, DealManage, TaskManage,
	},
	RoleSales: {
		LeadRead, LeadCreate, LeadUpdate, LeadExport,
		SearchCreate, SegmentManage, CampaignCreate, DealManage, TaskManage,
	},
	RoleResearcher: {
		LeadRead, LeadCreate, LeadUpdate, SearchCreate, SearchManage, SegmentManage,
	},
	RoleViewer: {
		LeadRead,
	},
}

// Service resolves user permissions with caching in-process.
type Service struct {
	pool *pgxpool.Pool
}

// NewService builds a role service.
func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

type ctxKey int

const ctxPerms ctxKey = iota

// Permissions is the resolved permission set for the request user.
type Permissions map[string]bool

// Has reports whether the permission is granted ("*" grants all).
func (p Permissions) Has(perm string) bool {
	return p["*"] || p[perm]
}

// Load fetches permission slugs for the user (union of role permissions).
func (s *Service) Load(ctx context.Context, userID string) (Permissions, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT p.slug
		FROM user_roles ur
		JOIN role_permissions rp ON rp.role_id = ur.role_id
		JOIN permissions p ON p.id = rp.permission_id
		WHERE ur.user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	perms := Permissions{}
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, err
		}
		perms[slug] = true
	}
	return perms, rows.Err()
}

// LoadSuperAdmin checks the is_super_admin flag directly.
func (s *Service) LoadSuperAdmin(ctx context.Context, userID string) (bool, error) {
	var isSuper bool
	err := s.pool.QueryRow(ctx, `SELECT is_super_admin FROM users WHERE id = $1`, userID).Scan(&isSuper)
	return isSuper, err
}

// WithContext stores permissions in context.
func WithContext(ctx context.Context, p Permissions) context.Context {
	return context.WithValue(ctx, ctxPerms, p)
}

// FromContext retrieves permissions from context (nil-safe).
func FromContext(ctx context.Context) Permissions {
	if p, ok := ctx.Value(ctxPerms).(Permissions); ok {
		return p
	}
	return Permissions{}
}
