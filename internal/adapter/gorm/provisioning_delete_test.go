package gorm_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xolo-gateway/xolo/internal/core/model"
	"github.com/xolo-gateway/xolo/internal/core/port"
	"github.com/xolo-gateway/xolo/internal/core/service"
	gormpkg "gorm.io/gorm"
)

// TestDeleteDomain removes a domain at once, under its condition, and
// publishes its deletion. A missing domain is no error without condition.
func TestDeleteDomain(t *testing.T) {
	eachBackendDB(t, func(t *testing.T, db *gormpkg.DB) {
		ctx := t.Context()
		store := newSeededStore(t, db)
		svc := businessService(store)
		scope := model.CommonScope{Family: model.FamilyTenantDomain, TenantID: string(testTenantID)}
		const host = "deleted.example.test"
		item, err := svc.PutCommon(ctx, scope, host, noCondition, func(ctx context.Context, tx *service.ProvisioningService) error {
			_, err := tx.PutDomain(ctx, testTenantID, host, model.StatusActive)
			return err
		})
		require.NoError(t, err)
		cursor, err := store.CaptureEventCursor(ctx)
		require.NoError(t, err)

		require.ErrorIs(t, svc.DeleteDomain(ctx, model.NewTenantID(), host, noCondition), port.ErrParentNotFound)
		require.ErrorIs(t, svc.DeleteDomain(ctx, testTenantID, host, ifMatch(t, `W/"1"`)), port.ErrPreconditionFailed)
		require.NoError(t, svc.DeleteDomain(ctx, testTenantID, host, ifMatch(t, item.ETag)))
		_, err = store.GetDomain(ctx, host)
		require.ErrorIs(t, err, port.ErrNotFound)
		events, _ := feedSince(t, store, cursor)
		require.Equal(t, []string{"tenant_domain.deleted.v1"}, eventTypes(events))

		require.NoError(t, svc.DeleteDomain(ctx, testTenantID, host, noCondition), "deleting a missing domain changes nothing")
		require.ErrorIs(t, svc.DeleteDomain(ctx, testTenantID, host, ifMatch(t, item.ETag)), port.ErrPreconditionFailed,
			"a condition on a missing domain fails")
	})
}

// TestDeleteOrgMember removes a membership at once, never the last active
// owner of the organization.
func TestDeleteOrgMember(t *testing.T) {
	eachBackendDB(t, func(t *testing.T, db *gormpkg.DB) {
		ctx := t.Context()
		store := newSeededStore(t, db)
		svc := businessService(store)
		fixture := newOwnershipFixture(t, store)
		owner := newOwnershipFixture(t, store)
		_, err := svc.PutOrgMember(ctx, testTenantID, fixture.org, owner.user, service.CommonMembership{Role: model.MembershipRoleOwner, Status: model.StatusActive})
		require.NoError(t, err)

		require.ErrorIs(t, svc.DeleteOrgMember(ctx, testTenantID, fixture.org, owner.user, noCondition), port.ErrLastOwner)
		require.NoError(t, svc.DeleteOrgMember(ctx, testTenantID, fixture.org, fixture.user, noCondition))
		_, err = store.GetUserOrgMembership(ctx, fixture.user, fixture.org)
		require.ErrorIs(t, err, port.ErrNotFound)
		require.NoError(t, svc.DeleteOrgMember(ctx, testTenantID, fixture.org, fixture.user, noCondition))
		require.ErrorIs(t, svc.DeleteOrgMember(ctx, model.NewTenantID(), fixture.org, owner.user, noCondition), port.ErrParentNotFound)
		require.ErrorIs(t, svc.DeleteOrgMember(ctx, testTenantID, model.NewOrgID(), owner.user, noCondition), port.ErrParentNotFound)
	})
}

// TestDeleteBusiness removes each business family at once. A resource of
// another organization is never touched, a builtin role is refused, and a
// frozen scope refuses every deletion.
func TestDeleteBusiness(t *testing.T) {
	eachBackendDB(t, func(t *testing.T, db *gormpkg.DB) {
		ctx := t.Context()
		base := newSeededStore(t, db)
		svc := businessService(base)
		fixture := newOwnershipFixture(t, base)
		f := newBusinessFixture(t, base, svc, fixture)
		scopes := businessScopes(fixture, f)
		elsewhere := newOwnershipFixture(t, base)

		// Under another organization, the resource is missing: nothing is
		// removed.
		foreign := scopes[f.alert]
		foreign.OrganizationID = string(elsewhere.org)
		require.NoError(t, svc.DeleteBusiness(ctx, foreign, f.alert, noCondition))
		_, err := base.GetAlertByID(ctx, model.AlertID(f.alert))
		require.NoError(t, err)

		roles, err := base.ListOrgRoles(ctx, fixture.org)
		require.NoError(t, err)
		for _, role := range roles {
			if role.Builtin() {
				err := svc.DeleteBusiness(ctx, scopes[f.role], string(role.ID()), noCondition)
				require.ErrorIs(t, err, port.ErrNotAllowed, "a builtin role is never deleted")
			}
		}

		stale := ifMatch(t, `W/"1"`)
		for _, key := range []string{f.alert, f.quota, f.app, f.role, f.provider} {
			require.ErrorIs(t, svc.DeleteBusiness(ctx, scopes[key], key, stale), port.ErrPreconditionFailed, scopes[key].Family)
			item, err := svc.GetCommon(ctx, scopes[key], key)
			require.NoError(t, err)
			require.NoError(t, svc.DeleteBusiness(ctx, scopes[key], key, ifMatch(t, item.ETag)), scopes[key].Family)
			_, err = svc.GetCommon(ctx, scopes[key], key)
			require.ErrorIs(t, err, port.ErrNotFound, scopes[key].Family)
			require.NoError(t, svc.DeleteBusiness(ctx, scopes[key], key, noCondition), "deleting a missing %s changes nothing", scopes[key].Family)
		}

		// A frozen organization refuses every deletion of its scope.
		other := newBusinessFixture(t, base, svc, elsewhere)
		_, err = lifecycleStore(t, db).FreezeResource(ctx, orgScope(testTenantID), string(elsewhere.org), noCondition)
		require.NoError(t, err)
		otherScopes := businessScopes(elsewhere, other)
		require.ErrorIs(t, svc.DeleteBusiness(ctx, otherScopes[other.alert], other.alert, noCondition), port.ErrResourceDeleted)
	})
}
