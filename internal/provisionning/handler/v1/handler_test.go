package v1_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	xologorm "github.com/bornholm/xolo/internal/adapter/gorm"
	"github.com/bornholm/xolo/internal/core/rbac"
	"github.com/bornholm/xolo/internal/core/service"
	v1 "github.com/bornholm/xolo/internal/provisionning/handler/v1"
	_ "github.com/ncruces/go-sqlite3/embed"
	"github.com/ncruces/go-sqlite3/gormlite"
	gormpkg "gorm.io/gorm"
)

func newTestHandler(t *testing.T) (*v1.Handler, *gormpkg.DB) {
	t.Helper()

	db, err := gormpkg.Open(gormlite.Open(":memory:"), &gormpkg.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	store := xologorm.NewStore(db)

	return v1.NewHandler(service.NewProvisioningService(store, store, store)), db
}

// call performs a request against the handler. body may be nil, a string or any
// JSON-serializable value.
func call(t *testing.T, handler http.Handler, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader *bytes.Reader

	switch payload := body.(type) {
	case nil:
		reader = bytes.NewReader(nil)
	case string:
		reader = bytes.NewReader([]byte(payload))
	default:
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}

	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}

	return payload
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()

	if rec.Code != want {
		t.Fatalf("status: got %d, want %d (body: %s)", rec.Code, want, rec.Body.String())
	}
}

func assertErrorCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()

	payload := decodeBody(t, rec)

	errorBody, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("body should carry an error envelope, got %s", rec.Body.String())
	}
	if got := errorBody["code"]; got != want {
		t.Errorf("error code: got %v, want %q", got, want)
	}
	if message, _ := errorBody["message"].(string); message == "" {
		t.Error("error message should not be empty")
	}
}

// createTenant provisions a tenant through the API and returns its identifier.
func createTenant(t *testing.T, handler http.Handler, slug string, withOwner bool) (tenantID, membershipID string) {
	t.Helper()

	payload := map[string]any{"slug": slug, "name": strings.ToUpper(slug)}
	if withOwner {
		payload["owner"] = map[string]any{
			"provider":    "openid-connect",
			"subject":     "sub-" + slug,
			"email":       slug + "@example.tld",
			"displayName": slug + " owner",
		}
	}

	rec := call(t, handler, http.MethodPost, "/v1/tenants", payload)
	assertStatus(t, rec, http.StatusCreated)

	body := decodeBody(t, rec)

	tenant, ok := body["tenant"].(map[string]any)
	if !ok {
		t.Fatalf("missing tenant in response: %s", rec.Body.String())
	}
	tenantID, _ = tenant["id"].(string)

	if membership, ok := body["ownerMembership"].(map[string]any); ok {
		membershipID, _ = membership["id"].(string)
	}

	return tenantID, membershipID
}

func TestHealthzAndPermissions(t *testing.T) {
	handler, _ := newTestHandler(t)

	rec := call(t, handler, http.MethodGet, "/v1/healthz", nil)
	assertStatus(t, rec, http.StatusOK)

	rec = call(t, handler, http.MethodGet, "/v1/permissions", nil)
	assertStatus(t, rec, http.StatusOK)

	groups, ok := decodeBody(t, rec)["groups"].([]any)
	if !ok {
		t.Fatalf("missing groups: %s", rec.Body.String())
	}
	if len(groups) != len(rbac.Catalog()) {
		t.Errorf("groups: got %d, want %d", len(groups), len(rbac.Catalog()))
	}
}

