package authn

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type fixedAuthenticator struct{ user *User }

func (a fixedAuthenticator) Authenticate(http.ResponseWriter, *http.Request) (*User, error) {
	return a.user, nil
}

func TestOptionalLetsSignedOutRequestsThrough(t *testing.T) {
	for _, tc := range []struct {
		name string
		user *User
		want string
	}{
		{"signed in", &User{Provider: "oidc", Subject: "jane"}, "signed in jane"},
		{"signed out", nil, "signed out"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			signedOut := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = "signed out"
			})
			signedIn := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = "signed in " + ContextUser(r.Context()).Subject
			})
			Optional(signedOut, fixedAuthenticator{nil}, fixedAuthenticator{tc.user})(signedIn).
				ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/locale", nil))
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
