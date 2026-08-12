//go:build !integration

package gorm_test

import "testing"

// postgresBackends enables the PostgreSQL backend only when a server DSN is
// provided explicitly. Without the "integration" build tag there is no
// testcontainers-managed server, so the default `go test ./...` stays on
// SQLite and needs no Docker daemon.
func postgresBackends(t *testing.T) []backend {
	t.Helper()

	dsn := postgresDSNFromEnv()
	if dsn == "" {
		return nil
	}

	return []backend{newPostgresBackend(dsn)}
}