func TestTenantEndpoints(t *testing.T) {
	t.Run("creates a tenant with its initial owner", func(t *testing.T) {
		handler, _ := newTestHandler(t)

		rec := call(t, handler, http.MethodPost, "/v1/tenants", map[string]any{
			"slug": "acme",
			"name": "Acme",
			"owner": map[string]any{
				"provider":    "openid-connect",
				"subject":     "sub-owner",
				"email":       "owner@acme.tld",
				"displayName": "Owner",
			},
		})
		assertStatus(t, rec, http.StatusCreated)

		body := decodeBody(t, rec)

		if created, _ := body["ownerCreated"].(bool); !created {
			t.Error("ownerCreated should be true")
		}

		owner, ok := body["owner"].(map[string]any)
		if !ok {
			t.Fatalf("missing owner: %s", rec.Body.String())
		}

		roles, _ := owner["platformRoles"].([]any)
		if len(roles) != 1 || roles[0] != "user" {
			t.Errorf("platform roles: got %v, want [user]", roles)
		}

		membership, ok := body["ownerMembership"].(map[string]any)
		if !ok {
			t.Fatalf("missing ownerMembership: %s", rec.Body.String())
		}
		memberRoles, _ := membership["roles"].([]any)
		if len(memberRoles) != 1 {
			t.Fatalf("membership roles: got %v", memberRoles)
		}
		if kind := memberRoles[0].(map[string]any)["builtinKind"]; kind != "owner" {
			t.Errorf("builtin kind: got %v, want owner", kind)
		}
	})

	t.Run("reports a duplicate slug as a conflict", func(t *testing.T) {
		handler, _ := newTestHandler(t)

		tenantID, _ := createTenant(t, handler, "acme", false)

		rec := call(t, handler, http.MethodPost, "/v1/tenants", map[string]any{"slug": "acme", "name": "Acme"})
		assertStatus(t, rec, http.StatusConflict)
		assertErrorCode(t, rec, "conflict")

		if message := decodeBody(t, rec)["error"].(map[string]any)["message"].(string); !strings.Contains(message, tenantID) {
			t.Errorf("conflict message should carry the existing id %q, got %q", tenantID, message)
		}
	})

	t.Run("rejects invalid payloads", func(t *testing.T) {
		handler, _ := newTestHandler(t)

		cases := map[string]struct {
			body   any
			status int
			code   string
		}{
			"malformed json": {"{", http.StatusBadRequest, "invalid_request"},
			"unknown field":  {map[string]any{"slug": "acme", "name": "Acme", "nope": true}, http.StatusBadRequest, "invalid_request"},
			"invalid slug":   {map[string]any{"slug": "Acme Corp", "name": "Acme"}, http.StatusUnprocessableEntity, "unprocessable"},
			"missing name":   {map[string]any{"slug": "acme"}, http.StatusUnprocessableEntity, "unprocessable"},
			"bad currency":   {map[string]any{"slug": "acme", "name": "Acme", "currency": "XXX"}, http.StatusUnprocessableEntity, "unprocessable"},
		}

		for name, testCase := range cases {
			t.Run(name, func(t *testing.T) {
				rec := call(t, handler, http.MethodPost, "/v1/tenants", testCase.body)
				assertStatus(t, rec, testCase.status)
				assertErrorCode(t, rec, testCase.code)
			})
		}
	})

	t.Run("reads, updates and deletes a tenant", func(t *testing.T) {
		handler, _ := newTestHandler(t)

		tenantID, _ := createTenant(t, handler, "acme", false)

		rec := call(t, handler, http.MethodGet, "/v1/tenants/"+tenantID, nil)
		assertStatus(t, rec, http.StatusOK)

		rec = call(t, handler, http.MethodPatch, "/v1/tenants/"+tenantID, map[string]any{"name": "Acme Corporation"})
		assertStatus(t, rec, http.StatusOK)
		if name := decodeBody(t, rec)["name"]; name != "Acme Corporation" {
			t.Errorf("name: got %v", name)
		}

		rec = call(t, handler, http.MethodDelete, "/v1/tenants/"+tenantID, nil)
		assertStatus(t, rec, http.StatusNoContent)

		rec = call(t, handler, http.MethodGet, "/v1/tenants/"+tenantID, nil)
		assertStatus(t, rec, http.StatusNotFound)
		assertErrorCode(t, rec, "not_found")
	})

	t.Run("looks a tenant up by slug", func(t *testing.T) {
		handler, _ := newTestHandler(t)

		createTenant(t, handler, "acme", false)

		rec := call(t, handler, http.MethodGet, "/v1/tenants?slug=acme", nil)
		assertStatus(t, rec, http.StatusOK)
		if total := decodeBody(t, rec)["total"]; total != float64(1) {
			t.Errorf("total: got %v, want 1", total)
		}

		rec = call(t, handler, http.MethodGet, "/v1/tenants?slug=unknown", nil)
		assertStatus(t, rec, http.StatusOK)
		if total := decodeBody(t, rec)["total"]; total != float64(0) {
			t.Errorf("total: got %v, want 0", total)
		}
	})

	t.Run("rejects invalid pagination", func(t *testing.T) {
		handler, _ := newTestHandler(t)

		rec := call(t, handler, http.MethodGet, "/v1/tenants?limit=10000", nil)
		assertStatus(t, rec, http.StatusBadRequest)
		assertErrorCode(t, rec, "invalid_request")
	})
}

