// Package testutil provides shared integration-test fixtures.
package testutil

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/jordanp2002/Local-Ledger/internal/database"
)

// OpenDB opens a migrated, isolated on-disk ledger and closes it after the test.
func OpenDB(t testing.TB) *sql.DB {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "finance.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	return db
}
