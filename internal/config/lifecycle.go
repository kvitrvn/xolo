package config

import (
	"fmt"
	"time"
)

// Lifecycle configures the deletion of tenants, organizations and members.
// Disabled, no deletion can be recorded, no database guard is installed and
// nothing is purged; the guards of deletions already recorded are kept.
type Lifecycle struct {
	Enabled bool `env:"ENABLED" envDefault:"false"`
	// Retention is how long a deleted resource is kept, frozen, before it may
	// be purged.
	Retention time.Duration `env:"RETENTION" envDefault:"720h"`
	// PollInterval is how often the purge worker looks for due deletions.
	PollInterval time.Duration `env:"POLL_INTERVAL" envDefault:"1m"`
	// PurgeBatch bounds the rows one purge transaction removes.
	PurgeBatch int `env:"PURGE_BATCH" envDefault:"1000"`
}

func (c Lifecycle) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Retention < time.Second || c.Retention > 3650*24*time.Hour {
		return fmt.Errorf("XOLO_LIFECYCLE_RETENTION must be between 1s and 3650 days")
	}
	if c.PollInterval < time.Second || c.PollInterval > time.Hour {
		return fmt.Errorf("XOLO_LIFECYCLE_POLL_INTERVAL must be between 1s and 1h")
	}
	if c.PurgeBatch < 1 || c.PurgeBatch > 100000 {
		return fmt.Errorf("XOLO_LIFECYCLE_PURGE_BATCH must be between 1 and 100000")
	}
	return nil
}
