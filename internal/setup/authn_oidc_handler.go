package setup

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/markbates/goth/providers/gitea"
	"github.com/markbates/goth/providers/github"
	"github.com/markbates/goth/providers/google"
	"github.com/markbates/goth/providers/openidConnect"
	"github.com/pkg/errors"
	"github.com/xolo-gateway/xolo/internal/config"
	"github.com/xolo-gateway/xolo/internal/http/middleware/authn/oidc"
)

type OIDCDiscovery struct {
	Issuer                string `json:"issuer"`
	JWKSURI               string `json:"jwks_uri"`
	AuthURL               string `json:"authorization_endpoint"`
	TokenURL              string `json:"token_endpoint"`
	UserInfoEndpoint      string `json:"userinfo_endpoint"`
	IntrospectionEndpoint string `json:"introspection_endpoint"`
	EndSessionEndpoint    string `json:"end_session_endpoint"`
}

// oidcProviderFactory builds a goth provider bound to one callback URL. The
// callback URL is the only part of a provider that varies between tenants, so
// providers are described as factories rather than instantiated once: a
// multi-tenant instance needs one instance per hostname it serves, because a
// goth provider freezes its redirect URI at construction.
type oidcProviderFactory func(callbackURL string) (goth.Provider, error)

// oidcCallbackURL is the redirect URI a provider is registered under. It has to
// match, byte for byte, the route the handler mounts and the URI declared at
// the identity provider.
func oidcCallbackURL(baseURL string, providerID string) string {
	return fmt.Sprintf("%s/auth/oidc/providers/%s/callback", baseURL, providerID)
}

func getOIDCAuthnHandlerFromConfig(ctx context.Context, conf *config.Config) (*oidc.Handler, error) {
	sessionStore, err := getSessionStoreFromConfig(ctx, conf)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	// Configure providers

	factories := make(map[string]oidcProviderFactory)
	providers := make([]oidc.Provider, 0)
	providersWithJWKS := make([]oidc.ProviderWithJWKS, 0)

	if conf.HTTP.Authn.Providers.Google.Key != "" && conf.HTTP.Authn.Providers.Google.Secret != "" {
		key := string(conf.HTTP.Authn.Providers.Google.Key)
		secret := string(conf.HTTP.Authn.Providers.Google.Secret)
		scopes := conf.HTTP.Authn.Providers.Google.Scopes

		factories["google"] = func(callbackURL string) (goth.Provider, error) {
			return google.New(key, secret, callbackURL, scopes...), nil
		}

		providers = append(providers, oidc.Provider{
			ID:    "google",
			Label: "Google",
			Icon:  "log-in",
		})

		providersWithJWKS = append(providersWithJWKS, oidc.ProviderWithJWKS{
			ID:      "google",
			Label:   "Google",
			Icon:    "log-in",
			Issuer:  "https://accounts.google.com",
			JWKSURL: "https://www.googleapis.com/oauth2/v3/certs",
		})
	}

	if conf.HTTP.Authn.Providers.Github.Key != "" && conf.HTTP.Authn.Providers.Github.Secret != "" {
		key := string(conf.HTTP.Authn.Providers.Github.Key)
		secret := string(conf.HTTP.Authn.Providers.Github.Secret)
		scopes := conf.HTTP.Authn.Providers.Github.Scopes

		factories["github"] = func(callbackURL string) (goth.Provider, error) {
			return github.New(key, secret, callbackURL, scopes...), nil
		}

		providers = append(providers, oidc.Provider{
			ID:    "github",
			Label: "Github",
			Icon:  "github",
		})

		issuer := "https://github.com"
		if conf.HTTP.BaseURL != "" && conf.HTTP.BaseURL != "/" {
			issuer = conf.HTTP.BaseURL
		}
		providersWithJWKS = append(providersWithJWKS, oidc.ProviderWithJWKS{
			ID:      "github",
			Label:   "Github",
			Icon:    "github",
			Issuer:  issuer,
			JWKSURL: "https://token.actions.githubusercontent.com/.well-known/jwks",
		})
	}

	if conf.HTTP.Authn.Providers.Gitea.Key != "" && conf.HTTP.Authn.Providers.Gitea.Secret != "" {
		key := string(conf.HTTP.Authn.Providers.Gitea.Key)
		secret := string(conf.HTTP.Authn.Providers.Gitea.Secret)
		scopes := conf.HTTP.Authn.Providers.Gitea.Scopes
		authURL := string(conf.HTTP.Authn.Providers.Gitea.AuthURL)
		tokenURL := string(conf.HTTP.Authn.Providers.Gitea.TokenURL)
		profileURL := string(conf.HTTP.Authn.Providers.Gitea.ProfileURL)

		factories["gitea"] = func(callbackURL string) (goth.Provider, error) {
			return gitea.NewCustomisedURL(key, secret, callbackURL, authURL, tokenURL, profileURL, scopes...), nil
		}

		providers = append(providers, oidc.Provider{
			ID:    "gitea",
			Label: string(conf.HTTP.Authn.Providers.Gitea.Label),
			Icon:  "gitlab",
		})

		discoveryURL := string(conf.HTTP.Authn.Providers.Gitea.DiscoveryURL)

		discovery, err := fetchOIDCDiscovery(ctx, discoveryURL)
		if err == nil && discovery != nil && discovery.JWKSURI != "" {
			providersWithJWKS = append(providersWithJWKS, oidc.ProviderWithJWKS{
				ID:               "gitea",
				Label:            string(conf.HTTP.Authn.Providers.Gitea.Label),
				Icon:             "gitlab",
				Issuer:           discovery.Issuer,
				JWKSURL:          discovery.JWKSURI,
				IntrospectionURL: discovery.IntrospectionEndpoint,
				UserInfoURL:      discovery.UserInfoEndpoint,
				ClientID:         key,
				ClientSecret:     secret,
			})
		}
	}

	for _, np := range conf.HTTP.Authn.OIDCProviders {
		if np.Key == "" || np.Secret == "" {
			continue
		}

		factory, provider, withJWKS, err := buildOIDCProvider(ctx, np)
		if err != nil {
			return nil, errors.Wrapf(err, "could not configure oidc provider %q", np.ID)
		}

		factories[np.ID] = factory
		providers = append(providers, provider)
		if withJWKS != nil {
			providersWithJWKS = append(providersWithJWKS, *withJWKS)
		}
	}

	opts := []oidc.OptionFunc{
		oidc.WithProviders(providers...),
		oidc.WithProvidersWithJWKS(providersWithJWKS),
	}

	if conf.Multitenancy.Enabled {
		// Each tenant is served on its own hostname, so each needs its own
		// redirect URI: a provider registered once at startup would send every
		// tenant back to a single host, where its session — bound to both the
		// hostname and the tenant — could not be used.
		opts = append(opts, oidc.WithProviderResolver(newHostScopedProviders(factories).Resolve))
	} else {
		gothProviders := make([]goth.Provider, 0, len(factories))

		for id, factory := range factories {
			gothProvider, err := factory(oidcCallbackURL(conf.HTTP.BaseURL, id))
			if err != nil {
				return nil, errors.Wrapf(err, "could not configure oidc provider %q", id)
			}

			// Providers name themselves from their own type, and openidConnect
			// mangles the given name on top of that ("openid-connect" becomes
			// "openid-connect-oidc"). The callback URL and the login links use the
			// ID verbatim, so force the name back to it.
			gothProvider.SetName(id)

			gothProviders = append(gothProviders, gothProvider)
		}

		goth.UseProviders(gothProviders...)
	}

	gothic.Store = sessionStore

	handler := oidc.NewHandler(
		sessionStore,
		opts...,
	)

	return handler, nil
}

