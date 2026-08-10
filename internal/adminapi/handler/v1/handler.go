package v1

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/bornholm/xolo/internal/core/service"
)

const (
	defaultPageLimit = 50
	maxPageLimit     = 200

	// maxRequestBodySize caps the payloads this API accepts. Its resources are
	// small: anything larger is a mistake.
	maxRequestBodySize = 1 << 20
)

// Handler exposes the provisioning operations as a versioned, resource
// oriented HTTP API.
//
// It is pure transport: it decodes and validates the request, delegates to the
// provisioning service, converts the result to an API representation and maps
// domain errors to HTTP statuses. It holds no store and no business rule.
type Handler struct {
	provisioning *service.ProvisioningService
	mux          *http.ServeMux
}

func NewHandler(provisioning *service.ProvisioningService) *Handler {
	h := &Handler{
		provisioning: provisioning,
		mux:          http.NewServeMux(),
	}

	h.mux.HandleFunc("GET /v1/healthz", h.handleHealthz)
	h.mux.HandleFunc("GET /v1/permissions", h.handlePermissions)

	h.mux.HandleFunc("GET /v1/tenants", h.handleListTenants)
	h.mux.HandleFunc("POST /v1/tenants", h.handleCreateTenant)
	h.mux.HandleFunc("GET /v1/tenants/{tenantID}", h.handleGetTenant)
	h.mux.HandleFunc("PATCH /v1/tenants/{tenantID}", h.handleUpdateTenant)
	h.mux.HandleFunc("DELETE /v1/tenants/{tenantID}", h.handleDeleteTenant)

	h.mux.HandleFunc("GET /v1/tenants/{tenantID}/members", h.handleListMembers)
	h.mux.HandleFunc("POST /v1/tenants/{tenantID}/members", h.handleAddMember)
	h.mux.HandleFunc("GET /v1/tenants/{tenantID}/members/{membershipID}", h.handleGetMember)
	h.mux.HandleFunc("DELETE /v1/tenants/{tenantID}/members/{membershipID}", h.handleRemoveMember)
	h.mux.HandleFunc("PUT /v1/tenants/{tenantID}/members/{membershipID}/roles", h.handleSetMemberRoles)

	h.mux.HandleFunc("GET /v1/tenants/{tenantID}/roles", h.handleListRoles)
	h.mux.HandleFunc("POST /v1/tenants/{tenantID}/roles", h.handleCreateRole)
	h.mux.HandleFunc("GET /v1/tenants/{tenantID}/roles/{roleID}", h.handleGetRole)
	h.mux.HandleFunc("PUT /v1/tenants/{tenantID}/roles/{roleID}", h.handleUpdateRole)
	h.mux.HandleFunc("DELETE /v1/tenants/{tenantID}/roles/{roleID}", h.handleDeleteRole)

	h.mux.HandleFunc("GET /v1/users", h.handleListUsers)
	h.mux.HandleFunc("PUT /v1/users", h.handlePutUser)
	h.mux.HandleFunc("GET /v1/users/{userID}", h.handleGetUser)
	h.mux.HandleFunc("PATCH /v1/users/{userID}", h.handleUpdateUser)

	// Catch-all so an unknown route answers with the same error envelope as
	// everything else.
	h.mux.HandleFunc("/", h.handleNotFound)

	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handlePermissions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"groups": newPermissionCatalogDTO()})
}

// routableMethods are the methods this API uses. They are probed to tell an
// unknown resource apart from a known one addressed with the wrong method.
var routableMethods = []string{
	http.MethodGet,
	http.MethodPost,
	http.MethodPut,
	http.MethodPatch,
	http.MethodDelete,
}

// handleNotFound serves every request no route matched. Registering it makes
// the catch-all shadow the mux's own 404 and 405 replies, so it re-derives the
// distinction to keep a single error envelope across the whole API.
func (h *Handler) handleNotFound(w http.ResponseWriter, r *http.Request) {
	allowed := make([]string, 0, len(routableMethods))

	for _, method := range routableMethods {
		if method == r.Method {
			continue
		}

		probe := r.Clone(r.Context())
		probe.Method = method

		if _, pattern := h.mux.Handler(probe); pattern != "" && pattern != "/" {
			allowed = append(allowed, method)
		}
	}

	if len(allowed) > 0 {
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		writeError(w, http.StatusMethodNotAllowed, codeMethodNotAllowed, "method not allowed for this resource")

		return
	}

	writeError(w, http.StatusNotFound, codeNotFound, "unknown resource")
}

// decodeJSON reads a JSON request body. Unknown fields are rejected: a
// declarative client that misspells a field must be told, not silently ignored.
func decodeJSON(w http.ResponseWriter, r *http.Request, payload any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBodySize))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(payload); err != nil {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "request body is not a valid JSON document for this endpoint")
		return false
	}

	return true
}

// pagination reads the page/limit query parameters. Pages are 1-based on the
// wire and 0-based in the domain.
func pagination(r *http.Request) (page, limit int, ok bool) {
	page, limit = 1, defaultPageLimit

	if raw := r.URL.Query().Get("page"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return 0, 0, false
		}
		page = parsed
	}

	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxPageLimit {
			return 0, 0, false
		}
		limit = parsed
	}

	return page, limit, true
}

func writeInvalidPagination(w http.ResponseWriter) {
	writeError(w, http.StatusBadRequest, codeInvalidRequest,
		"page must be a positive integer and limit must be between 1 and "+strconv.Itoa(maxPageLimit))
}
