package service

import (
	"context"
	"log/slog"
	"regexp"
	"slices"
	"strings"

	"github.com/bornholm/go-x/slogx"
	"github.com/xolo-gateway/xolo/internal/core/model"
	"github.com/xolo-gateway/xolo/internal/core/port"
	"github.com/xolo-gateway/xolo/internal/core/rbac"
	"github.com/pkg/errors"
)

// ProvisioningService orchestrates the multi-store workflows needed to
// provision organizations ("tenants" in the machine-to-machine vocabulary),
// their members and their roles.
//
// It is the single place where the invariants spanning several stores are
// enforced: an organization always gets its builtin roles, it never loses its
// last owner, roles are never assigned across organizations, and provisioning a
// tenant administrator never grants platform-wide privileges.
type ProvisioningService struct {
	orgStore  port.OrgStore
	userStore port.UserStore
	roleStore port.RoleStore
}

func NewProvisioningService(orgStore port.OrgStore, userStore port.UserStore, roleStore port.RoleStore) *ProvisioningService {
	return &ProvisioningService{
		orgStore:  orgStore,
		userStore: userStore,
		roleStore: roleStore,
	}
}

// slugPattern matches a DNS-label-like identifier, the form external systems
// (Kubernetes operators, Terraform providers) can always produce.
var slugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

const maxSlugLength = 63

// UserIdentityParams describes a user by its authentication identity. The
// provider/subject tuple is required: it is the same key interactive
// authentication uses, so a provisioned user can log in afterwards. No
// email-only identity mechanism exists.
type UserIdentityParams struct {
	Provider    string
	Subject     string
	Email       *string
	DisplayName *string
	Active      *bool
}

type CreateTenantParams struct {
	Slug        string
	Name        string
	Description string
	Currency    string
	Active      *bool

	// Owner, when set, is provisioned and granted the builtin owner role of the
	// new tenant.
	Owner *UserIdentityParams
}

type CreateTenantResult struct {
	Org model.Organization

	// Owner and OwnerMembership are nil when no initial owner was requested.
	Owner           model.User
	OwnerMembership model.Membership

	// OwnerCreated reports whether the owner user was created by this call, as
	// opposed to an already known identity that was reused.
	OwnerCreated bool
}

// CreateTenant creates an organization, its builtin roles and, optionally, its
// initial owner.
//
// The stores expose no cross-store transaction, so any failure occurring after
// the organization row exists triggers a best-effort compensation: the
// organization is deleted (memberships cascade) to avoid leaving a half
// provisioned tenant behind. A pre-existing user is never deleted.
func (s *ProvisioningService) CreateTenant(ctx context.Context, params CreateTenantParams) (*CreateTenantResult, error) {
	if err := validateSlug(params.Slug); err != nil {
		return nil, errors.WithStack(err)
	}

	name := strings.TrimSpace(params.Name)
	if name == "" {
		return nil, errors.Wrap(port.ErrInvalid, "name is required")
	}

	currency, err := normalizeCurrency(params.Currency)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	if params.Owner != nil {
		if err := validateIdentity(*params.Owner); err != nil {
			return nil, errors.WithStack(err)
		}
	}

	// Explicit pre-check so the conflict carries the existing tenant identifier,
	// which is what an external reconciler needs. The unique constraint on the
	// slug remains the authoritative backstop.
	if existing, err := s.orgStore.GetOrgBySlug(ctx, params.Slug); err == nil {
		return nil, errors.Wrapf(port.ErrAlreadyExists, "tenant with slug %q already exists (id: %s)", params.Slug, existing.ID())
	} else if !errors.Is(err, port.ErrNotFound) {
		return nil, errors.WithStack(err)
	}

	org := model.NewOrganization(params.Slug, name, strings.TrimSpace(params.Description), currency)
	if params.Active != nil {
		org = model.UpdateOrganization(org, model.WithOrgActive(*params.Active))
	}

	if err := s.orgStore.CreateOrg(ctx, org); err != nil {
		return nil, errors.WithStack(err)
	}

	result, err := s.completeTenantCreation(ctx, org, params.Owner)
	if err != nil {
		s.rollbackTenant(ctx, org.ID())
		return nil, errors.WithStack(err)
	}

	return result, nil
}

