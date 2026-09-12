package oidc

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pkg/errors"
	httpCtx "github.com/xolo-gateway/xolo/internal/http/context"
)

// serveWithContextProvider runs the middleware for one provider route and
// reports the status and the provider name gothic would read from the context.
func serveWithContextProvider(t *testing.T, h *Handler, providerID string, baseURL string) (int, string) {
	t.Helper()

	var seen string

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, _ := r.Context().Value("provider").(string)
		seen = name
	})

	req := httptest.NewRequest(http.MethodGet, "/providers/"+providerID, nil)
	req.SetPathValue("provider", providerID)
	req = req.WithContext(httpCtx.SetBaseURL(req.Context(), baseURL))

	rec := httptest.NewRecorder()
	h.withContextProvider(next).ServeHTTP(rec, req)

	return rec.Code, seen
}

func TestWithContextProvider(t *testing.T) {
	t.Run("without a resolver the route parameter is used as is", func(t *testing.T) {
		h := &Handler{}

		status, provider := serveWithContextProvider(t, h, "github", "https://xolo.example.com")

		if status != http.StatusOK {
			t.Fatalf("status: got %d, want %d", status, http.StatusOK)
		}
		if provider != "github" {
			t.Errorf("provider: got %q, want %q", provider, "github")
		}
	})

	t.Run("the resolver names the provider serving this host", func(t *testing.T) {
		h := &Handler{
			resolveProvider: func(providerID string, baseURL string) (string, error) {
				if baseURL != "https://acme.xolo.example.com" {
					t.Errorf("base url: got %q, want the host of the request", baseURL)
				}

				return HostScopedProviderName(providerID, "acme.xolo.example.com"), nil
			},
		}

		status, provider := serveWithContextProvider(t, h, "github", "https://acme.xolo.example.com")

		if status != http.StatusOK {
			t.Fatalf("status: got %d, want %d", status, http.StatusOK)
		}
		if want := "github@acme.xolo.example.com"; provider != want {
			t.Errorf("provider: got %q, want %q", provider, want)
		}
	})

	t.Run("a provider that can not be resolved is a 404", func(t *testing.T) {
		h := &Handler{
			resolveProvider: func(string, string) (string, error) {
				return "", errors.New("no such provider")
			},
		}

		status, provider := serveWithContextProvider(t, h, "nope", "https://acme.xolo.example.com")

		if status != http.StatusNotFound {
			t.Fatalf("status: got %d, want %d", status, http.StatusNotFound)
		}
		if provider != "" {
			t.Errorf("the request should not have been served, got provider %q", provider)
		}
	})
}
