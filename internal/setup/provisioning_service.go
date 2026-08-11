package setup

import (
	"context"

	"github.com/xolo-gateway/xolo/internal/config"
	"github.com/xolo-gateway/xolo/internal/core/service"
	"github.com/pkg/errors"
)

// getProvisioningServiceFromConfig builds the provisioning service on top of
// the very same store instances the public HTTP server uses — cache and event
// decorators included. No second database connection, no second repository
// implementation.
var getProvisioningServiceFromConfig = createFromConfigOnce(func(ctx context.Context, conf *config.Config) (*service.ProvisioningService, error) {
	orgStore, err := getOrgStoreFromConfig(ctx, conf)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	userStore, err := getUserStoreFromConfig(ctx, conf)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	roleStore, err := getRoleStoreFromConfig(ctx, conf)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return service.NewProvisioningService(orgStore, userStore, roleStore), nil
})
