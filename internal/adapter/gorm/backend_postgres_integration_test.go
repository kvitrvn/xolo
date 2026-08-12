//go:build integration

package gorm_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// postgresImage pins the server the integration suite runs against. Keep it in
// step with the version documented for production deployments.
const postgresImage = "postgres:17-alpine"

// sharedPostgresDSN addresses the server every PostgreSQL subtest connects to,
// either a testcontainers-managed instance started in TestMain or the external
// server named by XOLO_TEST_POSTGRES_DSN.
var sharedPostgresDSN string

// TestMain starts one PostgreSQL container for the whole package: containers
// cost seconds to boot, while per-test schemas cost milliseconds, so isolation
// is done at the schema level (see newPostgresBackend).
func TestMain(m *testing.M) {
	if dsn := postgresDSNFromEnv(); dsn != "" {
		sharedPostgresDSN = dsn
		os.Exit(m.Run())
	}

	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("xolo"),
		tcpostgres.WithUsername("xolo"),
		tcpostgres.WithPassword("xolo"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(2*time.Minute),
		),
	)
	if err != nil {
		log.Fatalf("could not start %s container: %v", postgresImage, err)
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Fatalf("could not build connection string: %v", err)
	}
	sharedPostgresDSN = dsn

	code := m.Run()

	if err := testcontainers.TerminateContainer(container); err != nil {
		log.Printf("could not terminate container: %v", err)
	}

	os.Exit(code)
}

// postgresBackends enables the PostgreSQL backend for every eachBackend call.
func postgresBackends(t *testing.T) []backend {
	t.Helper()

	if sharedPostgresDSN == "" {
		t.Fatal(fmt.Sprintf("no PostgreSQL server available: TestMain did not start one and %s is unset", testPostgresDSNEnv))
	}

	return []backend{newPostgresBackend(sharedPostgresDSN)}
}