func TestMemberEndpoints(t *testing.T) {
	newMember := func(subject string) map[string]any {
		return map[string]any{
			"user": map[string]any{
				"provider": "openid-connect",
				"subject":  subject,
				"email":    subject + "@acme.tld",
			},
			"builtinRoles": []string{"member"},
		}
	}

	t.Run("adds, reads, updates and removes a member", func(t *testing.T) {
		handler, _ := newTestHandler(t)

		tenantID, _ := createTenant(t, handler, "acme", true)

		rec := call(t, handler, http.MethodPost, "/v1/tenants/"+tenantID+"/members", newMember("sub-member"))
		assertStatus(t, rec, http.StatusCreated)

		membershipID := decodeBody(t, rec)["id"].(string)

		rec = call(t, handler, http.MethodGet, "/v1/tenants/"+tenantID+"/members/"+membershipID, nil)
		assertStatus(t, rec, http.StatusOK)

		rec = call(t, handler, http.MethodPut, "/v1/tenants/"+tenantID+"/members/"+membershipID+"/roles",
			map[string]any{"builtinRoles": []string{"admin"}})
		assertStatus(t, rec, http.StatusOK)

		roles := decodeBody(t, rec)["roles"].([]any)
		if len(roles) != 1 || roles[0].(map[string]any)["builtinKind"] != "admin" {
			t.Errorf("roles: got %v", roles)
		}

		rec = call(t, handler, http.MethodGet, "/v1/tenants/"+tenantID+"/members", nil)
		assertStatus(t, rec, http.StatusOK)
		if total := decodeBody(t, rec)["total"]; total != float64(2) {
			t.Errorf("total: got %v, want 2", total)
		}

		rec = call(t, handler, http.MethodDelete, "/v1/tenants/"+tenantID+"/members/"+membershipID, nil)
		assertStatus(t, rec, http.StatusNoContent)
	})

	t.Run("reports a duplicate membership as a conflict", func(t *testing.T) {
		handler, _ := newTestHandler(t)

		tenantID, _ := createTenant(t, handler, "acme", true)

		rec := call(t, handler, http.MethodPost, "/v1/tenants/"+tenantID+"/members", newMember("sub-member"))
		assertStatus(t, rec, http.StatusCreated)

		rec = call(t, handler, http.MethodPost, "/v1/tenants/"+tenantID+"/members", newMember("sub-member"))
		assertStatus(t, rec, http.StatusConflict)
		assertErrorCode(t, rec, "conflict")
	})

	t.Run("refuses a role belonging to another tenant", func(t *testing.T) {
		handler, _ := newTestHandler(t)

		acmeID, _ := createTenant(t, handler, "acme", true)
		otherID, _ := createTenant(t, handler, "other", false)

		rec := call(t, handler, http.MethodGet, "/v1/tenants/"+otherID+"/roles", nil)
		assertStatus(t, rec, http.StatusOK)
		otherRoleID := decodeBody(t, rec)["items"].([]any)[0].(map[string]any)["id"].(string)

		rec = call(t, handler, http.MethodPost, "/v1/tenants/"+acmeID+"/members", newMember("sub-member"))
		assertStatus(t, rec, http.StatusCreated)
		membershipID := decodeBody(t, rec)["id"].(string)

		rec = call(t, handler, http.MethodPut, "/v1/tenants/"+acmeID+"/members/"+membershipID+"/roles",
			map[string]any{"roleIds": []string{otherRoleID}})
		assertStatus(t, rec, http.StatusUnprocessableEntity)
		assertErrorCode(t, rec, "unprocessable")
	})

	t.Run("refuses to drop the last owner", func(t *testing.T) {
		handler, _ := newTestHandler(t)

		tenantID, ownerMembershipID := createTenant(t, handler, "acme", true)

		rec := call(t, handler, http.MethodDelete, "/v1/tenants/"+tenantID+"/members/"+ownerMembershipID, nil)
		assertStatus(t, rec, http.StatusConflict)
		assertErrorCode(t, rec, "conflict")

		rec = call(t, handler, http.MethodPut, "/v1/tenants/"+tenantID+"/members/"+ownerMembershipID+"/roles",
			map[string]any{"builtinRoles": []string{"member"}})
		assertStatus(t, rec, http.StatusConflict)
	})

	t.Run("hides resources of another tenant", func(t *testing.T) {
		handler, _ := newTestHandler(t)

		acmeID, acmeMembershipID := createTenant(t, handler, "acme", true)
		otherID, _ := createTenant(t, handler, "other", false)

		rec := call(t, handler, http.MethodGet, "/v1/tenants/"+otherID+"/members/"+acmeMembershipID, nil)
		assertStatus(t, rec, http.StatusNotFound)
		assertErrorCode(t, rec, "not_found")

		rec = call(t, handler, http.MethodGet, "/v1/tenants/"+acmeID+"/members/nope", nil)
		assertStatus(t, rec, http.StatusNotFound)

		rec = call(t, handler, http.MethodGet, "/v1/tenants/nope/members", nil)
		assertStatus(t, rec, http.StatusNotFound)
	})
}

