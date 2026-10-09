package gorm

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/xolo-gateway/xolo/internal/core/model"
	"github.com/xolo-gateway/xolo/internal/core/port"
	"github.com/xolo-gateway/xolo/internal/deletion"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ResourceDeletion records the deletion of a tenant, an organization or a
// member. While it exists, the database guards refuse every write to the
// scope of the resource.
type ResourceDeletion struct {
	Family     string `gorm:"primaryKey"`
	ResourceID string `gorm:"primaryKey"`
	TenantID   string `gorm:"index;not null"`
	DeletedAt  time.Time
	PurgeAfter time.Time `gorm:"index"`
	// ConfirmedAt and ExportSHA256 record the confirmation of the export: the
	// deletion may be purged once PurgeAfter has passed.
	ConfirmedAt  *time.Time
	ExportSHA256 string `gorm:"not null;default:''"`
	// PurgedAt is set once the scope is purged. The deletion is kept: its
	// identifier stays retired, and the guards keep refusing to reuse it.
	PurgedAt *time.Time `gorm:"index"`
	// PurgedWith designates the deletion of the parent this resource was
	// purged with.
	PurgedWith string `gorm:"not null;default:''"`
	Attempts   int    `gorm:"not null;default:0"`
	Diagnostic string `gorm:"not null;default:''"`
	// Purging is only ever true within a purge transaction, which sets it
	// back before committing: the guards let that transaction remove the
	// frozen rows, and no other ever sees it set.
	Purging bool `gorm:"not null;default:false"`
}

// LifecycleControl remembers whether the guards are installed.
type LifecycleControl struct {
	ID              int `gorm:"primaryKey;autoIncrement:false"`
	GuardsInstalled bool
}

const lifecycleControlID = 1

func (d ResourceDeletion) view() model.Deletion {
	return model.Deletion{
		Family: d.Family, TenantID: d.TenantID, ResourceID: d.ResourceID,
		DeletedAt: d.DeletedAt.UTC(), PurgeAfter: d.PurgeAfter.UTC(),
		ExportSHA256: d.ExportSHA256, ConfirmedAt: utcTime(d.ConfirmedAt), PurgedAt: utcTime(d.PurgedAt),
		PurgedWith: d.PurgedWith, Attempts: d.Attempts, Diagnostic: d.Diagnostic,
	}
}

func utcTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	utc := t.UTC()
	return &utc
}

// WithLifecycle allows recording deletions, kept retention before their
// purge. Without it, FreezeResource refuses with ErrLifecycleDisabled.
func WithLifecycle(enabled bool, retention time.Duration) StoreOption {
	return func(opts *storeOptions) {
		if enabled {
			opts.lifecycleRetention = retention
		}
	}
}

// lifecycleTarget designates the row a freeze locks and deactivates.
var lifecycleTargets = map[string]struct{ kind, table string }{
	model.FamilyTenant:       {"tenant", "tenants"},
	model.FamilyOrganization: {"organization", "organizations"},
	model.FamilyMember:       {"user", "users"},
}

