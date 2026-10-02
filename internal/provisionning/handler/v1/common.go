package v1

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"unicode/utf8"

	"github.com/xolo-gateway/xolo/internal/core/model"
	"github.com/xolo-gateway/xolo/internal/core/service"
)

const ContractVersion = "0.1.0-draft.1"

type resourceRepresentation struct {
	Slug   string       `json:"slug"`
	Name   string       `json:"name"`
	Status model.Status `json:"status"`
}
type memberRepresentation struct {
	Email       string           `json:"email"`
	DisplayName string           `json:"display_name,omitempty"`
	TenantRole  model.TenantRole `json:"tenant_role"`
	Status      model.Status     `json:"status"`
}
type membershipRepresentation struct {
	Role   model.MembershipRole `json:"role"`
	Status model.Status         `json:"status"`
}
type domainRepresentation struct {
	Status model.Status `json:"status"`
}

// Read the entire bounded body before decoding: trailing whitespace counts
// toward the size limit. RawMessage preserves null/missing/type distinctions.
func decodeCommon(w http.ResponseWriter, r *http.Request, required []string, optional ...string) (map[string]string, bool) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeError(w, 415, "unsupported_media_type", "application/json is required")
		return nil, false
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodySize+1))
	if err != nil || len(raw) > maxRequestBodySize || !utf8.Valid(raw) {
		writeError(w, 400, "invalid_json", "invalid JSON body")
		return nil, false
	}
	var obj map[string]json.RawMessage
	if err = json.Unmarshal(raw, &obj); err != nil {
		writeError(w, 400, "invalid_json", "expected one JSON object")
		return nil, false
	}
	if obj == nil {
		writeError(w, 400, "invalid_representation", "null is not a representation")
		return nil, false
	}
	allowed := map[string]bool{}
	for _, k := range append(append([]string{}, required...), optional...) {
		allowed[k] = true
	}
	values := map[string]string{}
	for k, v := range obj {
		if !allowed[k] {
			writeError(w, 400, "invalid_json", "unknown field")
			return nil, false
		}
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			writeError(w, 400, "invalid_representation", "null fields are not allowed")
			return nil, false
		}
		var text string
		if json.Unmarshal(v, &text) != nil {
			writeError(w, 400, "invalid_json", "fields must be strings")
			return nil, false
		}
		// encoding/json replaces unpaired UTF-16 surrogates. Reject them before
		// that lossy conversion while accepting a genuine U+FFFD character.
		if !validJSONString(v) {
			writeError(w, 400, "invalid_json", "invalid Unicode string")
			return nil, false
		}
		values[k] = text
	}
	for _, k := range required {
		if _, ok := values[k]; !ok {
			writeError(w, 400, "invalid_representation", "required field missing")
			return nil, false
		}
	}
	return values, true
}
func validJSONString(v []byte) bool {
	// JSON validity has already been checked; inspect escaped code units only.
	for i := 1; i < len(v)-1; i++ {
		if v[i] != '\\' {
			continue
		}
		i++
		if v[i] != 'u' {
			continue
		}
		n := hexUnit(v[i+1 : i+5])
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(v) || v[i+1] != '\\' || v[i+2] != 'u' {
				return false
			}
			low := hexUnit(v[i+3 : i+7])
			if low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
func hexUnit(b []byte) int {
	n := 0
	for _, c := range b {
		n *= 16
		switch {
		case c >= '0' && c <= '9':
			n += int(c - '0')
		case c >= 'a' && c <= 'f':
			n += int(c-'a') + 10
		default:
			n += int(c-'A') + 10
		}
	}
	return n
}

func (h *Handler) handleManifest(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"name": "Xolo", "version": h.version, "contract_version": ContractVersion})
}
func (h *Handler) handleCommonResource(w http.ResponseWriter, r *http.Request) {
	p, ok := decodeCommon(w, r, []string{"slug", "name", "status"})
	if !ok {
		return
	}
	input := service.CommonResource{Slug: p["slug"], Name: p["name"], Status: model.Status(p["status"])}
	var out service.CommonResource
	var err error
	if r.PathValue("orgID") == "" {
		out, err = h.provisioning.PutCommonTenant(r.Context(), model.TenantID(r.PathValue("tenantID")), input)
	} else {
		out, err = h.provisioning.PutCommonOrganization(r.Context(), model.TenantID(r.PathValue("tenantID")), model.OrgID(r.PathValue("orgID")), input)
	}
	if err != nil {
		writeServiceError(r.Context(), w, err, "could not write resource")
		return
	}
	writeJSON(w, 200, resourceRepresentation{out.Slug, out.Name, out.Status})
}
func (h *Handler) handleCommonMember(w http.ResponseWriter, r *http.Request) {
	p, ok := decodeCommon(w, r, []string{"email", "tenant_role", "status"}, "display_name")
	if !ok {
		return
	}
	out, err := h.provisioning.PutCommonMember(r.Context(), model.TenantID(r.PathValue("tenantID")), model.UserID(r.PathValue("memberID")), service.CommonMember{Email: p["email"], DisplayName: p["display_name"], TenantRole: model.TenantRole(p["tenant_role"]), Status: model.Status(p["status"])})
	if err != nil {
		writeServiceError(r.Context(), w, err, "could not write member")
		return
	}
	writeJSON(w, 200, memberRepresentation{out.Email, out.DisplayName, out.TenantRole, out.Status})
}
func (h *Handler) handleCommonDomain(w http.ResponseWriter, r *http.Request) {
	p, ok := decodeCommon(w, r, []string{"status"})
	if !ok {
		return
	}
	out, err := h.provisioning.PutCommonDomain(r.Context(), model.TenantID(r.PathValue("tenantID")), r.PathValue("hostname"), model.Status(p["status"]))
	if err != nil {
		writeServiceError(r.Context(), w, err, "could not write domain")
		return
	}
	writeJSON(w, 200, domainRepresentation{out})
}
func (h *Handler) handleCommonMembership(w http.ResponseWriter, r *http.Request) {
	p, ok := decodeCommon(w, r, []string{"role", "status"})
	if !ok {
		return
	}
	out, err := h.provisioning.PutCommonMembership(r.Context(), model.TenantID(r.PathValue("tenantID")), model.OrgID(r.PathValue("orgID")), model.UserID(r.PathValue("memberID")), service.CommonMembership{Role: model.MembershipRole(p["role"]), Status: model.Status(p["status"])})
	if err != nil {
		writeServiceError(r.Context(), w, err, "could not write membership")
		return
	}
	writeJSON(w, 200, membershipRepresentation{out.Role, out.Status})
}
