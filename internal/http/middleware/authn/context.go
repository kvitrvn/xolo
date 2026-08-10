package authn

import (
	"context"

	"github.com/pkg/errors"
)

type contextKey string

const keyUser contextKey = "user"

func ContextUser(ctx context.Context) *User {
	user, ok := ctx.Value(keyUser).(*User)
	if !ok {
		panic(errors.New("no user in context"))
	}

	return user
}

// SetContextUser attaches an authenticated identity to the context. The
// authentication middleware calls it; it is exported so the middlewares reading
// that identity can be tested without standing up a full authenticator.
func SetContextUser(ctx context.Context, user *User) context.Context {
	return context.WithValue(ctx, keyUser, user)
}
