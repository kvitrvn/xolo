package service

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/pkg/errors"
	"github.com/xolo-gateway/xolo/internal/core/model"
	"github.com/xolo-gateway/xolo/internal/core/port"
	"github.com/xolo-gateway/xolo/internal/deletion"
)

// purgeLimit bounds the deletions one tick of the purge worker takes.
const purgeLimit = 100

// LifecycleService runs the deletion of tenants, organizations and members.
// Recording a deletion freezes the resource and everything it holds; the
// client then exports the scope, confirms the export with its digest, and
// the scope is purged once the retention has elapsed.
type LifecycleService struct {
	store port.LifecycleStore
}

func NewLifecycleService(store port.LifecycleStore) *LifecycleService {
	return &LifecycleService{store: store}
}

// Freeze records the deletion of the resource, after checking condition
// against its current revision.
func (s *LifecycleService) Freeze(ctx context.Context, scope model.CommonScope, key string, condition model.MatchCondition) (model.Deletion, error) {
	return s.store.FreezeResource(model.EnsureActor(ctx), scope, key, condition)
}

// Read returns the deletion recorded for the resource.
func (s *LifecycleService) Read(ctx context.Context, scope model.CommonScope, key string) (model.Deletion, error) {
	return s.store.ReadDeletion(ctx, scope, key)
}

// Export writes the export of the scope of the deletion to w. Once the
// first line is written, a failure only cuts the stream: the export lacks
// its trailer, and its verification fails.
func (s *LifecycleService) Export(ctx context.Context, scope model.CommonScope, key string, w io.Writer) error {
	_, err := deletion.Export(ctx, s.store, scope, key, w)
	return err
}

// Confirm records the digest of the export of the deletion, which allows
// its purge once the retention has elapsed. The condition must designate the
// revision of the deleted resource, and the digest must be the one of its
// current export: a client confirms what it holds, nothing else.
func (s *LifecycleService) Confirm(ctx context.Context, scope model.CommonScope, key string, condition model.MatchCondition, digest string) (model.Deletion, error) {
	ctx = model.EnsureActor(ctx)
	if !condition.Present || condition.Any {
		return model.Deletion{}, errors.WithStack(port.ErrConfirmationRequired)
	}
	if !deletion.ValidDigest(digest) {
		return model.Deletion{}, errors.Wrap(port.ErrInvalid, "export_sha256 must be 64 lowercase hexadecimal characters")
	}
	current, err := s.store.ReadDeletion(ctx, scope, key)
	if err != nil {
		return model.Deletion{}, err
	}
	// A repeated confirmation is answered by the store, against the digest
	// already confirmed: the export may have changed since.
	if current.ConfirmedAt == nil && current.PurgedAt == nil {
		exported, err := deletion.Digest(ctx, s.store, scope, key)
		if err != nil {
			return model.Deletion{}, err
		}
		if exported != digest {
			return model.Deletion{}, errors.Wrap(port.ErrExportMismatch, "the digest is not the one of the current export")
		}
	}
	return s.store.ConfirmDeletion(ctx, scope, key, condition, digest)
}

// PurgeDue purges the deletions whose export is confirmed and whose
// retention has elapsed, and returns how many it purged. A failed purge is
// logged and attempted again later, after the others.
func (s *LifecycleService) PurgeDue(ctx context.Context) (int, error) {
	due, err := s.store.DueDeletions(ctx, purgeLimit)
	if err != nil {
		return 0, err
	}
	purged := 0
	for _, d := range due {
		if err := s.store.PurgeDeletion(ctx, d); err != nil {
			if ctx.Err() != nil {
				return purged, ctx.Err()
			}
			slog.ErrorContext(ctx, "purge failed", slog.String("resource_type", d.Family), slog.String("resource_id", d.ResourceID), slog.Any("error", errors.WithStack(err)))
			continue
		}
		purged++
	}
	return purged, nil
}

// Run purges the due deletions at every interval until ctx is done.
// Failures are logged and retried at the next tick: the purge never stops the
// server. Every replica may run it: a deletion is purged by one at a time.
func (s *LifecycleService) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := s.PurgeDue(ctx); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "purge of due deletions failed", slog.Any("error", errors.WithStack(err)))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