// completeTenantCreation performs every step following the organization
// insertion. It is split out so the caller can compensate on any failure.
func (s *ProvisioningService) completeTenantCreation(ctx context.Context, org model.Organization, owner *UserIdentityParams) (*CreateTenantResult, error) {
	if err := s.roleStore.EnsureBuiltinRoles(ctx, org.ID()); err != nil {
		return nil, errors.WithStack(err)
	}

	result := &CreateTenantResult{Org: org}

	if owner == nil {
		return result, nil
	}

	user, created, err := s.ProvisionUser(ctx, *owner)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	result.Owner = user
	result.OwnerCreated = created

	ownerRole, err := s.resolveBuiltinRole(ctx, org.ID(), model.BuiltinKindOwner)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	membership := model.NewMembership(user.ID(), org.ID())
	if err := s.orgStore.AddMember(ctx, membership); err != nil {
		return nil, errors.WithStack(err)
	}

	if err := s.roleStore.SetMembershipRoles(ctx, membership.ID(), []model.RoleID{ownerRole.ID()}); err != nil {
		return nil, errors.WithStack(err)
	}

	stored, err := s.orgStore.GetMembership(ctx, membership.ID())
	if err != nil {
		return nil, errors.WithStack(err)
	}

	result.OwnerMembership = stored

	return result, nil
}

// rollbackTenant compensates a partially created tenant. Failures are logged
// rather than returned: the caller is already reporting the original error.
func (s *ProvisioningService) rollbackTenant(ctx context.Context, orgID model.OrgID) {
	if err := s.orgStore.DeleteOrg(ctx, orgID); err != nil {
		slog.ErrorContext(ctx, "could not rollback partially created tenant",
			slog.String("orgID", string(orgID)), slogx.Error(errors.WithStack(err)))
	}
}

func (s *ProvisioningService) GetTenant(ctx context.Context, orgID model.OrgID) (model.Organization, error) {
	org, err := s.orgStore.GetOrgByID(ctx, orgID)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	return org, nil
}

func (s *ProvisioningService) GetTenantBySlug(ctx context.Context, slug string) (model.Organization, error) {
	org, err := s.orgStore.GetOrgBySlug(ctx, slug)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	return org, nil
}

func (s *ProvisioningService) ListTenants(ctx context.Context, opts port.ListOrgsOptions) ([]model.Organization, int64, error) {
	orgs, total, err := s.orgStore.ListOrgs(ctx, opts)
	if err != nil {
		return nil, 0, errors.WithStack(err)
	}
	return orgs, total, nil
}

type UpdateTenantParams struct {
	Name              *string
	Description       *string
	Active            *bool
	Currency          *string
	ShareQuotaEqually *bool
}

// UpdateTenant applies the provided fields to an existing tenant. The slug is
// immutable: it is the stable handle external systems reconcile on.
func (s *ProvisioningService) UpdateTenant(ctx context.Context, orgID model.OrgID, params UpdateTenantParams) (model.Organization, error) {
	org, err := s.orgStore.GetOrgByID(ctx, orgID)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	opts := make([]model.OrgOption, 0, 5)

	if params.Name != nil {
		name := strings.TrimSpace(*params.Name)
		if name == "" {
			return nil, errors.Wrap(port.ErrInvalid, "name can not be empty")
		}
		opts = append(opts, model.WithOrgName(name))
	}
	if params.Description != nil {
		opts = append(opts, model.WithOrgDescription(strings.TrimSpace(*params.Description)))
	}
	if params.Active != nil {
		opts = append(opts, model.WithOrgActive(*params.Active))
	}
	if params.Currency != nil {
		currency, err := normalizeCurrency(*params.Currency)
		if err != nil {
			return nil, errors.WithStack(err)
		}
		opts = append(opts, model.WithOrgCurrency(currency))
	}
	if params.ShareQuotaEqually != nil {
		opts = append(opts, model.WithOrgShareQuotaEqually(*params.ShareQuotaEqually))
	}

	updated := model.UpdateOrganization(org, opts...)

	if err := s.orgStore.SaveOrg(ctx, updated); err != nil {
		return nil, errors.WithStack(err)
	}

	return updated, nil
}

