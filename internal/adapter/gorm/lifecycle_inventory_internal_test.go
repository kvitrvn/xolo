package gorm

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ncruces/go-sqlite3/gormlite"
	"github.com/stretchr/testify/require"
	gormpkg "gorm.io/gorm"
)

// TestPurgeOrderCoversInventory: the purge empties every table of the
// inventory, once, and nothing else.
func TestPurgeOrderCoversInventory(t *testing.T) {
	seen := map[string]bool{}
	for _, table := range purgeOrder {
		if seen[table] {
			t.Errorf("%s is purged twice", table)
		}
		seen[table] = true
		lifecycleTableOf(table)
	}
	for _, entry := range lifecycleTables {
		if !seen[entry.table] {
			t.Errorf("%s is missing from purgeOrder", entry.table)
		}
	}
}

// TestPurgeOrderRespectsDependencies: a table is purged before every table
// its inventory expressions read, which would no longer designate its rows.
func TestPurgeOrderRespectsDependencies(t *testing.T) {
	reads := map[string][]string{
		"membership_roles":  {"memberships"},
		"role_permissions":  {"roles"},
		"role_models":       {"roles"},
		"application_roles": {"applications"},
		"quota":             {"applications"},
		"users":             {"applications"},
		"llm_models":        {"providers"},
	}
	for table, parents := range reads {
		for _, parent := range parents {
			if slices.Index(purgeOrder, table) > slices.Index(purgeOrder, parent) {
				t.Errorf("%s must be purged before %s", table, parent)
			}
		}
	}
}

// TestInventoryKeysArePrimaryKeys: the export orders the rows of each table
// by its key, and the purge removes them through it. A key that is not the
// whole primary key would skip or repeat rows.
func TestInventoryKeysArePrimaryKeys(t *testing.T) {
	db, err := gormpkg.Open(gormlite.Open("file:"+filepath.Join(t.TempDir(), "inventory.sqlite")), &gormpkg.Config{})
	require.NoError(t, err)
	require.NoError(t, NewStore(db).Migrate(context.Background()))
	for _, entry := range lifecycleTables {
		var columns []struct {
			Name string
			PK   int
		}
		require.NoError(t, db.Raw("SELECT name, pk FROM pragma_table_info(?) WHERE pk > 0 ORDER BY pk", entry.table).Scan(&columns).Error)
		primary := make([]string, 0, len(columns))
		for _, column := range columns {
			primary = append(primary, column.Name)
		}
		require.ElementsMatch(t, primary, entry.keyColumns(), entry.table)
	}
}