func getRandomBytes(n int) ([]byte, error) {
	data := make([]byte, n)

	read, err := rand.Read(data)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	if read != n {
		return nil, errors.Errorf("could not read %d bytes", n)
	}

	return data, nil
}

// buildOIDCProvider configures a single named OIDC provider: the factory
// building its goth provider (for interactive login), its login-button
// descriptor, and — when discovery succeeds — its JWKS/introspection/userinfo
// descriptor used by the oidctoken and oauth2token authenticators. The provider
// ID is reused as the goth name so it stays consistent across the
// interactive-login and API-token paths.
func buildOIDCProvider(ctx context.Context, np config.NamedOIDCProvider) (oidcProviderFactory, oidc.Provider, *oidc.ProviderWithJWKS, error) {
	discoveryURL := string(np.DiscoveryURL)
	key := string(np.Key)
	secret := string(np.Secret)

	discovery, discoveryErr := fetchOIDCDiscovery(ctx, discoveryURL)

	var factory oidcProviderFactory

	if discoveryErr == nil && discovery != nil && discovery.AuthURL != "" && discovery.TokenURL != "" {
		// The discovery document is read once, here, and reused by every provider
		// instance built from this factory. A multi-tenant instance builds one per
		// hostname it serves, and openidConnect.NewNamed would re-fetch the
		// document each time — on the login path of a tenant seen for the first
		// time.
		factory = func(callbackURL string) (goth.Provider, error) {
			provider, err := openidConnect.NewCustomisedURL(
				key,
				secret,
				callbackURL,
				discovery.AuthURL,
				discovery.TokenURL,
				discovery.Issuer,
				discovery.UserInfoEndpoint,
				discovery.EndSessionEndpoint,
				np.Scopes...,
			)
			if err != nil {
				return nil, errors.WithStack(err)
			}

			return provider, nil
		}
	} else {
		// Discovery could not be read here — let the provider try on its own, so a
		// transient failure at this point still surfaces as the same startup error
		// as before rather than silently disabling the provider.
		factory = func(callbackURL string) (goth.Provider, error) {
			provider, err := openidConnect.NewNamed(np.ID, key, secret, callbackURL, discoveryURL, np.Scopes...)
			if err != nil {
				return nil, errors.WithStack(err)
			}

			return provider, nil
		}
	}

	provider := oidc.Provider{
		ID:    np.ID,
		Label: np.Label,
		Icon:  np.Icon,
	}

	var withJWKS *oidc.ProviderWithJWKS
	if discoveryErr == nil && discovery != nil && discovery.JWKSURI != "" {
		withJWKS = &oidc.ProviderWithJWKS{
			ID:               np.ID,
			Label:            np.Label,
			Icon:             np.Icon,
			DiscoveryURL:     discoveryURL,
			Issuer:           discovery.Issuer,
			JWKSURL:          discovery.JWKSURI,
			IntrospectionURL: discovery.IntrospectionEndpoint,
			UserInfoURL:      discovery.UserInfoEndpoint,
			ClientID:         key,
			ClientSecret:     secret,
			RequiredScope:    np.RequiredScope,
			RequiredAudience: np.RequiredAudience,
		}
	}

	return factory, provider, withJWKS, nil
}

func fetchOIDCDiscovery(ctx context.Context, discoveryURL string) (*OIDCDiscovery, error) {
	if discoveryURL == "" {
		return nil, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("discovery fetch failed with status %d", resp.StatusCode)
	}

	var discovery OIDCDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&discovery); err != nil {
		return nil, errors.WithStack(err)
	}

	return &discovery, nil
}
