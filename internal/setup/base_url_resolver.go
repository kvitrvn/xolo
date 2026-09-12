package setup

import (
	"net/http"
	"net/url"

	"github.com/pkg/errors"
)

// newTenantBaseURLResolver derives the public base URL of a request from its
// own host, keeping the scheme and path of the configured base URL. A
// multi-tenant instance serves every tenant on its own hostname, so a single
// instance-wide base URL would send each of them links, redirects and OAuth
// callbacks pointing at another tenant's host.
//
// hostMatches guards the substitution: the Host header is client-controlled, so
// a host the tenant pattern does not frame falls back to the configured value
// rather than ending up in a generated URL. Such hosts are answered 404 by the
// tenant middleware anyway — this only makes sure nothing is built from them in
// the meantime.
func newTenantBaseURLResolver(baseURL string, hostMatches func(host string) bool) (func(r *http.Request) string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, errors.Wrapf(err, "could not parse base url %q", baseURL)
	}

	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.Errorf("base url %q must be absolute (scheme and host) when multi-tenancy is enabled", baseURL)
	}

	return func(r *http.Request) string {
		if r.Host == "" || !hostMatches(r.Host) {
			return baseURL
		}

		perHost := *parsed
		perHost.Host = r.Host

		return perHost.String()
	}, nil
}
