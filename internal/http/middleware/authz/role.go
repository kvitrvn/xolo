package authz

import "github.com/xolo-gateway/xolo/internal/core/model"

// Aliases of the platform roles defined in the domain, kept for the existing
// call sites. model is the single source of truth.
const (
	RoleUser  = model.PlatformRoleUser
	RoleAdmin = model.PlatformRoleAdmin
)
