package cache

import (
	"context"

	"github.com/xolo-gateway/xolo/internal/core/model"
	"github.com/xolo-gateway/xolo/internal/core/port"
)

// LifecycleStore invalidates the cached users and tokens of the scope of a
// deletion, once frozen and once purged: the store deactivates and removes
// them in its own transactions.
type LifecycleStore struct {
	port.LifecycleStore
	users *UserStore
}

func NewLifecycleStore(backend port.LifecycleStore, users *UserStore) *LifecycleStore {
	return &LifecycleStore{LifecycleStore: backend, users: users}
}

func (s *LifecycleStore) FreezeResource(ctx context.Context, scope model.CommonScope, key string, condition model.MatchCondition) (model.Deletion, error) {
	deletion, err := s.LifecycleStore.FreezeResource(ctx, scope, key, condition)
	if err == nil {
		s.invalidate(deletion)
	}
	return deletion, err
}

func (s *LifecycleStore) PurgeDeletion(ctx context.Context, deletion model.Deletion) error {
	err := s.LifecycleStore.PurgeDeletion(ctx, deletion)
	if err == nil {
		s.invalidate(deletion)
	}
	return err
}

func (s *LifecycleStore) invalidate(d model.Deletion) {
	if s.users == nil {
		return
	}
	switch d.Family {
	case model.FamilyTenant:
		tenant := model.TenantID(d.ResourceID)
		s.users.userCache.RemoveMatching(func(u *CacheableUser) bool { return u.TenantID() == tenant })
		// A cached application token tells its organization, not its tenant.
		s.users.authTokenCache.RemoveMatching(func(*CacheableAuthToken) bool { return true })
	case model.FamilyOrganization:
		org := model.OrgID(d.ResourceID)
		s.users.authTokenCache.RemoveMatching(func(t *CacheableAuthToken) bool {
			app := t.Application()
			return t.OrgID() == org || (app != nil && app.OrgID() == org)
		})
	case model.FamilyMember:
		user := model.UserID(d.ResourceID)
		s.users.userCache.RemoveMatching(func(u *CacheableUser) bool { return u.ID() == user })
		s.users.authTokenCache.RemoveMatching(func(t *CacheableAuthToken) bool {
			owner := t.Owner()
			return owner != nil && owner.ID() == user
		})
	}
}

var _ port.LifecycleStore = (*LifecycleStore)(nil)
