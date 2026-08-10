package config

import (
	"strings"
	"testing"
	"time"
)

func TestParse_AdminAPIDisabledByDefault(t *testing.T) {
	t.Setenv("XOLO_SECRET_KEY", testSecretKey)

	conf, err := Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if conf.AdminAPI.Enabled {
		t.Error("admin api should be disabled by default")
	}
	if conf.AdminAPI.Address != ":3003" {
		t.Errorf("address: got %q, want %q", conf.AdminAPI.Address, ":3003")
	}
	if conf.AdminAPI.ShutdownTimeout != 10*time.Second {
		t.Errorf("shutdown timeout: got %v, want %v", conf.AdminAPI.ShutdownTimeout, 10*time.Second)
	}
}

func TestParse_AdminAPIEnabled(t *testing.T) {
	t.Setenv("XOLO_SECRET_KEY", testSecretKey)
	t.Setenv("XOLO_ADMIN_API_ENABLED", "true")
	t.Setenv("XOLO_ADMIN_API_ADDRESS", ":4443")
	t.Setenv("XOLO_ADMIN_API_TLS_CERT_FILE", "/etc/xolo/admin.crt")
	t.Setenv("XOLO_ADMIN_API_TLS_KEY_FILE", "/etc/xolo/admin.key")
	t.Setenv("XOLO_ADMIN_API_TLS_CLIENT_CA_FILE", "/etc/xolo/ca.crt")
	t.Setenv("XOLO_ADMIN_API_SHUTDOWN_TIMEOUT", "30s")

	conf, err := Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	admin := conf.AdminAPI

	if !admin.Enabled {
		t.Error("admin api should be enabled")
	}
	if admin.Address != ":4443" {
		t.Errorf("address: got %q", admin.Address)
	}
	if admin.TLSCertFile != "/etc/xolo/admin.crt" || admin.TLSKeyFile != "/etc/xolo/admin.key" || admin.TLSClientCAFile != "/etc/xolo/ca.crt" {
		t.Errorf("tls files: got %+v", admin)
	}
	if admin.ShutdownTimeout != 30*time.Second {
		t.Errorf("shutdown timeout: got %v", admin.ShutdownTimeout)
	}
}

// TestParse_AdminAPIEnabledWithoutTLS checks that enabling the Admin API
// without the mutual TLS material fails at startup, naming the missing
// variable. There is no anonymous fallback.
func TestParse_AdminAPIEnabledWithoutTLS(t *testing.T) {
	for missing, env := range map[string]map[string]string{
		"XOLO_ADMIN_API_TLS_CERT_FILE": {
			"XOLO_ADMIN_API_TLS_KEY_FILE":       "/etc/xolo/admin.key",
			"XOLO_ADMIN_API_TLS_CLIENT_CA_FILE": "/etc/xolo/ca.crt",
		},
		"XOLO_ADMIN_API_TLS_KEY_FILE": {
			"XOLO_ADMIN_API_TLS_CERT_FILE":      "/etc/xolo/admin.crt",
			"XOLO_ADMIN_API_TLS_CLIENT_CA_FILE": "/etc/xolo/ca.crt",
		},
		"XOLO_ADMIN_API_TLS_CLIENT_CA_FILE": {
			"XOLO_ADMIN_API_TLS_CERT_FILE": "/etc/xolo/admin.crt",
			"XOLO_ADMIN_API_TLS_KEY_FILE":  "/etc/xolo/admin.key",
		},
	} {
		t.Run("missing "+missing, func(t *testing.T) {
			t.Setenv("XOLO_SECRET_KEY", testSecretKey)
			t.Setenv("XOLO_ADMIN_API_ENABLED", "true")
			for name, value := range env {
				t.Setenv(name, value)
			}

			_, err := Parse()
			if err == nil {
				t.Fatal("Parse should have failed")
			}
			if !strings.Contains(err.Error(), missing) {
				t.Errorf("error should name %s, got %v", missing, err)
			}
		})
	}
}
