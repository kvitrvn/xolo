package oidc

import (
	"net/http"
	"net/url"

	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"github.com/pkg/errors"
)

// completeUserAuth mirrors gothic.CompleteUserAuth without persisting the
// authorized provider session. OIDC sessions contain access, ID, and refresh
// tokens after the code exchange and can exceed the browser cookie limit. Xolo
// only needs those tokens long enough to fetch the user, so keeping them in the
// callback request avoids both an oversized cookie and unnecessary token
// exposure to the browser.
func completeUserAuth(res http.ResponseWriter, req *http.Request) (goth.User, error) {
	providerName, err := gothic.GetProviderName(req)
	if err != nil {
		return goth.User{}, errors.WithStack(err)
	}

	provider, err := goth.GetProvider(providerName)
	if err != nil {
		return goth.User{}, errors.WithStack(err)
	}

	value, err := gothic.GetFromSession(providerName, req)
	if err != nil {
		return goth.User{}, errors.WithStack(err)
	}
	defer func() {
		_ = gothic.Logout(res, req)
	}()

	sess, err := provider.UnmarshalSession(value)
	if err != nil {
		return goth.User{}, errors.WithStack(err)
	}

	if err := validateState(req, sess); err != nil {
		return goth.User{}, errors.WithStack(err)
	}

	user, err := provider.FetchUser(sess)
	if err == nil {
		return user, nil
	}

	params := req.URL.Query()
	if params.Encode() == "" && req.Method == http.MethodPost {
		if err := req.ParseForm(); err != nil {
			return goth.User{}, errors.WithStack(err)
		}
		params = req.Form
	}

	if _, err := sess.Authorize(provider, params); err != nil {
		return goth.User{}, errors.WithStack(err)
	}

	user, err = provider.FetchUser(sess)
	return user, errors.WithStack(err)
}

func validateState(req *http.Request, sess goth.Session) error {
	rawAuthURL, err := sess.GetAuthURL()
	if err != nil {
		return errors.WithStack(err)
	}

	authURL, err := url.Parse(rawAuthURL)
	if err != nil {
		return errors.WithStack(err)
	}

	originalState := authURL.Query().Get("state")
	if originalState != "" && originalState != gothic.GetState(req) {
		return errors.New("state token mismatch")
	}

	return nil
}