func (s *ProvisioningService) DeleteTenant(ctx context.Context, orgID model.OrgID) error {
	if err := s.orgStore.DeleteOrg(ctx, orgID); err != nil {
		return errors.WithStack(err)
	}
	return nil
}

// ProvisionUser returns the user matching the provider/subject tuple, creating
// it if needed, and reports whether it was created.
//
// A newly created user receives exactly the "user" platform role: administering
// a tenant must never turn into administering the Xolo instance. The platform
// roles of an already known user are never modified here.
func (s *ProvisioningService) ProvisionUser(ctx context.Context, params UserIdentityParams) (model.User, bool, error) {
	if err := validateIdentity(params); err != nil {
		return nil, false, errors.WithStack(err)
	}

	existing, err := s.userStore.GetUserByIdentity(ctx, params.Provider, params.Subject)
	if err != nil && !errors.Is(err, port.ErrNotFound) {
		return nil, false, errors.WithStack(err)
	}

	if err == nil {
		updated, err := s.applyUserFields(ctx, existing, params.Email, params.DisplayName, params.Active)
		if err != nil {
			return nil, false, errors.WithStack(err)
		}
		return updated, false, nil
	}

	created, err := s.userStore.FindOrCreateUser(ctx, params.Provider, params.Subject)
	if err != nil {
		return nil, false, errors.WithStack(err)
	}

	user := model.CopyUser(created)

	// FindOrCreateUser may have found a user created concurrently between our
	// read and this call. Only a genuinely fresh user gets its platform roles
	// initialized, so an existing user never has its roles reset here.
	if len(user.Roles()) == 0 {
		user.SetRoles(model.PlatformRoleUser)
	}

	if params.Email != nil {
		user.SetEmail(strings.TrimSpace(*params.Email))
	}
	if params.DisplayName != nil {
		user.SetDisplayName(strings.TrimSpace(*params.DisplayName))
	}
	if params.Active != nil {
		user.SetActive(*params.Active)
	}

	if err := s.userStore.SaveUser(ctx, user); err != nil {
		// The user row already exists but holds none of the requested fields.
		// Drop it so a retry starts from a clean state.
		if deleteErr := s.userStore.DeleteUser(ctx, user.ID()); deleteErr != nil {
			slog.ErrorContext(ctx, "could not rollback partially created user",
				slog.String("userID", string(user.ID())), slogx.Error(errors.WithStack(deleteErr)))
		}
		return nil, false, errors.WithStack(err)
	}

	return user, true, nil
}

func (s *ProvisioningService) GetUser(ctx context.Context, userID model.UserID) (model.User, error) {
	user, err := s.userStore.GetUserByID(ctx, userID)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	return user, nil
}

func (s *ProvisioningService) FindUserByIdentity(ctx context.Context, provider, subject string) (model.User, error) {
	user, err := s.userStore.GetUserByIdentity(ctx, provider, subject)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	return user, nil
}

func (s *ProvisioningService) ListUsers(ctx context.Context, opts port.QueryUsersOptions) ([]model.User, int64, error) {
	users, err := s.userStore.QueryUsers(ctx, opts)
	if err != nil {
		return nil, 0, errors.WithStack(err)
	}

	total, err := s.userStore.CountUsers(ctx, opts)
	if err != nil {
		return nil, 0, errors.WithStack(err)
	}

	return users, total, nil
}

type UpdateUserParams struct {
	Email       *string
	DisplayName *string
	Active      *bool
}

