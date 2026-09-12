package oidc

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/bornholm/go-x/slogx"
	"github.com/gorilla/sessions"
	"github.com/pkg/errors"
	httpCtx "github.com/xolo-gateway/xolo/internal/http/context"
	"github.com/xolo-gateway/xolo/internal/http/middleware/authn/oauth2token"
	"github.com/xolo-gateway/xolo/internal/http/middleware/authn/oidctoken"
)

type ProviderWithJWKS struct {
	ID          string
	Label      string
	Icon       string
	DiscoveryURL string
	Issuer      string
	JWKSURL     string
	// IntrospectionURL, ClientID and ClientSecret, when set, enable RFC 7662
	// access-token introspection for this provider (see ProvidersWithIntrospection).
	IntrospectionURL string
	ClientID         string
	ClientSecret     string
	// UserInfoURL, when set, validates opaque access tokens (when no
	// introspection endpoint is available) and enriches introspected identities
	// missing an email or display name.
	UserInfoURL string
	// RequiredScope / RequiredAudience are per-provider requirements enforced on
	// the introspection path.
	RequiredScope    string
	RequiredAudience string
}

type Handler struct {
	mux              *http.ServeMux
	sessionStore     sessions.Store
	sessionName      string
	providers       []Provider
	providersWithJWKS []ProviderWithJWKS
	resolveProvider  ProviderResolver
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func NewHandler(sessionStore sessions.Store, funcs ...OptionFunc) *Handler {
	opts := NewOptions(funcs...)
	h := &Handler{
		mux:              http.NewServeMux(),
		sessionStore:     sessionStore,
		sessionName:      opts.SessionName,
		providers:       opts.Providers,
		providersWithJWKS: opts.ProvidersWithJWKS,
		resolveProvider:  opts.ResolveProvider,
	}

	h.mux.HandleFunc("GET /login", h.getLoginPage)
	h.mux.Handle("GET /providers/{provider}", h.withContextProvider(http.HandlerFunc(h.handleProvider)))
	h.mux.Handle("GET /providers/{provider}/callback", h.withContextProvider(http.HandlerFunc(h.handleProviderCallback)))
	h.mux.HandleFunc("GET /logout", h.handleLogout)
	h.mux.Handle("GET /providers/{provider}/logout", h.withContextProvider(http.HandlerFunc(h.handleProviderLogout)))

	return h
}

func (h *Handler) ProvidersWithJWKS() []oidctoken.Provider {
	providers := make([]oidctoken.Provider, 0, len(h.providersWithJWKS))
	for _, p := range h.providersWithJWKS {
		providers = append(providers, oidctoken.Provider{
			ID:          p.ID,
			Label:      p.Label,
			Icon:       p.Icon,
			DiscoveryURL: p.DiscoveryURL,
			Issuer:      p.Issuer,
			JWKSURL:     p.JWKSURL,
		})
	}
	return providers
}

// ProvidersForTokenValidation returns, for each configured provider able to
// validate an incoming opaque access token — i.e. exposing an introspection
// endpoint (preferred) or a userinfo endpoint (fallback, e.g. Auth0) — an
// oauth2token.Provider keyed on the same provider ID used for interactive logins.
func (h *Handler) ProvidersForTokenValidation() []oauth2token.Provider {
	providers := make([]oauth2token.Provider, 0, len(h.providersWithJWKS))
	for _, p := range h.providersWithJWKS {
		hasIntrospection := p.IntrospectionURL != "" && p.ClientID != ""
		if !hasIntrospection && p.UserInfoURL == "" {
			continue
		}
		providers = append(providers, oauth2token.Provider{
			ID:               p.ID,
			IntrospectionURL: p.IntrospectionURL,
			ClientID:         p.ClientID,
			ClientSecret:     p.ClientSecret,
			UserInfoURL:      p.UserInfoURL,
			RequiredScope:    p.RequiredScope,
			RequiredAudience: p.RequiredAudience,
		})
	}
	return providers
}

var _ http.Handler = &Handler{}

// withContextProvider names the goth provider the request must be served by.
// gothic reads that context value before the route parameter, which is what
// lets a multi-tenant instance answer on a host-scoped provider while the route
// keeps the bare provider ID.
func (h *Handler) withContextProvider(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		provider := r.PathValue("provider")

		if h.resolveProvider != nil {
			resolved, err := h.resolveProvider(provider, httpCtx.BaseURL(r.Context()).String())
			if err != nil {
				// An unknown provider on an otherwise valid host is a 404, like any
				// route that does not exist: the ID comes straight from the URL.
				slog.WarnContext(r.Context(), "could not resolve oidc provider",
					slog.String("provider", provider), slogx.Error(errors.WithStack(err)))
				http.NotFound(w, r)

				return
			}

			provider = resolved
		}

		r = r.WithContext(context.WithValue(r.Context(), "provider", provider))
		next.ServeHTTP(w, r)
	}

	return http.HandlerFunc(fn)
}