// FreezeResource implements port.LifecycleStore. In one provisioning
// transaction, it locks the row of the resource, deactivates it — which
// publishes its suspension — and records its deletion. Every family the
// scope holds must belong to the authority of the caller.
func (s *Store) FreezeResource(ctx context.Context, scope model.CommonScope, key string, condition model.MatchCondition) (model.Deletion, error) {
	var out model.Deletion
	target, ok := lifecycleTargets[scope.Family]
	if !ok {
		return out, errors.WithStack(port.ErrInvalid)
	}
	if err := validateCommonScope(scope, key); err != nil || key == "" {
		return out, errors.WithStack(port.ErrInvalid)
	}
	if s.lifecycleRetention <= 0 {
		return out, errors.WithStack(port.ErrLifecycleDisabled)
	}
	tenantID := scope.TenantID
	if scope.Family == model.FamilyTenant {
		tenantID = key
	}
	err := s.WithProvisioningTransaction(ctx, func(ptx port.ProvisioningTx) error {
		tx := ptx.(*provisioningTx)
		db := tx.db
		q := db.Table(target.table).Where("id = ?", key)
		if scope.Family != model.FamilyTenant {
			q = q.Where("tenant_id = ?", tenantID)
		}
		if isPostgres(db) {
			// Waits for the writes of the scope in progress, which lock this
			// row FOR KEY SHARE, and holds the next ones until commit.
			q = q.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var ids []string
		if err := q.Pluck("id", &ids).Error; err != nil {
			return errors.WithStack(err)
		}
		if len(ids) == 0 {
			return errors.WithStack(port.ErrNotFound)
		}
		if scope.Family == model.FamilyTenant {
			var slugs []string
			if err := db.Table("tenants").Where("id = ?", key).Pluck("slug", &slugs).Error; err != nil {
				return errors.WithStack(err)
			}
			// The default tenant serves the single-tenant instance: it is
			// never deleted.
			if len(slugs) == 1 && slugs[0] == model.DefaultTenantSlug {
				return errors.Wrap(port.ErrNotAllowed, "the default tenant can not be deleted")
			}
		}

		// A repeated freeze returns the recorded deletion before the
		// condition: the first one published the suspension, which moved the
		// ETag a retried request still carries.
		var existing []ResourceDeletion
		if err := db.Where("family = ? AND resource_id = ?", scope.Family, key).Limit(1).Find(&existing).Error; err != nil {
			return errors.WithStack(err)
		}
		if len(existing) == 1 {
			return nil
		}
		item, err := tx.ReadProjection(ctx, scope, key)
		if err != nil && !errors.Is(err, port.ErrNotFound) {
			return err
		}
		if !condition.Matches(item.ETag) {
			return errors.WithStack(port.ErrPreconditionFailed)
		}
		if err := frozenParents(db, scope, tenantID); err != nil {
			return err
		}
		if scope.Family == model.FamilyMember {
			if err := lastOrganizationOwner(db, key); err != nil {
				return err
			}
		}
		if err := platformAdmins(db, scope.Family, key); err != nil {
			return err
		}
		if err := tx.authorizeFreeze(ctx, scope.Family, key, tenantID); err != nil {
			return err
		}

		// Deactivated while still writable: the projection publishes the
		// suspension, and the sign-ins and requests of the scope stop.
		if err := tx.deactivate(ctx, scope.Family, key); err != nil {
			return err
		}
		// A member keeps its OIDC sessions: they are keyed by the issuer of
		// the sign-in, which its row does not hold. Deactivated, the member is
		// refused on every request all the same (authz.Active, token login).
		if scope.Family == model.FamilyTenant {
			if err := db.Where("tenant_id = ?", key).Delete(&OIDCSession{}).Error; err != nil {
				return errors.WithStack(err)
			}
		}

		// Truncated to the precision both backends store.
		now := db.NowFunc().UTC().Truncate(time.Microsecond)
		deletion := ResourceDeletion{Family: scope.Family, ResourceID: key, TenantID: tenantID, DeletedAt: now, PurgeAfter: now.Add(s.lifecycleRetention)}
		if err := db.Create(&deletion).Error; err != nil {
			return errors.WithStack(err)
		}
		if err := auditFreeze(ctx, db, deletion); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	// Read after commit: the suspension published at commit gives the ETag.
	return s.ReadDeletion(ctx, scope, key)
}

// deactivate suspends the resource through the bound stores, so that its
// projection and the last-owner checks follow.
func (tx *provisioningTx) deactivate(ctx context.Context, family, key string) error {
	switch family {
	case model.FamilyTenant:
		tenant, err := tx.GetTenantByID(ctx, model.TenantID(key))
		if err != nil {
			return err
		}
		return tx.SaveTenant(ctx, model.UpdateTenant(tenant, model.WithTenantActive(false)))
	case model.FamilyOrganization:
		org, err := tx.GetOrgByID(ctx, model.OrgID(key))
		if err != nil {
			return err
		}
		return tx.SaveOrg(ctx, model.UpdateOrganization(org, model.WithOrgActive(false)))
	default:
		user, err := tx.GetUserByID(ctx, model.UserID(key))
		if err != nil {
			return err
		}
		next := model.CopyUser(user)
		next.SetActive(false)
		return tx.SaveUser(ctx, next)
	}
}

// frozenParents refuses to freeze a resource whose tenant or organization is
// already frozen: its scope already is.
// A member of a frozen organization stays a member of its tenant: only the
// tenant holds it.
func frozenParents(db *gorm.DB, scope model.CommonScope, tenantID string) error {
	if scope.Family == model.FamilyTenant {
		return nil
	}
	var n int64
	if err := db.Model(&ResourceDeletion{}).Where("family = ? AND resource_id = ?", model.FamilyTenant, tenantID).Count(&n).Error; err != nil {
		return errors.WithStack(err)
	}
	if n > 0 {
		return errors.WithStack(port.ErrResourceDeleted)
	}
	return nil
}

// platformAdmins refuses to freeze a platform administrator, or a tenant
// holding one: provisioning never acts on platform-wide privileges, and a
// purge would remove the account.
func platformAdmins(db *gorm.DB, family, key string) error {
	column := "users.id"
	switch family {
	case model.FamilyTenant:
		column = "users.tenant_id"
	case model.FamilyOrganization:
		return nil
	}
	var n int64
	if err := db.Table("users").Joins("JOIN user_roles ur ON ur.user_id = users.id").
		Where(column+" = ? AND ur.role = ?", key, model.PlatformRoleAdmin).Count(&n).Error; err != nil {
		return errors.WithStack(err)
	}
	if n > 0 {
		return errors.WithStack(port.ErrPlatformAdminProtected)
	}
	return nil
}

// lastOrganizationOwner refuses to freeze the last active owner of an
// organization still alive. An inactive user is no active owner: the
// organizations it owns already have none. The tenant owner is checked by
// the deactivation.
func lastOrganizationOwner(db *gorm.DB, userID string) error {
	var orgs []string
	if err := db.Table("memberships").
		Joins("JOIN membership_roles mr ON mr.membership_id = memberships.id").
		Joins("JOIN roles ON roles.id = mr.role_id").
		Joins("JOIN users ON users.id = memberships.user_id").
		Where("memberships.user_id = ? AND memberships.status = ? AND roles.builtin_kind = ? AND users.active = ?", userID, string(model.StatusActive), model.BuiltinKindOwner, true).
		Where("NOT EXISTS (SELECT 1 FROM resource_deletions d WHERE d.family = ? AND d.resource_id = memberships.org_id)", model.FamilyOrganization).
		Pluck("memberships.org_id", &orgs).Error; err != nil {
		return errors.WithStack(err)
	}
	for _, org := range orgs {
		var owners int64
		if err := db.Table("memberships").
			Joins("JOIN membership_roles mr ON mr.membership_id = memberships.id").
			Joins("JOIN roles ON roles.id = mr.role_id").
			Joins("JOIN users ON users.id = memberships.user_id").
			Where("memberships.org_id = ? AND memberships.user_id <> ? AND memberships.status = ? AND roles.builtin_kind = ? AND users.active = ?", org, userID, string(model.StatusActive), model.BuiltinKindOwner, true).
			Count(&owners).Error; err != nil {
			return errors.WithStack(err)
		}
		if owners == 0 {
			return errors.Wrapf(port.ErrLastOwner, "organization %s", org)
		}
	}
	return nil
}

// authorizeFreeze checks the authority of the caller over every family the
// scope holds: freezing it forbids their writes. The whole freeze is refused
// otherwise.
func (tx *provisioningTx) authorizeFreeze(ctx context.Context, family, key, tenantID string) error {
	deletion := ResourceDeletion{Family: family, ResourceID: key, TenantID: tenantID}
	for _, t := range lifecycleTables {
		if t.family == "" {
			continue
		}
		var n int64
		if err := scopeRows(tx.db, t.table, deletion).Limit(1).Count(&n).Error; err != nil {
			return errors.WithStack(err)
		}
		if n == 0 {
			continue
		}
		if err := tx.checkOwnership(ctx, t.family); err != nil {
			return err
		}
	}
	return nil
}

// scopeRows selects the rows of an inventory table in the scope of a
// deletion, which need not be recorded yet.
func scopeRows(db *gorm.DB, table string, d ResourceDeletion) *gorm.DB {
	t := lifecycleTableOf(table)
	var predicate string
	switch d.Family {
	case model.FamilyTenant:
		predicate = t.tenantExpression("r") + " = ?"
	case model.FamilyOrganization:
		predicate = t.expression(t.org, "r") + " = ?"
	default:
		predicate = t.expression(t.member, "r") + " = ?"
	}
	q := db.Table(table+" r").Where(predicate, d.ResourceID)
	if table == "roles" {
		q = q.Where("r.builtin = ?", false)
	}
	return q
}

func auditFreeze(ctx context.Context, db *gorm.DB, d ResourceDeletion) error {
	actor := model.ActorFromContext(ctx)
	actorJSON, err := json.Marshal(actor)
	if err != nil {
		return errors.WithStack(err)
	}
	after, err := json.Marshal(map[string]any{"action": "freeze", "purge_after": d.PurgeAfter})
	if err != nil {
		return errors.WithStack(err)
	}
	audit := MutationAudit{ID: uuid.NewString(), Actor: string(actorJSON), RequestID: actor.RequestID, TenantID: d.TenantID, Resource: d.Family, ResourceID: d.ResourceID, Before: "null", After: string(after)}
	if d.Family == model.FamilyOrganization {
		audit.OrgID = d.ResourceID
	}
	return errors.WithStack(db.Create(&audit).Error)
}

// deletionTenant returns the tenant of the deletion of a lifecycle resource.
func deletionTenant(scope model.CommonScope, key string) (string, error) {
	if _, ok := lifecycleTargets[scope.Family]; !ok {
		return "", errors.WithStack(port.ErrInvalid)
	}
	if err := validateCommonScope(scope, key); err != nil || key == "" {
		return "", errors.WithStack(port.ErrInvalid)
	}
	if scope.Family == model.FamilyTenant {
		return key, nil
	}
	return scope.TenantID, nil
}

// readDeletionRow reads the deletion of a resource within db, with the ETag
// of the frozen resource while it is not purged.
func readDeletionRow(db *gorm.DB, scope model.CommonScope, key string) (ResourceDeletion, model.Deletion, error) {
	tenantID, err := deletionTenant(scope, key)
	if err != nil {
		return ResourceDeletion{}, model.Deletion{}, err
	}
	var rows []ResourceDeletion
	if err := db.Where("family = ? AND resource_id = ? AND tenant_id = ?", scope.Family, key, tenantID).Limit(1).Find(&rows).Error; err != nil {
		return ResourceDeletion{}, model.Deletion{}, errors.WithStack(err)
	}
	if len(rows) == 0 {
		return ResourceDeletion{}, model.Deletion{}, errors.WithStack(port.ErrNotFound)
	}
	out := rows[0].view()
	if rows[0].PurgedAt == nil {
		var projections []ProvisioningProjection
		if err := db.Where(projectionWhere, scope.Family, tenantID, "", key).Limit(1).Find(&projections).Error; err != nil {
			return ResourceDeletion{}, model.Deletion{}, errors.WithStack(err)
		}
		if len(projections) == 1 {
			out.ETag = model.CommonETag(projections[0].Revision)
		}
	}
	return rows[0], out, nil
}

// ReadDeletion implements port.LifecycleStore.
func (s *Store) ReadDeletion(ctx context.Context, scope model.CommonScope, key string) (model.Deletion, error) {
	var out model.Deletion
	err := s.readTransaction(ctx, func(db *gorm.DB) error {
		var err error
		_, out, err = readDeletionRow(db, scope, key)
		return err
	})
	return out, err
}

// ConfirmDeletion implements port.LifecycleStore. The condition must
// designate the revision of the frozen resource explicitly: a wildcard would
// confirm an export the client never compared.
func (s *Store) ConfirmDeletion(ctx context.Context, scope model.CommonScope, key string, condition model.MatchCondition, digest string) (model.Deletion, error) {
	var out model.Deletion
	if !condition.Present || condition.Any {
		return out, errors.WithStack(port.ErrConfirmationRequired)
	}
	if !deletion.ValidDigest(digest) {
		return out, errors.Wrap(port.ErrInvalid, "export_sha256 must be 64 lowercase hexadecimal characters")
	}
	if _, err := deletionTenant(scope, key); err != nil {
		return out, err
	}
	if s.lifecycleRetention <= 0 {
		return out, errors.WithStack(port.ErrLifecycleDisabled)
	}
	err := s.WithProvisioningTransaction(ctx, func(ptx port.ProvisioningTx) error {
		tx := ptx.(*provisioningTx)
		if err := tx.checkOwnership(ctx, scope.Family); err != nil {
			return err
		}
		if isPostgres(tx.db) {
			// Serializes the confirmations and the purge of the deletion.
			var locked []ResourceDeletion
			if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("family = ? AND resource_id = ?", scope.Family, key).Find(&locked).Error; err != nil {
				return errors.WithStack(err)
			}
		}
		row, deletion, err := readDeletionRow(tx.db, scope, key)
		if err != nil {
			return err
		}
		if row.PurgedAt != nil {
			return errors.WithStack(port.ErrNotFound)
		}
		if !condition.Matches(deletion.ETag) {
			return errors.WithStack(port.ErrPreconditionFailed)
		}
		if row.ConfirmedAt != nil {
			if row.ExportSHA256 != digest {
				return errors.Wrap(port.ErrExportMismatch, "another export is already confirmed")
			}
			out = deletion
			return nil
		}
		now := tx.db.NowFunc().UTC().Truncate(time.Microsecond)
		if err := tx.db.Model(&ResourceDeletion{}).Where("family = ? AND resource_id = ?", row.Family, row.ResourceID).
			Updates(map[string]any{"confirmed_at": now, "export_sha256": digest}).Error; err != nil {
			return errors.WithStack(err)
		}
		if err := auditDeletion(ctx, tx.db, row, map[string]any{"action": "export_confirmed", "export_sha256": digest}); err != nil {
			return err
		}
		deletion.ConfirmedAt, deletion.ExportSHA256 = &now, digest
		out = deletion
		return nil
	})
	return out, err
}

// deletionAuditResource marks the audits of the lifecycle operations that
// follow a freeze. The export leaves them out: it must not change once the
// scope is frozen.
const deletionAuditResource = "deletion"

// auditDeletion records a lifecycle operation on a deletion.
func auditDeletion(ctx context.Context, db *gorm.DB, d ResourceDeletion, after map[string]any) error {
	actor := model.ActorFromContext(ctx)
	actorJSON, err := json.Marshal(actor)
	if err != nil {
		return errors.WithStack(err)
	}
	after["resource_type"] = d.Family
	encoded, err := json.Marshal(after)
	if err != nil {
		return errors.WithStack(err)
	}
	audit := MutationAudit{ID: uuid.NewString(), Actor: string(actorJSON), RequestID: actor.RequestID, TenantID: d.TenantID, Resource: deletionAuditResource, ResourceID: d.ResourceID, Before: "null", After: string(encoded)}
	if d.Family == model.FamilyOrganization {
		audit.OrgID = d.ResourceID
	}
	return errors.WithStack(db.Create(&audit).Error)
}

var _ port.LifecycleStore = &Store{}
