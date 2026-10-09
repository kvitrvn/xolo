//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/xolo-gateway/xolo/internal/core/model"
	"github.com/xolo-gateway/xolo/internal/deletion"
)

// TestLifecyclePurge starts a multi-tenant server with the lifecycle enabled
// on a snapshot of the seeded database, deletes a tenant through the
// provisioning listener, exports its scope, confirms the export and waits
// for the purge worker.
func TestLifecyclePurge(t *testing.T) {
	dir := t.TempDir()
	dsn := filepath.Join(dir, "lifecycle.sqlite")
	db, err := openDB(env.dsn)
	require.NoError(t, err)
	require.NoError(t, db.Exec("VACUUM INTO ?", dsn).Error)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	port, err := freePort()
	require.NoError(t, err)
	provisioningEnv, provisioning, err := configureProvisioning(dir)
	require.NoError(t, err)
	config := append(serverEnv(env.pluginsDir, port, fmt.Sprintf("http://127.0.0.1:%d", port), dsn), provisioningEnv...)
	config = append(config,
		"XOLO_MULTITENANCY_ENABLED=true",
		"XOLO_LIFECYCLE_ENABLED=true",
		"XOLO_LIFECYCLE_RETENTION=1s",
		"XOLO_LIFECYCLE_POLL_INTERVAL=1s",
	)
	logPath := filepath.Join(dir, "server.log")
	stop, err := launchServer(env.serverBin, logPath, config)
	require.NoError(t, err)
	t.Cleanup(stop)
	t.Cleanup(func() {
		if t.Failed() {
			if logs, err := os.ReadFile(logPath); err == nil {
				t.Logf("---- lifecycle server log ----\n%s", logs)
			}
		}
	})

	require.Eventually(t, func() bool {
		resp, err := provisioning.client.Get(provisioning.url + "/v1/manifest")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		var manifest struct {
			Capabilities []string `json:"capabilities"`
		}
		return resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&manifest) == nil && strings.Contains(strings.Join(manifest.Capabilities, " "), "lifecycle")
	}, 90*time.Second, 250*time.Millisecond, "provisioning listener not ready")

	put := func(path string, body any) {
		t.Helper()
		status, _, raw := provisioning.exchange(t, http.MethodPut, path, nil, body)
		require.Equal(t, http.StatusOK, status, string(raw))
	}
	tenantID := uuid.NewString()
	tenantPath := "/v1/tenants/" + tenantID
	put(tenantPath, map[string]any{"slug": "e2e-deleted", "name": "Deleted", "status": "active"})
	put(tenantPath+"/organizations/"+uuid.NewString(), map[string]any{"slug": "inside", "name": "Inside", "status": "active"})
	put(tenantPath+"/members/"+uuid.NewString(), map[string]any{"email": "deleted@e2e.test", "tenant_role": "member", "status": "active"})
	status, _, raw := provisioning.exchange(t, http.MethodGet, "/v1/events/cursor", nil, nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var start struct {
		Cursor string `json:"cursor"`
	}
	require.NoError(t, json.Unmarshal(raw, &start))

	status, headers, raw := provisioning.exchange(t, http.MethodDelete, tenantPath, nil, nil)
	require.Equal(t, http.StatusAccepted, status, string(raw))
	etag := headers.Get("ETag")
	require.NotEmpty(t, etag)

	status, headers, raw = provisioning.exchange(t, http.MethodGet, tenantPath+"/deletion/export", nil, nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	require.Equal(t, "application/x-ndjson", headers.Get("Content-Type"))
	summary, err := deletion.Verify(bytes.NewReader(raw))
	require.NoError(t, err)
	require.Equal(t, 1, summary.Tables["tenants"])
	require.Equal(t, 1, summary.Tables["organizations"])
	require.Equal(t, 1, summary.Tables["users"])
	require.NotContains(t, string(raw), "e2e-session-key")

	status, _, raw = provisioning.exchange(t, http.MethodPost, tenantPath+"/purge-confirmation", map[string]string{"If-Match": etag}, map[string]any{"export_sha256": summary.SHA256})
	require.Equal(t, http.StatusOK, status, string(raw))

	var purged model.Deletion
	require.Eventually(t, func() bool {
		status, _, raw := provisioning.exchange(t, http.MethodGet, tenantPath+"/deletion", nil, nil)
		return status == http.StatusOK && json.Unmarshal(raw, &purged) == nil && purged.PurgedAt != nil
	}, 30*time.Second, 250*time.Millisecond, "the tenant is not purged")
	require.Equal(t, model.DeletionPurged, purged.Diagnostic)

	status, _, _ = provisioning.exchange(t, http.MethodGet, tenantPath, nil, nil)
	require.Equal(t, http.StatusNotFound, status)
	status, _, raw = provisioning.exchange(t, http.MethodPut, tenantPath, nil, map[string]any{"slug": "e2e-deleted", "name": "Again", "status": "active"})
	require.Equal(t, http.StatusConflict, status, string(raw))
	require.Contains(t, string(raw), "resource_deleted")

	status, _, raw = provisioning.exchange(t, http.MethodGet, "/v1/events?cursor="+url.QueryEscape(start.Cursor), nil, nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var page model.CommonEventPage
	require.NoError(t, json.Unmarshal(raw, &page))
	var types []string
	for _, event := range page.Items {
		if event.Data.Key.TenantID == tenantID {
			types = append(types, event.Type)
		}
	}
	require.Equal(t, []string{"tenant.deleted.v1"}, types, "the events of the purged tenant are gone, its deletion stays")
}
