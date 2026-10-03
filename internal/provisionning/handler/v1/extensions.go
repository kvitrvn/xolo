package v1

import "net/http"

func (h *Handler) handleExtensions(w http.ResponseWriter, r *http.Request) {
	extensions := []any{
		map[string]any{"name": "identity", "version": "1", "scope": "tenant", "path": "/v1/tenants/{tenantID}/members/{memberID}", "issuer": "exact HTTPS URL", "subject": "exact UTF-8, 1..255 bytes"},
		map[string]any{"name": "ownership", "version": "1", "policy": h.provisioning.OwnershipPolicy()},
		map[string]any{"name": "adoption", "version": "1", "path": "/v1/xolo/export", "format": "xolo-adoption/1"},
	}
	if h.webhooksEnabled {
		extensions = append(extensions, map[string]any{"name": "webhooks", "version": "1", "scope": "tenant", "owner": h.provisioning.OwnershipPolicy()["subscription"], "path": "/v1/xolo/tenants/{tenantID}/webhooks"})
	}
	writeJSON(w, 200, map[string]any{"extensions": extensions})
}
func (h *Handler) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, 400, "invalid_parameter", "unsupported query parameter")
		return
	}
	raw, err := h.provisioning.ExportAdoption(r.Context())
	if err != nil {
		writeServiceError(r.Context(), w, err, "could not export inventory")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="xolo-adoption.json"`)
	w.WriteHeader(200)
	_, _ = w.Write(raw)
}
