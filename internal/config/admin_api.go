package config

import (
	"time"

	"github.com/pkg/errors"
)

// AdminAPI configures the instance administration API. It is served on its own
// listener, on its own port, and authenticates its clients with mutual TLS
// only: no OIDC, no session, no user token.
//
// It is disabled by default: an instance that does not need machine
// provisioning never opens that port.
type AdminAPI struct {
	Enabled         bool          `env:"ENABLED" envDefault:"false"`
	Address         string        `env:"ADDRESS,expand" envDefault:":3003"`
	TLSCertFile     string        `env:"TLS_CERT_FILE,expand"`
	TLSKeyFile      string        `env:"TLS_KEY_FILE,expand"`
	TLSClientCAFile string        `env:"TLS_CLIENT_CA_FILE,expand"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"10s"`
}

// Validate refuses a configuration that would enable the Admin API without the
// material needed to enforce mutual TLS. There is no anonymous fallback: an
// incomplete configuration is a startup failure, not a degraded mode.
func (c *AdminAPI) Validate() error {
	if !c.Enabled {
		return nil
	}

	for _, required := range []struct {
		name  string
		value string
	}{
		{"XOLO_ADMIN_API_TLS_CERT_FILE", c.TLSCertFile},
		{"XOLO_ADMIN_API_TLS_KEY_FILE", c.TLSKeyFile},
		{"XOLO_ADMIN_API_TLS_CLIENT_CA_FILE", c.TLSClientCAFile},
	} {
		if required.value == "" {
			return errors.Errorf("%s is required but not set when XOLO_ADMIN_API_ENABLED is true", required.name)
		}
	}

	if c.Address == "" {
		return errors.New("XOLO_ADMIN_API_ADDRESS is required but not set when XOLO_ADMIN_API_ENABLED is true")
	}

	return nil
}