// UpdateUser updates the profile fields of a user. Platform roles are
// deliberately not exposed: the Provisionning API never grants instance-wide privileges.
func (s *ProvisioningService) UpdateUser(ctx context.Context, userID model.UserID, params UpdateUserParams) (model.User, error) {
	user, err := s.userStore.GetUserByID(ctx, userID)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	updated, err := s.applyUserFields(ctx, user, params.Email, params.DisplayName, params.Active)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return updated, nil
}

// applyUserFields writes the provided fields on an existing user, leaving its
// platform roles untouched. It skips the write entirely when nothing changes.
func (s *ProvisioningService) applyUserFields(ctx context.Context, user model.User, email, displayName *string, active *bool) (model.User, error) {
	updated := model.CopyUser(user)
	changed := false

	if email != nil {
		if trimmed := strings.TrimSpace(*email); trimmed != updated.Email() {
			updated.SetEmail(trimmed)
			changed = true
		}
	}
	if displayName != nil {
		if trimmed := strings.TrimSpace(*displayName); trimmed != updated.DisplayName() {
			updated.SetDisplayName(trimmed)
			changed = true
		}
	}
	if active != nil && *active != updated.Active() {
		updated.SetActive(*active)
		changed = true
	}

	if !changed {
		return user, nil
	}

	if err := s.userStore.SaveUser(ctx, updated); err != nil {
		return nil, errors.WithStack(err)
	}

	return updated, nil
}

type AddMemberParams struct {
	// UserID targets an existing user. When empty, User is used to provision
	// the member's identity.
	UserID model.UserID
	User   *UserIdentityParams

	RoleIDs []model.RoleID
	// BuiltinRoles holds builtin role kinds ("owner", "admin", "member"),
	// letting a caller assign roles without first reading their identifiers.
	BuiltinRoles []string
}

// AddMember adds a user to a tenant and assigns its initial roles.
func (s *ProvisioningService) AddMember(ctx context.Context, orgID model.OrgID, params AddMemberParams) (model.Membership, error) {
	if _, err := s.orgStore.GetOrgByID(ctx, orgID); err != nil {
		return nil, errors.WithStack(err)
	}

	var (
		user model.User
		err  error
	)

	switch {
	case params.UserID != "":
		user, err = s.userStore.GetUserByID(ctx, params.UserID)
		if err != nil {
			return nil, errors.WithStack(err)
		}
	case params.User != nil:
		user, _, err = s.ProvisionUser(ctx, *params.User)
		if err != nil {
			return nil, errors.WithStack(err)
		}
	default:
		return nil, errors.Wrap(port.ErrInvalid, "either a user id or a user identity is required")
	}

	if existing, err := s.orgStore.GetUserOrgMembership(ctx, user.ID(), orgID); err == nil {
		return nil, errors.Wrapf(port.ErrAlreadyExists, "user is already a member of this tenant (membership id: %s)", existing.ID())
	} else if !errors.Is(err, port.ErrNotFound) {
		return nil, errors.WithStack(err)
	}

	roleIDs, err := s.resolveRoleIDs(ctx, orgID, params.RoleIDs, params.BuiltinRoles)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	membership := model.NewMembership(user.ID(), orgID)
	if err := s.orgStore.AddMember(ctx, membership); err != nil {
		return nil, errors.WithStack(err)
	}

	if len(roleIDs) > 0 {
		if err := s.roleStore.SetMembershipRoles(ctx, membership.ID(), roleIDs); err != nil {
			if removeErr := s.orgStore.RemoveMember(ctx, membership.ID()); removeErr != nil {
				slog.ErrorContext(ctx, "could not rollback partially created membership",
					slog.String("membershipID", string(membership.ID())), slogx.Error(errors.WithStack(removeErr)))
			}
			return nil, errors.WithStack(err)
		}
	}

	stored, err := s.orgStore.GetMembership(ctx, membership.ID())
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return stored, nil
}