func TestRoleEndpoints(t *testing.T) {
	t.Run("creates, updates and deletes a custom role", func(t *testing.T) {
		handler, _ := newTestHandler(t)

		tenantID, _ := createTenant(t, handler, "acme", false)

		rec := call(t, handler, http.MethodPost, "/v1/tenants/"+tenantID+"/roles", map[string]any{
			"name":        "auditor",
			"permissions": []string{"usage:read"},
		})
		assertStatus(t, rec, http.StatusCreated)

		roleID := decodeBody(t, rec)["id"].(string)

		rec = call(t, handler, http.MethodPut, "/v1/tenants/"+tenantID+"/roles/"+roleID, map[string]any{
			"permissions": []string{"usage:read", "members:read"},
		})
		assertStatus(t, rec, http.StatusOK)
		if permissions := decodeBody(t, rec)["permissions"].([]any); len(permissions) != 2 {
			t.Errorf("permissions: got %v", permissions)
		}

		rec = call(t, handler, http.MethodDelete, "/v1/tenants/"+tenantID+"/roles/"+roleID, nil)
		assertStatus(t, rec, http.StatusNoContent)
	})

	t.Run("refuses an unknown permission", func(t *testing.T) {
		handler, _ := newTestHandler(t)

		tenantID, _ := createTenant(t, handler, "acme", false)

		rec := call(t, handler, http.MethodPost, "/v1/tenants/"+tenantID+"/roles", map[string]any{
			"name":        "bogus",
			"permissions": []string{"not:a:permission"},
		})
		assertStatus(t, rec, http.StatusUnprocessableEntity)
		assertErrorCode(t, rec, "unprocessable")
	})

	t.Run("refuses a duplicate role name", func(t *testing.T) {
		handler, _ := newTestHandler(t)

		tenantID, _ := createTenant(t, handler, "acme", false)

		rec := call(t, handler, http.MethodPost, "/v1/tenants/"+tenantID+"/roles", map[string]any{"name": "auditor"})
		assertStatus(t, rec, http.StatusCreated)

		rec = call(t, handler, http.MethodPost, "/v1/tenants/"+tenantID+"/roles", map[string]any{"name": "auditor"})
		assertStatus(t, rec, http.StatusConflict)
		assertErrorCode(t, rec, "conflict")
	})

	t.Run("protects builtin roles", func(t *testing.T) {
		handler, _ := newTestHandler(t)

		tenantID, _ := createTenant(t, handler, "acme", false)

		rec := call(t, handler, http.MethodGet, "/v1/tenants/"+tenantID+"/roles", nil)
		assertStatus(t, rec, http.StatusOK)

		var builtinID string
		for _, raw := range decodeBody(t, rec)["items"].([]any) {
			role := raw.(map[string]any)
			if role["builtinKind"] == "owner" {
				builtinID = role["id"].(string)
			}
		}
		if builtinID == "" {
			t.Fatal("no builtin owner role found")
		}

		rec = call(t, handler, http.MethodPut, "/v1/tenants/"+tenantID+"/roles/"+builtinID, map[string]any{"name": "hacked"})
		assertStatus(t, rec, http.StatusConflict)
		assertErrorCode(t, rec, "conflict")

		rec = call(t, handler, http.MethodDelete, "/v1/tenants/"+tenantID+"/roles/"+builtinID, nil)
		assertStatus(t, rec, http.StatusConflict)
	})
}

