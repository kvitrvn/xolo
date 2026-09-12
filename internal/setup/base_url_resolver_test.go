package setup

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewTenantBaseURLResolver(t *testing.T) {
	matchesTenantHost := func(host string) bool {
		return strings.HasSuffix(host, ".xolo.example.com") ||
			strings.HasSuffix(host, ".xolo.example.com:3002")
	}

	t.Run("refuses a base url that is not absolute", func(t *testing.T) {
		if _, err := newTenantBaseURLResolver("/", matchesTenantHost); err == nil {
			t.Fatal("error: got nil, want one")
		}
	})

	for name, testCase := range map[string]struct {
		baseURL string
		host    string
		want    string
	}{
		"tenant host takes over the authority": {
			baseURL: "https://xolo.example.com",
			host:    "acme.xolo.example.com",
			want:    "https://acme.xolo.example.com",
		},
		"the port travels with the host": {
			baseURL: "http://xolo.example.com:3002",
			host:    "acme.xolo.example.com:3002",
			want:    "http://acme.xolo.example.com:3002",
		},
		"the configured path is kept": {
			baseURL: "https://xolo.example.com/gateway",
			host:    "acme.xolo.example.com",
			want:    "https://acme.xolo.example.com/gateway",
		},
		"a host outside the pattern falls back": {
			baseURL: "https://xolo.example.com",
			host:    "evil.example.com",
			want:    "https://xolo.example.com",
		},
		"an empty host falls back": {
			baseURL: "https://xolo.example.com",
			host:    "",
			want:    "https://xolo.example.com",
		},
	} {
		t.Run(name, func(t *testing.T) {
			resolve, err := newTenantBaseURLResolver(testCase.baseURL, matchesTenantHost)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = testCase.host

			if got := resolve(req); got != testCase.want {
				t.Errorf("base url: got %q, want %q", got, testCase.want)
			}
		})
	}
}