func (s *ProvisioningService) ListMembers(ctx context.Context, orgID model.OrgID, opts port.ListOrgMembersOptions) ([]model.Membership, int64, error) {
	if _, err := s.orgStore.GetOrgByID(ctx, orgID); err != nil {
		return nil, 0, errors.WithStack(err)
	}

	members, total, err := s.orgStore.ListOrgMembers(ctx, orgID, opts)
	if err != nil {
		return nil, 0, errors.WithStack(err)
	}

	return members, total, nil
}

// GetMember returns a membership scoped to the given tenant. A membership
// belonging to another tenant is reported as not found: the Provisionning API must not
// let a caller probe another tenant's identifiers.
func (s *ProvisioningService) GetMember(ctx context.Context, orgID model.OrgID, membershipID model.MembershipID) (model.Membership, error) {
	membership, err := s.orgStore.GetMembership(ctx, membershipID)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	if membership.OrgID() != orgID {
		return nil, errors.Wrapf(port.ErrNotFound, "membership %q does not belong to tenant %q", membershipID, orgID)
	}

	return membership, nil
}

// SetMemberRoles fully replaces the roles of a membership. Every role must
// belong to the membership's tenant, and the tenant must keep at least one
// owner.
func (s *ProvisioningService) SetMemberRoles(ctx context.Context, orgID model.OrgID, membershipID model.MembershipID, roleIDs []model.RoleID, builtinRoles []string) (model.Membership, error) {
	membership, err := s.GetMember(ctx, orgID, membershipID)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	resolved, err := s.resolveRoleIDs(ctx, orgID, roleIDs, builtinRoles)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	if hasOwnerRole(membership.Roles()) && !slices.Contains(resolved, ownerRoleID(membership.Roles())) {
		if err := s.assertNotLastOwner(ctx, orgID, membershipID); err != nil {
			return nil, errors.WithStack(err)
		}
	}

	if err := s.roleStore.SetMembershipRoles(ctx, membershipID, resolved); err != nil {
		return nil, errors.WithStack(err)
	}

	updated, err := s.orgStore.GetMembership(ctx, membershipID)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return updated, nil
}

