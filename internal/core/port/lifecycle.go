package port

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/xolo-gateway/xolo/internal/core/model"
)

var (
	// ErrResourceDeleted refuses a write to a resource whose deletion is
	// recorded, or to anything it holds.
	ErrResourceDeleted = fmt.Errorf("resource deleted: %w", ErrNotAllowed)

	// ErrLifecycleDisabled refuses to record a deletion while the lifecycle
	// is disabled.
	ErrLifecycleDisabled = fmt.Errorf("lifecycle disabled: %w", ErrNotAllowed)

	// ErrConfirmationRequired refuses a confirmation that does not designate
	// the revision of the deleted resource explicitly.
	ErrConfirmationRequired = fmt.Errorf("confirmation required: %w", ErrNotAllowed)

	// ErrExportMismatch refuses a confirmation whose digest is not the one of
	// the current export of the deletion, or differs from the one already
	// confirmed.
	ErrExportMismatch = fmt.Errorf("export mismatch: %w", ErrNotAllowed)

	// ErrPurgeNotReady refuses to purge a deletion not confirmed yet, or whose
	// retention has not elapsed.
	ErrPurgeNotReady = fmt.Errorf("purge not ready: %w", ErrNotAllowed)
)

// LifecycleStore records deletions. A recorded deletion freezes the resource
// and its whole scope until it is purged.
type LifecycleStore interface {
	// FreezeResource records the deletion of a tenant, an organization or a
	// member, after checking condition against its current revision. A
	// repeated call returns the deletion already recorded.
	FreezeResource(ctx context.Context, scope model.CommonScope, key string, condition model.MatchCondition) (model.Deletion, error)
	// ReadDeletion returns the deletion recorded for a resource, ErrNotFound
	// when there is none.
	ReadDeletion(ctx context.Context, scope model.CommonScope, key string) (model.Deletion, error)
	// ReadDeletionScope reads, on one snapshot, the deletion of a resource
	// not purged yet, then every row of its scope, table by table in a
	// stable order. Secrets are left out. The rows are frozen: two reads give
	// the same rows, unless a purge removed some in between.
	ReadDeletionScope(ctx context.Context, scope model.CommonScope, key string, start func(source string, deletion model.Deletion) error, row func(table string, row json.RawMessage) error) error
	// ConfirmDeletion records the digest of the export of a deletion, under
	// the explicit revision of the deleted resource. The caller has checked
	// the digest against the export. A repeated confirmation with the same
	// digest returns the deletion; another digest is ErrExportMismatch.
	ConfirmDeletion(ctx context.Context, scope model.CommonScope, key string, condition model.MatchCondition, digest string) (model.Deletion, error)
	// DueDeletions lists confirmed deletions whose retention has elapsed,
	// those attempted the least first.
	DueDeletions(ctx context.Context, limit int) ([]model.Deletion, error)
	// PurgeDeletion removes every row of the scope of a due deletion. It can
	// be interrupted and resumed; a purged deletion is left as is.
	PurgeDeletion(ctx context.Context, deletion model.Deletion) error
}
