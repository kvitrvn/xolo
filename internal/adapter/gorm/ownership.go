package gorm

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"

	"github.com/pkg/errors"
	"github.com/xolo-gateway/xolo/internal/core/model"
	"github.com/xolo-gateway/xolo/internal/core/port"
)

// OwnershipPolicy returns the write authority of each family, nil when the
// store checks none.
func (s *Store) OwnershipPolicy() model.OwnershipPolicy {
	return s.ownership
}

// checkOwnership refuses a write to family when the policy reserves it to
// another authority than the one of ctx. Writers whose effect has no
// projection (invitations, webhook subscriptions) call it themselves; every
// other identity write is checked when its projections are published.
func (s *Store) checkOwnership(ctx context.Context, family string) error {
	if !s.ownership.Allows(family, model.WriteAuthority(ctx)) {
		return errors.Wrapf(port.ErrOwnershipDenied, "%s", family)
	}
	return nil
}

// authorizeProjections checks the write authority of every projection a
// transaction changes, cascades included: the transaction rolls back as a
// whole when one of them belongs to another authority. Only the public
// representation is governed, so the fields outside the common contract
// (platform roles, organization settings, custom roles) stay local.
//
// A sign-in may write the very account it resolves, so that a member owned
// by the control plane can still link its proven identity. A member choosing
// their language may change the locale of their own representation, and
// nothing else of it.
func authorizeProjections(ctx context.Context, ownership model.OwnershipPolicy, changes []projectionChange) error {
	if ownership == nil {
		return nil
	}
	authority := model.WriteAuthority(ctx)
	signIn, signingIn := model.SignInAccount(ctx)
	localeOf, choosingLocale := model.LocaleChange(ctx)
	for _, change := range changes {
		family := change.id.family
		if ownership.Allows(family, authority) {
			continue
		}
		if authority == model.OwnerLocal && family == model.FamilyMember {
			if signingIn && change.id.key == string(signIn) {
				continue
			}
			if choosingLocale && change.id.key == string(localeOf) && differsOnlyByLocale(change) {
				continue
			}
		}
		return errors.Wrapf(port.ErrOwnershipDenied, "%s", family)
	}
	return nil
}

// differsOnlyByLocale reports whether a change keeps a member and every
// field of its representation but locale.
func differsOnlyByLocale(change projectionChange) bool {
	if change.stored == nil || change.want == nil {
		return false
	}
	var stored, want map[string]json.RawMessage
	if json.Unmarshal([]byte(change.stored.Representation), &stored) != nil || json.Unmarshal([]byte(change.want.Representation), &want) != nil {
		return false
	}
	delete(stored, "locale")
	delete(want, "locale")
	return maps.EqualFunc(stored, want, func(a, b json.RawMessage) bool { return bytes.Equal(a, b) })
}
