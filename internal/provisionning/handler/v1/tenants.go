package v1

import (
	"net/http"

	"github.com/bornholm/xolo/internal/core/model"
	"github.com/bornholm/xolo/internal/core/port"
	"github.com/bornholm/xolo/internal/core/service"
	"github.com/pkg/errors"
)

type createTenantResponse struct {
	Tenant tenantDTO `json:"tenant"`

	// Owner and Membership are present only when an initial owner was
	// requested. OwnerCreated tells an external reconciler whether the identity
	// already existed.
	Owner        *userDTO       `json:"owner,omitempty"`
	Membership   *membershipDTO `json:"ownerMembership,omitempty"`
	OwnerCreated bool           `json:"ownerCreated"`
}

func (h *Handler) handleListTenants(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// An exact slug lookup lets a reconciler read back the state it just
	// declared without knowing the generated identifier.
	if slug := r.URL.Query().Get("slug"); slug != "" {
		org, err := h.provisioning.GetTenantBySlug(ctx, slug)
		if err != nil {
			if errors.Is(err, port.ErrNotFound) {
				writeJSON(w, http.StatusOK, newListDTO([]tenantDTO{}, 1, 1, 0))
				return
			}
			writeServiceError(ctx, w, err, "tenant not found")
			return
		}

		writeJSON(w, http.StatusOK, newListDTO([]tenantDTO{newTenantDTO(org)}, 1, 1, 1))

		return
	}

	page, limit, ok := pagination(r)
	if !ok {
		writeInvalidPagination(w)
		return
	}

	offset := page - 1

	orgs, total, err := h.provisioning.ListTenants(ctx, port.ListOrgsOptions{Page: &offset, Limit: &limit})
	if err != nil {
		writeServiceError(ctx, w, err, "could not list tenants")
		return
	}

	items := make([]tenantDTO, 0, len(orgs))
	for _, org := range orgs {
		items = append(items, newTenantDTO(org))
	}

	writeJSON(w, http.StatusOK, newListDTO(items, page, limit, total))
}

func (h *Handler) handleCreateTenant(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var payload createTenantRequest
	if !decodeJSON(w, r, &payload) {
		return
	}

	params := service.CreateTenantParams{
		Slug:        payload.Slug,
		Name:        payload.Name,
		Description: payload.Description,
		Currency:    payload.Currency,
		Active:      payload.Active,
	}

	if payload.Owner != nil {
		owner := toIdentityParams(*payload.Owner)
		params.Owner = &owner
	}

	result, err := h.provisioning.CreateTenant(ctx, params)
	if err != nil {
		writeServiceError(ctx, w, err, "could not create tenant")
		return
	}

	response := createTenantResponse{
		Tenant:       newTenantDTO(result.Org),
		OwnerCreated: result.OwnerCreated,
	}

	if result.Owner != nil {
		owner := newUserDTO(result.Owner)
		response.Owner = &owner
	}
	if result.OwnerMembership != nil {
		membership := newMembershipDTO(result.OwnerMembership)
		response.Membership = &membership
	}

	writeJSON(w, http.StatusCreated, response)
}

func (h *Handler) handleGetTenant(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	org, err := h.provisioning.GetTenant(ctx, model.OrgID(r.PathValue("tenantID")))
	if err != nil {
		writeServiceError(ctx, w, err, "tenant not found")
		return
	}

	writeJSON(w, http.StatusOK, newTenantDTO(org))
}

func (h *Handler) handleUpdateTenant(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var payload updateTenantRequest
	if !decodeJSON(w, r, &payload) {
		return
	}

	org, err := h.provisioning.UpdateTenant(ctx, model.OrgID(r.PathValue("tenantID")), service.UpdateTenantParams{
		Name:              payload.Name,
		Description:       payload.Description,
		Active:            payload.Active,
		Currency:          payload.Currency,
		ShareQuotaEqually: payload.ShareQuotaEqually,
	})
	if err != nil {
		writeServiceError(ctx, w, err, "tenant not found")
		return
	}

	writeJSON(w, http.StatusOK, newTenantDTO(org))
}

func (h *Handler) handleDeleteTenant(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.provisioning.DeleteTenant(ctx, model.OrgID(r.PathValue("tenantID"))); err != nil {
		writeServiceError(ctx, w, err, "tenant not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func toIdentityParams(payload userIdentityRequest) service.UserIdentityParams {
	return service.UserIdentityParams{
		Provider:    payload.Provider,
		Subject:     payload.Subject,
		Email:       payload.Email,
		DisplayName: payload.DisplayName,
		Active:      payload.Active,
	}
}
