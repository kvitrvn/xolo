package oidc

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/sessions"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"golang.org/x/oauth2"
)

func TestCompleteUserAuthDoesNotPersistAuthorizedProviderSession(t *testing.T) {
	const providerName = "oversized-session"

	previousStore := gothic.Store
	gothic.Store = sessions.NewCookieStore([]byte("0123456789abcdef0123456789abcdef"))
	goth.ClearProviders()
	t.Cleanup(func() {
		gothic.Store = previousStore
		goth.ClearProviders()
	})

	goth.UseProviders(&oversizedSessionProvider{name: providerName})

	startRequest := httptest.NewRequest(http.MethodGet, "/auth/oidc/providers/"+providerName, nil)
	startRequest = gothic.GetContextWithProvider(startRequest, providerName)
	startResponse := httptest.NewRecorder()
	if err := gothic.StoreInSession(providerName, "pending", startRequest, startResponse); err != nil {
		t.Fatalf("store pending session: %v", err)
	}

	callbackRequest := httptest.NewRequest(
		http.MethodGet,
		"/auth/oidc/providers/"+providerName+"/callback?state=expected&code=code",
		nil,
	)
	callbackRequest = gothic.GetContextWithProvider(callbackRequest, providerName)
	for _, cookie := range startResponse.Result().Cookies() {
		callbackRequest.AddCookie(cookie)
	}

	user, err := completeUserAuth(httptest.NewRecorder(), callbackRequest)
	if err != nil {
		t.Fatalf("complete user auth: %v", err)
	}
	if user.UserID != "subject" {
		t.Fatalf("user ID: got %q, want %q", user.UserID, "subject")
	}
}

func TestValidateStateRejectsMismatch(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/callback?state=unexpected", nil)

	err := validateState(request, &oversizedSession{})
	if err == nil || err.Error() != "state token mismatch" {
		t.Fatalf("validate state: got %v, want state token mismatch", err)
	}
}

type oversizedSessionProvider struct {
	name string
}

func (p *oversizedSessionProvider) Name() string { return p.name }

func (p *oversizedSessionProvider) SetName(name string) { p.name = name }

func (p *oversizedSessionProvider) BeginAuth(string) (goth.Session, error) {
	return &oversizedSession{}, nil
}

func (p *oversizedSessionProvider) UnmarshalSession(string) (goth.Session, error) {
	return &oversizedSession{}, nil
}

func (p *oversizedSessionProvider) FetchUser(session goth.Session) (goth.User, error) {
	sess := session.(*oversizedSession)
	if !sess.authorized {
		return goth.User{}, errors.New("authorization required")
	}

	return goth.User{UserID: "subject", Email: "user@example.com"}, nil
}

func (p *oversizedSessionProvider) Debug(bool) {}

func (p *oversizedSessionProvider) RefreshToken(string) (*oauth2.Token, error) {
	return nil, errors.New("not supported")
}

func (p *oversizedSessionProvider) RefreshTokenAvailable() bool { return false }

type oversizedSession struct {
	authorized bool
}

func (s *oversizedSession) GetAuthURL() (string, error) {
	return "https://idp.example.com/authorize?state=expected", nil
}

func (s *oversizedSession) Marshal() string {
	if s.authorized {
		panic("authorized provider session must not be persisted")
	}

	return "pending"
}

func (s *oversizedSession) Authorize(goth.Provider, goth.Params) (string, error) {
	s.authorized = true
	return "token", nil
}
