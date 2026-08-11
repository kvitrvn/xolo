package v1

import (
	"net/http"

	"github.com/bornholm/xolo/internal/core/model"
	"github.com/bornholm/xolo/internal/core/service"
)

func (h *Handler) handleListRoles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	roles, err := h.provisioning.ListRoles(ctx, model.OrgID(r.PathValue("tenantID")))
	if err != nil {
		writeServiceError(ctx, w, err, "tenant not found")
		return
	}

	items := make([]roleDTO, 0, len(roles))
	for _, role := range roles {
		items = append(items, newRoleDTO(role))
	}

	// Roles are not paginated: an organization holds a handful of them.
	writeJSON(w, http.StatusOK, newListDTO(items, 1, len(items), int64(len(items))))
}

func (h *Handler) handleCreateRole(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var payload roleRequest
	if !decodeJSON(w, r, &payload) {
		return
	}

	role, err := h.provisioning.CreateRole(ctx, model.OrgID(r.PathValue("tenantID")), service.RoleParams{
		Name:        payload.Name,
		Description: payload.Description,
		Permissions: payload.Permissions,
		ModelGrants: toModelGrants(payload.ModelGrants),
	})
	if err != nil {
		writeServiceError(ctx, w, err, "could not create role")
		return
	}

	writeJSON(w, http.StatusCreated, newRoleDTO(role))
}

func (h *Handler) handleGetRole(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	role, err := h.provisioning.GetRole(ctx,
		model.OrgID(r.PathValue("tenantID")),
		model.RoleID(r.PathValue("roleID")),
	)
	if err != nil {
		writeServiceError(ctx, w, err, "role not found")
		return
	}

	writeJSON(w, http.StatusOK, newRoleDTO(role))
}

func (h *Handler) handleUpdateRole(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var payload roleRequest
	if !decodeJSON(w, r, &payload) {
		return
	}

	role, err := h.provisioning.UpdateRole(ctx,
		model.OrgID(r.PathValue("tenantID")),
		model.RoleID(r.PathValue("roleID")),
		service.RoleParams{
			Name:        payload.Name,
			Description: payload.Description,
			Permissions: payload.Permissions,
			ModelGrants: toModelGrants(payload.ModelGrants),
		},
	)
	if err != nil {
		writeServiceError(ctx, w, err, "role not found")
		return
	}

	writeJSON(w, http.StatusOK, newRoleDTO(role))
}

func (h *Handler) handleDeleteRole(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	err := h.provisioning.DeleteRole(ctx,
		model.OrgID(r.PathValue("tenantID")),
		model.RoleID(r.PathValue("roleID")),
	)
	if err != nil {
		writeServiceError(ctx, w, err, "role not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
