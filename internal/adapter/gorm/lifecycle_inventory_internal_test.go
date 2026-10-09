package gorm

import (
	"slices"
	"testing"
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
