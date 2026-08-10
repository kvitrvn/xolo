package setup

import (
	"context"

	"github.com/bornholm/xolo/internal/adminapi"
	v1 "github.com/bornholm/xolo/internal/adminapi/handler/v1"
	"github.com/bornholm/xolo/internal/config"
	"github.com/pkg/errors"
)

// NewAdminAPIServerFromConfig assembles the Admin API server. It returns a nil
// server when the API is disabled, which the caller treats as "nothing to run".
//
// The TLS material is loaded here, before any listener is opened, so a
// misconfiguration is fatal at startup rather than on the first request.
func NewAdminAPIServerFromConfig(ctx context.Context, conf *config.Config) (*adminapi.Server, error) {
	if !conf.AdminAPI.Enabled {
		return nil, nil
	}

	provisioning, err := getProvisioningServiceFromConfig(ctx, conf)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	tlsConfig, err := adminapi.LoadTLSConfig(
		conf.AdminAPI.TLSCertFile,
		conf.AdminAPI.TLSKeyFile,
		conf.AdminAPI.TLSClientCAFile,
	)
	if err != nil {
		return nil, errors.Wrap(err, "could not load admin api tls configuration")
	}

	return adminapi.NewServer(
		adminapi.WithAddress(conf.AdminAPI.Address),
		adminapi.WithTLSConfig(tlsConfig),
		adminapi.WithHandler(v1.NewHandler(provisioning)),
		adminapi.WithShutdownTimeout(conf.AdminAPI.ShutdownTimeout),
	), nil
}
