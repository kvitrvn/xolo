package config

import (
	"testing"
	"time"
)

func TestParse_LifecycleDisabledByDefault(t *testing.T) {
	t.Setenv("XOLO_SECRET_KEY", testSecretKey)

	conf, err := Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if conf.Lifecycle.Enabled || conf.Lifecycle.Retention != 720*time.Hour {
		t.Errorf("unexpected defaults: %+v", conf.Lifecycle)
	}
}

func TestParse_LifecycleRetention(t *testing.T) {
	for value, valid := range map[string]bool{"24h": true, "1s": true, "0s": false, "87601h": false} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("XOLO_SECRET_KEY", testSecretKey)
			t.Setenv("XOLO_LIFECYCLE_ENABLED", "true")
			t.Setenv("XOLO_LIFECYCLE_RETENTION", value)
			if _, err := Parse(); (err == nil) != valid {
				t.Errorf("%s: got %v", value, err)
			}
		})
	}
}

func TestParse_LifecyclePurge(t *testing.T) {
	for _, c := range []struct {
		name, interval, batch string
		valid                 bool
	}{
		{"defaults", "", "", true},
		{"bounds", "1h", "100000", true},
		{"interval too short", "500ms", "", false},
		{"interval too long", "2h", "", false},
		{"empty batch", "", "0", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("XOLO_SECRET_KEY", testSecretKey)
			t.Setenv("XOLO_LIFECYCLE_ENABLED", "true")
			if c.interval != "" {
				t.Setenv("XOLO_LIFECYCLE_POLL_INTERVAL", c.interval)
			}
			if c.batch != "" {
				t.Setenv("XOLO_LIFECYCLE_PURGE_BATCH", c.batch)
			}
			conf, err := Parse()
			if (err == nil) != c.valid {
				t.Fatalf("got %v", err)
			}
			if c.name == "defaults" && (conf.Lifecycle.PollInterval != time.Minute || conf.Lifecycle.PurgeBatch != 1000) {
				t.Errorf("unexpected defaults: %+v", conf.Lifecycle)
			}
		})
	}
}
