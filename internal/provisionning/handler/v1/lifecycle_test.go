package v1_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ncruces/go-sqlite3/gormlite"
	"github.com/stretchr/testify/require"
	xologorm "github.com/xolo-gateway/xolo/internal/adapter/gorm"
	"github.com/xolo-gateway/xolo/internal/core/model"
	"github.com/xolo-gateway/xolo/internal/core/service"
	"github.com/xolo-gateway/xolo/internal/deletion"
	v1 "github.com/xolo-gateway/xolo/internal/provisionning/handler/v1"
	gormpkg "gorm.io/gorm"
)

func TestFrozenResourceConflict(t *testing.T) {
	db, err := gormpkg.Open(gormlite.Open(":memory:"), &gormpkg.Config{})
	require.NoError(t, err)
	store := xologorm.NewStore(db, xologorm.WithLifecycle(true, time.Hour))
	require.NoError(t, store.PrepareLifecycle(context.Background(), true))
	tenant, err := store.GetTenantBySlug(context.Background(), model.DefaultTenantSlug)
	require.NoError(t, err)
	handler := v1.NewHandler(service.NewProvisioningService(store, store, store, store,
		service.WithProvisioningTransaction(store), service.WithProvisioningReader(store)), testVersion)
	tenantID := string(tenant.ID())
	orgID := putOrganization(t, handler, tenantID, "frozen")

	_, err = store.FreezeResource(context.Background(), model.CommonScope{Family: model.FamilyOrganization, TenantID: tenantID}, orgID, model.MatchCondition{})
	require.NoError(t, err)

	rec := call(t, handler, http.MethodPut, "/v1/tenants/"+tenantID+"/organizations/"+orgID, resource("frozen"))
	assertStatus(t, rec, http.StatusConflict)
	assertErrorCode(t, rec, "resource_deleted")
}

// lifecycleHandler serves a store recording deletions, with the lifecycle
// routes announced when enabled.
func lifecycleHandler(t *testing.T, enabled bool) (http.Handler, *xologorm.Store, string) {
	t.Helper()
	db, err := gormpkg.Open(gormlite.Open(":memory:"), &gormpkg.Config{})
	require.NoError(t, err)
	store := xologorm.NewStore(db, xologorm.WithLifecycle(enabled, time.Hour))
	require.NoError(t, store.PrepareLifecycle(context.Background(), enabled))
	tenant, err := store.GetTenantBySlug(context.Background(), model.DefaultTenantSlug)
	require.NoError(t, err)
	handler := v1.NewHandler(service.NewProvisioningService(store, store, store, store,
		service.WithProvisioningTransaction(store), service.WithProvisioningReader(store)), testVersion,
		v1.WithLifecycle(service.NewLifecycleService(store), enabled))
	return handler, store, string(tenant.ID())
}

func manifestCapabilities(t *testing.T, handler http.Handler) []any {
	t.Helper()
	rec := call(t, handler, http.MethodGet, "/v1/manifest", nil)
	assertStatus(t, rec, http.StatusOK)
	return decodeBody(t, rec)["capabilities"].([]any)
}