func (s *ProvisioningService) RemoveMember(ctx context.Context, orgID model.OrgID, membershipID model.MembershipID) error {
	membership, err := s.GetMember(ctx, orgID, membershipID)
	if err != nil {
		return errors.WithStack(err)
	}

	if hasOwnerRole(membership.Roles()) {
		if err := s.assertNotLastOwner(ctx, orgID, membershipID); err != nil {
			return errors.WithStack(err)
		}
	}

	if err := s.orgStore.RemoveMember(ctx, membershipID); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (s *ProvisioningService) ListRoles(ctx context.Context, orgID model.OrgID) ([]model.Role, error) {
	if _, err := s.orgStore.GetOrgByID(ctx, orgID); err != nil {
		return nil, errors.WithStack(err)
	}

	roles, err := s.roleStore.ListOrgRoles(ctx, orgID)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return roles, nil
}

// GetRole returns a role scoped to the given tenant. A role belonging to
// another tenant is reported as not found.
func (s *ProvisioningService) GetRole(ctx context.Context, orgID model.OrgID, roleID model.RoleID) (model.Role, error) {
	role, err := s.roleStore.GetRoleByID(ctx, roleID)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	if role.OrgID() != orgID {
		return nil, errors.Wrapf(port.ErrNotFound, "role %q does not belong to tenant %q", roleID, orgID)
	}

	return role, nil
}

type RoleParams struct {
	Name        *string
	Description *string
	Permissions []string
	ModelGrants []model.ModelGrant
}

func (s *ProvisioningService) CreateRole(ctx context.Context, orgID model.OrgID, params RoleParams) (model.Role, error) {
	if _, err := s.orgStore.GetOrgByID(ctx, orgID); err != nil {
		return nil, errors.WithStack(err)
	}

	if params.Name == nil || strings.TrimSpace(*params.Name) == "" {
		return nil, errors.Wrap(port.ErrInvalid, "name is required")
	}

	if err := validatePermissions(params.Permissions); err != nil {
		return nil, errors.WithStack(err)
	}
	if err := validateModelGrants(params.ModelGrants); err != nil {
		return nil, errors.WithStack(err)
	}

	description := ""
	if params.Description != nil {
		description = strings.TrimSpace(*params.Description)
	}

	role := model.NewRole(orgID, strings.TrimSpace(*params.Name), description)
	role.SetPermissions(params.Permissions)
	role.SetModelGrants(params.ModelGrants)

	if err := s.roleStore.CreateRole(ctx, role); err != nil {
		return nil, errors.WithStack(err)
	}

	return role, nil
}

// UpdateRole updates a custom role. Builtin roles are immutable: their
// permissions are part of the domain definition and the rest of the codebase
// relies on them.
func (s *ProvisioningService) UpdateRole(ctx context.Context, orgID model.OrgID, roleID model.RoleID, params RoleParams) (model.Role, error) {
	role, err := s.GetRole(ctx, orgID, roleID)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	if role.Builtin() {
		return nil, errors.Wrapf(port.ErrNotAllowed, "builtin role %q can not be modified", role.Name())
	}

	opts := make([]model.RoleOption, 0, 4)

	if params.Name != nil {
		name := strings.TrimSpace(*params.Name)
		if name == "" {
			return nil, errors.Wrap(port.ErrInvalid, "name can not be empty")
		}
		opts = append(opts, model.WithRoleName(name))
	}
	if params.Description != nil {
		opts = append(opts, model.WithRoleDescription(strings.TrimSpace(*params.Description)))
	}
	if params.Permissions != nil {
		if err := validatePermissions(params.Permissions); err != nil {
			return nil, errors.WithStack(err)
		}
		opts = append(opts, model.WithRolePermissions(params.Permissions))
	}
	if params.ModelGrants != nil {
		if err := validateModelGrants(params.ModelGrants); err != nil {
			return nil, errors.WithStack(err)
		}
		opts = append(opts, model.WithRoleModelGrants(params.ModelGrants))
	}

	updated := model.UpdateRole(role, opts...)

	if err := s.roleStore.SaveRole(ctx, updated); err != nil {
		return nil, errors.WithStack(err)
	}

	return updated, nil
}

func (s *ProvisioningService) DeleteRole(ctx context.Context, orgID model.OrgID, roleID model.RoleID) error {
	role, err := s.GetRole(ctx, orgID, roleID)
	if err != nil {
		return errors.WithStack(err)
	}

	if role.Builtin() {
		return errors.Wrapf(port.ErrNotAllowed, "builtin role %q can not be deleted", role.Name())
	}

	if err := s.roleStore.DeleteRole(ctx, roleID); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

// resolveRoleIDs merges explicit role identifiers and builtin role kinds into a
// deduplicated list, and verifies every resulting role belongs to orgID. This
// is what makes cross-tenant role assignment impossible.
func (s *ProvisioningService) resolveRoleIDs(ctx context.Context, orgID model.OrgID, roleIDs []model.RoleID, builtinKinds []string) ([]model.RoleID, error) {
	if len(roleIDs) == 0 && len(builtinKinds) == 0 {
		return nil, nil
	}

	orgRoles, err := s.roleStore.ListOrgRoles(ctx, orgID)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	byID := make(map[model.RoleID]struct{}, len(orgRoles))
	byKind := make(map[string]model.RoleID, len(orgRoles))
	for _, role := range orgRoles {
		byID[role.ID()] = struct{}{}
		if role.Builtin() {
			byKind[role.BuiltinKind()] = role.ID()
		}
	}

	seen := make(map[model.RoleID]struct{}, len(roleIDs)+len(builtinKinds))
	resolved := make([]model.RoleID, 0, len(roleIDs)+len(builtinKinds))

	appendRole := func(id model.RoleID) {
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		resolved = append(resolved, id)
	}

	for _, id := range roleIDs {
		if _, ok := byID[id]; !ok {
			return nil, errors.Wrapf(port.ErrInvalid, "role %q does not belong to tenant %q", id, orgID)
		}
		appendRole(id)
	}

	for _, kind := range builtinKinds {
		id, ok := byKind[kind]
		if !ok {
			return nil, errors.Wrapf(port.ErrInvalid, "unknown builtin role kind %q", kind)
		}
		appendRole(id)
	}

	return resolved, nil
}

func (s *ProvisioningService) resolveBuiltinRole(ctx context.Context, orgID model.OrgID, kind string) (model.Role, error) {
	roles, err := s.roleStore.ListOrgRoles(ctx, orgID)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	for _, role := range roles {
		if role.Builtin() && role.BuiltinKind() == kind {
			return role, nil
		}
	}

	return nil, errors.Wrapf(port.ErrNotFound, "no builtin %q role found for tenant %q", kind, orgID)
}

// assertNotLastOwner refuses an operation that would leave the tenant without
// any owner.
func (s *ProvisioningService) assertNotLastOwner(ctx context.Context, orgID model.OrgID, membershipID model.MembershipID) error {
	members, _, err := s.orgStore.ListOrgMembers(ctx, orgID, port.ListOrgMembersOptions{})
	if err != nil {
		return errors.WithStack(err)
	}

	if IsLastOwner(members, membershipID) {
		return errors.Wrap(port.ErrNotAllowed, "a tenant must keep at least one owner")
	}

	return nil
}

// IsLastOwner reports whether the excluded membership is the only one holding a
// builtin owner role among the given members.
func IsLastOwner(members []model.Membership, exclude model.MembershipID) bool {
	for _, member := range members {
		if member.ID() == exclude {
			continue
		}
		if hasOwnerRole(member.Roles()) {
			return false
		}
	}
	return true
}

func hasOwnerRole(roles []model.Role) bool {
	return ownerRoleID(roles) != ""
}

func ownerRoleID(roles []model.Role) model.RoleID {
	for _, role := range roles {
		if role.BuiltinKind() == model.BuiltinKindOwner {
			return role.ID()
		}
	}
	return ""
}

func validateSlug(slug string) error {
	if slug == "" {
		return errors.Wrap(port.ErrInvalid, "slug is required")
	}
	if len(slug) > maxSlugLength {
		return errors.Wrapf(port.ErrInvalid, "slug must be at most %d characters long", maxSlugLength)
	}
	if !slugPattern.MatchString(slug) {
		return errors.Wrap(port.ErrInvalid, "slug must only contain lowercase letters, digits and dashes, and must start and end with a letter or a digit")
	}
	return nil
}

func validateIdentity(params UserIdentityParams) error {
	if strings.TrimSpace(params.Provider) == "" {
		return errors.Wrap(port.ErrInvalid, "user provider is required")
	}
	if strings.TrimSpace(params.Subject) == "" {
		return errors.Wrap(port.ErrInvalid, "user subject is required")
	}
	return nil
}

func validatePermissions(codes []string) error {
	for _, code := range codes {
		if !rbac.IsKnown(code) {
			return errors.Wrapf(port.ErrInvalid, "unknown permission %q", code)
		}
	}
	return nil
}

func validateModelGrants(grants []model.ModelGrant) error {
	for _, grant := range grants {
		if grant.ModelID == "" {
			return errors.Wrap(port.ErrInvalid, "model grant requires a model id")
		}
		if grant.Kind != rbac.ModelKindLLM && grant.Kind != rbac.ModelKindVirtual {
			return errors.Wrapf(port.ErrInvalid, "unknown model grant kind %q", grant.Kind)
		}
	}
	return nil
}

// normalizeCurrency returns the currency to use, defaulting when empty and
// refusing anything outside the supported list.
func normalizeCurrency(currency string) (string, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		return model.DefaultCurrency, nil
	}
	if !slices.Contains(model.SupportedCurrencies, currency) {
		return "", errors.Wrapf(port.ErrInvalid, "unsupported currency %q", currency)
	}
	return currency, nil
}
