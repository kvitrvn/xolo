package setup

import (
	"context"

	"github.com/pkg/errors"
	"github.com/xolo-gateway/xolo/internal/adapter/cache"
	"github.com/xolo-gateway/xolo/internal/config"
	"github.com/xolo-gateway/xolo/internal/core/port"
	"github.com/xolo-gateway/xolo/internal/core/service"
)

// getLifecycleServiceFromConfig runs the deletions on the gorm store,
// invalidating the cached users and tokens of the scopes it freezes and
// purges. Disabled, the store refuses to record a deletion.
var getLifecycleServiceFromConfig = createFromConfigOnce(func(ctx context.Context, conf *config.Config) (*service.LifecycleService, error) {
	if err := conf.Lifecycle.Validate(); err != nil {
		return nil, errors.WithStack(err)
	}
	backend, err := getGormStoreFromConfig(ctx, conf)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	userStore, err := getUserStoreFromConfig(ctx, conf)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	var store port.LifecycleStore = backend
	if cached, ok := userStore.(*cache.UserStore); ok {
		store = cache.NewLifecycleStore(backend, cached)
	}
	return service.NewLifecycleService(store), nil
})

// startLifecyclePurgeFromConfig starts the purge of the due deletions until
// ctx is done, only when the lifecycle is enabled: a disabled instance keeps
// the deletions already recorded frozen, unpurged.
var startLifecyclePurgeFromConfig = createFromConfigOnce(func(ctx context.Context, conf *config.Config) (struct{}, error) {
	if !conf.Lifecycle.Enabled {
		return struct{}{}, nil
	}
	lifecycle, err := getLifecycleServiceFromConfig(ctx, conf)
	if err != nil {
		return struct{}{}, errors.WithStack(err)
	}
	go lifecycle.Run(ctx, conf.Lifecycle.PollInterval)
	return struct{}{}, nil
})
