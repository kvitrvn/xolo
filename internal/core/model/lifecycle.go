package model

import "time"

// LifecycleFamilies lists the families whose deletion is recorded before
// their purge: everything they hold is frozen in between.
var LifecycleFamilies = []string{FamilyTenant, FamilyOrganization, FamilyMember}

// Diagnostics of a deletion.
const (
	DeletionPurged      = "purged"
	DeletionPurgeFailed = "purge_failed"
)

// Deletion records the deletion of a resource. From DeletedAt, the resource
// and everything it holds are frozen: no row of its scope may be written
// anymore. PurgeAfter is the earliest time it may be purged, once its export
// is confirmed. A purged deletion stays recorded: its identifier is retired.
type Deletion struct {
	Family     string    `json:"resource_type"`
	TenantID   string    `json:"tenant_id"`
	ResourceID string    `json:"resource_id"`
	DeletedAt  time.Time `json:"deleted_at"`
	PurgeAfter time.Time `json:"purge_after"`
	// ETag is the revision of the frozen resource, which no longer changes
	// until its purge. The confirmation of the export is conditioned on it.
	ETag         string     `json:"etag,omitempty"`
	ExportSHA256 string     `json:"export_sha256,omitempty"`
	ConfirmedAt  *time.Time `json:"confirmed_at,omitempty"`
	PurgedAt     *time.Time `json:"purged_at,omitempty"`
	// PurgedWith designates the deletion that purged this resource with its
	// parent.
	PurgedWith string `json:"purged_with,omitempty"`
	Attempts   int    `json:"attempts"`
	Diagnostic string `json:"diagnostic,omitempty"`
}
