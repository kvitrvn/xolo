package adminapi

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRequireClientCert exercises the defense in depth check directly: the TLS
// handshake normally rejects these requests before they reach the handler, so
// the middleware can only be reached through a misconfigured listener.
func TestRequireClientCert(t *testing.T) {
	var served bool

	handler := requireClientCert(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served = true
		if _, ok := CurrentClientIdentity(r.Context()); !ok {
			t.Error("client identity should be available in the request context")
		}
	}))

	t.Run("rejects a request without TLS state", func(t *testing.T) {
		served = false

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/tenants", nil))

		if served {
			t.Error("the wrapped handler should not have been called")
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
		}
		if body := rec.Body.String(); !strings.Contains(body, `"unauthorized"`) {
			t.Errorf("body: got %q", body)
		}
	})

	t.Run("rejects a TLS request without peer certificate", func(t *testing.T) {
		served = false

		req := httptest.NewRequest(http.MethodGet, "/v1/tenants", nil)
		req.TLS = &tls.ConnectionState{}

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if served {
			t.Error("the wrapped handler should not have been called")
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("exposes the client identity", func(t *testing.T) {
		served = false

		req := httptest.NewRequest(http.MethodGet, "/v1/tenants", nil)
		req.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{{
				SerialNumber: big.NewInt(42),
				Subject:      pkix.Name{CommonName: "control-plane"},
			}},
		}

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if !served {
			t.Fatal("the wrapped handler should have been called")
		}
		if rec.Code != http.StatusOK {
			t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
		}
	})
}
