package adminapi

import (
	"context"
	"crypto/x509"
)

// ClientIdentity describes the client certificate that authenticated an Admin
// API request.
//
// It carries no authorization decision: any client holding a certificate signed
// by the configured authority administers the instance. It is recorded so
// requests can be attributed in the logs, and so per-certificate scopes can be
// introduced later without reworking the request plumbing.
type ClientIdentity struct {
	CommonName   string
	SerialNumber string
	Subject      string
}

func newClientIdentity(cert *x509.Certificate) *ClientIdentity {
	return &ClientIdentity{
		CommonName:   cert.Subject.CommonName,
		SerialNumber: cert.SerialNumber.String(),
		Subject:      cert.Subject.String(),
	}
}

type contextKey struct{}

var clientIdentityContextKey contextKey

func setClientIdentity(ctx context.Context, identity *ClientIdentity) context.Context {
	return context.WithValue(ctx, clientIdentityContextKey, identity)
}

// CurrentClientIdentity returns the identity of the client certificate that
// authenticated the request, if any.
func CurrentClientIdentity(ctx context.Context) (*ClientIdentity, bool) {
	identity, ok := ctx.Value(clientIdentityContextKey).(*ClientIdentity)
	return identity, ok
}