func TestUserEndpoints(t *testing.T) {
	identity := map[string]any{
		"provider":    "openid-connect",
		"subject":     "sub-1",
		"email":       "user@acme.tld",
		"displayName": "User",
	}

	t.Run("upserts a user idempotently", func(t *testing.T) {
		handler, _ := newTestHandler(t)

		rec := call(t, handler, http.MethodPut, "/v1/users", identity)
		assertStatus(t, rec, http.StatusCreated)
		userID := decodeBody(t, rec)["id"].(string)

		rec = call(t, handler, http.MethodPut, "/v1/users", identity)
		assertStatus(t, rec, http.StatusOK)
		if id := decodeBody(t, rec)["id"]; id != userID {
			t.Errorf("id: got %v, want %q", id, userID)
		}
	})

	t.Run("looks a user up by identity", func(t *testing.T) {
		handler, _ := newTestHandler(t)

		call(t, handler, http.MethodPut, "/v1/users", identity)

		rec := call(t, handler, http.MethodGet, "/v1/users?provider=openid-connect&subject=sub-1", nil)
		assertStatus(t, rec, http.StatusOK)
		if total := decodeBody(t, rec)["total"]; total != float64(1) {
			t.Errorf("total: got %v, want 1", total)
		}

		rec = call(t, handler, http.MethodGet, "/v1/users?provider=openid-connect&subject=unknown", nil)
		assertStatus(t, rec, http.StatusOK)
		if total := decodeBody(t, rec)["total"]; total != float64(0) {
			t.Errorf("total: got %v, want 0", total)
		}

		rec = call(t, handler, http.MethodGet, "/v1/users?provider=openid-connect", nil)
		assertStatus(t, rec, http.StatusBadRequest)
		assertErrorCode(t, rec, "invalid_request")
	})

	t.Run("updates and reads a user", func(t *testing.T) {
		handler, _ := newTestHandler(t)

		rec := call(t, handler, http.MethodPut, "/v1/users", identity)
		userID := decodeBody(t, rec)["id"].(string)

		rec = call(t, handler, http.MethodPatch, "/v1/users/"+userID, map[string]any{"displayName": "Renamed"})
		assertStatus(t, rec, http.StatusOK)
		if name := decodeBody(t, rec)["displayName"]; name != "Renamed" {
			t.Errorf("display name: got %v", name)
		}

		rec = call(t, handler, http.MethodGet, "/v1/users/"+userID, nil)
		assertStatus(t, rec, http.StatusOK)

		rec = call(t, handler, http.MethodGet, "/v1/users/nope", nil)
		assertStatus(t, rec, http.StatusNotFound)
	})

	t.Run("filters on the active flag", func(t *testing.T) {
		handler, _ := newTestHandler(t)

		active := false
		call(t, handler, http.MethodPut, "/v1/users", identity)
		call(t, handler, http.MethodPut, "/v1/users", map[string]any{
			"provider": "openid-connect",
			"subject":  "sub-pending",
			"email":    "pending@acme.tld",
			"active":   active,
		})

		rec := call(t, handler, http.MethodGet, "/v1/users?active=false", nil)
		assertStatus(t, rec, http.StatusOK)

		items := decodeBody(t, rec)["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("inactive users: got %d, want 1", len(items))
		}
		if email := items[0].(map[string]any)["email"]; email != "pending@acme.tld" {
			t.Errorf("email: got %v", email)
		}

		rec = call(t, handler, http.MethodGet, "/v1/users?active=true", nil)
		assertStatus(t, rec, http.StatusOK)
		if total := decodeBody(t, rec)["total"]; total != float64(1) {
			t.Errorf("active users: got %v, want 1", total)
		}

		rec = call(t, handler, http.MethodGet, "/v1/users?active=maybe", nil)
		assertStatus(t, rec, http.StatusBadRequest)
		assertErrorCode(t, rec, "invalid_request")
	})

	t.Run("never exposes platform role management", func(t *testing.T) {
		handler, _ := newTestHandler(t)

		rec := call(t, handler, http.MethodPut, "/v1/users", identity)
		userID := decodeBody(t, rec)["id"].(string)

		rec = call(t, handler, http.MethodPatch, "/v1/users/"+userID, map[string]any{"platformRoles": []string{"admin"}})
		assertStatus(t, rec, http.StatusBadRequest)
		assertErrorCode(t, rec, "invalid_request")

		rec = call(t, handler, http.MethodGet, "/v1/users/"+userID, nil)
		roles := decodeBody(t, rec)["platformRoles"].([]any)
		if len(roles) != 1 || roles[0] != "user" {
			t.Errorf("platform roles: got %v, want [user]", roles)
		}
	})
}

func TestRoutingFallbacks(t *testing.T) {
	handler, _ := newTestHandler(t)

	rec := call(t, handler, http.MethodGet, "/v1/unknown", nil)
	assertStatus(t, rec, http.StatusNotFound)
	assertErrorCode(t, rec, "not_found")

	rec = call(t, handler, http.MethodDelete, "/v1/permissions", nil)
	assertStatus(t, rec, http.StatusMethodNotAllowed)
}

// TestInternalErrorsDoNotLeak checks that an unexpected failure is reported as
// a generic error: no SQL, no file path, no stack trace.
func TestInternalErrorsDoNotLeak(t *testing.T) {
	handler, db := newTestHandler(t)

	createTenant(t, handler, "acme", false)

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	rec := call(t, handler, http.MethodGet, "/v1/tenants", nil)
	assertStatus(t, rec, http.StatusInternalServerError)
	assertErrorCode(t, rec, "internal_error")

	body := rec.Body.String()
	for _, leak := range []string{"sql", "SELECT", "gorm", ".go:", "/home/", "database"} {
		if strings.Contains(body, leak) {
			t.Errorf("error body should not contain %q, got %s", leak, body)
		}
	}
}
