package gorm

import (
	"strings"

	"github.com/ncruces/go-sqlite3"
	"github.com/pkg/errors"
)

var (
	ErrMissingSource = errors.New("missing source")
)

// isUniqueConstraintViolation reports whether err is a SQLite constraint error
// naming every given "table.column" token. SQLite reports multi-column indexes
// as "UNIQUE constraint failed: roles.org_id, roles.name", so all the columns of
// the index must be listed to identify it unambiguously.
func isUniqueConstraintViolation(err error, columns ...string) bool {
	var sqliteErr *sqlite3.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code() != sqlite3.CONSTRAINT {
		return false
	}

	message := sqliteErr.Error()
	if !strings.Contains(message, "UNIQUE constraint failed") {
		return false
	}

	for _, column := range columns {
		if !strings.Contains(message, column) {
			return false
		}
	}

	return true
}