// TestLifecycleHTTPProfile drives a deletion over HTTP: DELETE, status,
// export, confirmation.
func TestLifecycleHTTPProfile(t *testing.T) {
	handler, _, tenantID := lifecycleHandler(t, true)
	require.Contains(t, manifestCapabilities(t, handler), "lifecycle")
	orgID := putOrganization(t, handler, tenantID, "deleted")
	orgPath := "/v1/tenants/" + tenantID + "/organizations/" + orgID

	rec := call(t, handler, http.MethodGet, orgPath+"/deletion", nil)
	assertStatus(t, rec, http.StatusNotFound)
	rec = call(t, handler, http.MethodDelete, orgPath, map[string]any{})
	assertStatus(t, rec, http.StatusBadRequest)
	rec = call(t, handler, http.MethodDelete, orgPath+"?force=true", nil)
	assertStatus(t, rec, http.StatusBadRequest)
	rec = callWithHeaders(t, handler, http.MethodDelete, orgPath, ifMatchHeader(`W/"1"`), nil)
	assertStatus(t, rec, http.StatusPreconditionFailed)

	rec = call(t, handler, http.MethodDelete, orgPath, nil)
	assertStatus(t, rec, http.StatusAccepted)
	etag := rec.Header().Get("ETag")
	require.NotEmpty(t, etag)
	body := decodeBody(t, rec)
	require.Equal(t, "organization", body["resource_type"])
	require.Equal(t, orgID, body["resource_id"])
	rec = call(t, handler, http.MethodDelete, orgPath, nil)
	assertStatus(t, rec, http.StatusAccepted)
	require.Equal(t, etag, rec.Header().Get("ETag"), "a repeated DELETE returns the recorded deletion")
	rec = call(t, handler, http.MethodPut, orgPath, resource("deleted"))
	assertStatus(t, rec, http.StatusConflict)
	assertErrorCode(t, rec, "resource_deleted")

	rec = call(t, handler, http.MethodGet, orgPath+"/deletion", nil)
	assertStatus(t, rec, http.StatusOK)
	require.Equal(t, etag, rec.Header().Get("ETag"))

	rec = call(t, handler, http.MethodGet, orgPath+"/deletion/export", nil)
	assertStatus(t, rec, http.StatusOK)
	require.Equal(t, "application/x-ndjson", rec.Header().Get("Content-Type"))
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	summary, err := deletion.Verify(rec.Body)
	require.NoError(t, err)
	confirmation := map[string]any{"export_sha256": summary.SHA256}

	confirm := orgPath + "/purge-confirmation"
	rec = callWithContentType(t, handler, http.MethodPost, confirm, "text/plain", confirmation)
	assertStatus(t, rec, http.StatusUnsupportedMediaType)
	rec = call(t, handler, http.MethodPost, confirm, confirmation)
	assertStatus(t, rec, http.StatusPreconditionRequired)
	assertErrorCode(t, rec, "precondition_required")
	rec = callWithHeaders(t, handler, http.MethodPost, confirm, ifMatchHeader("*"), confirmation)
	assertStatus(t, rec, http.StatusPreconditionRequired)
	for _, invalid := range []any{"null", map[string]any{}, map[string]any{"export_sha256": summary.SHA256, "extra": true}, "{} {}"} {
		raw, ok := invalid.(string)
		if !ok {
			encoded, err := json.Marshal(invalid)
			require.NoError(t, err)
			raw = string(encoded)
		}
		rec = callWithContentType(t, handler, http.MethodPost, confirm, "application/json", raw)
		assertStatus(t, rec, http.StatusBadRequest)
	}
	rec = callWithHeaders(t, handler, http.MethodPost, confirm, ifMatchHeader(`W/"1"`), confirmation)
	assertStatus(t, rec, http.StatusPreconditionFailed)
	rec = callWithHeaders(t, handler, http.MethodPost, confirm, ifMatchHeader(etag), map[string]any{"export_sha256": strings.Repeat("0", 64)})
	assertStatus(t, rec, http.StatusConflict)
	assertErrorCode(t, rec, "export_mismatch")
	for range 2 {
		rec = callWithHeaders(t, handler, http.MethodPost, confirm, ifMatchHeader(etag), confirmation)
		assertStatus(t, rec, http.StatusOK)
		require.Equal(t, etag, rec.Header().Get("ETag"), "the confirmation keeps the revision")
		require.Equal(t, summary.SHA256, decodeBody(t, rec)["export_sha256"])
	}
}

// TestLifecycleDisabled answers lifecycle_disabled, and announces nothing.
func TestLifecycleDisabled(t *testing.T) {
	handler, _, tenantID := lifecycleHandler(t, false)
	require.NotContains(t, manifestCapabilities(t, handler), "lifecycle")
	orgID := putOrganization(t, handler, tenantID, "kept")
	rec := call(t, handler, http.MethodDelete, "/v1/tenants/"+tenantID+"/organizations/"+orgID, nil)
	assertStatus(t, rec, http.StatusConflict)
	assertErrorCode(t, rec, "lifecycle_disabled")
}

// TestImmediateDeletes removes domains and memberships at once, whether the
// lifecycle is enabled or not.
func TestImmediateDeletes(t *testing.T) {
	handler, _, tenantID := lifecycleHandler(t, false)
	tenantPath := "/v1/tenants/" + tenantID
	rec := call(t, handler, http.MethodPut, tenantPath+"/domains/gone.example.test", map[string]any{"status": "active"})
	assertStatus(t, rec, http.StatusOK)
	domainETag := rec.Header().Get("ETag")
	rec = callWithHeaders(t, handler, http.MethodDelete, tenantPath+"/domains/gone.example.test", ifMatchHeader(`W/"1"`), nil)
	assertStatus(t, rec, http.StatusPreconditionFailed)
	rec = callWithHeaders(t, handler, http.MethodDelete, tenantPath+"/domains/gone.example.test", ifMatchHeader(domainETag), nil)
	assertStatus(t, rec, http.StatusNoContent)
	rec = call(t, handler, http.MethodGet, tenantPath+"/domains/gone.example.test", nil)
	assertStatus(t, rec, http.StatusNotFound)
	rec = call(t, handler, http.MethodDelete, tenantPath+"/domains/gone.example.test", nil)
	assertStatus(t, rec, http.StatusNoContent)

	orgID := putOrganization(t, handler, tenantID, "members")
	memberID := uuid.NewString()
	rec = call(t, handler, http.MethodPut, tenantPath+"/members/"+memberID, memberBody("gone@example.test"))
	assertStatus(t, rec, http.StatusOK)
	membership := tenantPath + "/organizations/" + orgID + "/members/" + memberID
	rec = call(t, handler, http.MethodPut, membership, membershipBody("member"))
	assertStatus(t, rec, http.StatusOK)
	rec = call(t, handler, http.MethodDelete, membership, nil)
	assertStatus(t, rec, http.StatusNoContent)
	rec = call(t, handler, http.MethodGet, membership, nil)
	assertStatus(t, rec, http.StatusNotFound)
	rec = call(t, handler, http.MethodDelete, "/v1/tenants/"+uuid.NewString()+"/organizations/"+orgID+"/members/"+memberID, nil)
	assertStatus(t, rec, http.StatusNotFound)
	assertErrorCode(t, rec, "parent_not_found")
}
