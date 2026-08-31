package database_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/database"
)

func TestOpenCreatesMigratesAndClosesOnDiskDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "finance.db")

	db, err := database.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", path, err)
	}
	if db == nil {
		t.Fatal("Open() returned a nil database")
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat database file: %v", err)
	}
	if got := db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("max open connections = %d, want 1", got)
	}
	if got := openTestQueryInt64(t, db, "PRAGMA user_version"); got != 7 {
		t.Fatalf("schema version = %d, want 7", got)
	}
	if got := openTestQueryInt64(t, db, "PRAGMA foreign_keys"); got != 1 {
		t.Fatalf("foreign_keys = %d, want 1", got)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestOpenCreatesMissingParentDirectory(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "LocalLedger")
	path := filepath.Join(parent, "finance.db")

	db, err := database.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", path, err)
	}
	defer openTestCloseDB(t, db)

	info, err := os.Stat(parent)
	if err != nil {
		t.Fatalf("stat database directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("database parent %q is not a directory", parent)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat database file: %v", err)
	}
}

func TestOpenPreservesURICharacterPaths(t *testing.T) {
	characters := []string{"?", "#", "%", "&"}
	for _, character := range characters {
		t.Run(character, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ledger path"+character+".db")
			db, err := database.Open(context.Background(), path)
			if err != nil {
				t.Fatalf("Open(%q) error = %v", path, err)
			}
			defer openTestCloseDB(t, db)

			if _, err := db.Exec(`CREATE TABLE path_probe (value TEXT NOT NULL)`); err != nil {
				t.Fatalf("create probe table: %v", err)
			}
			if _, err := db.Exec(`INSERT INTO path_probe(value) VALUES ('opened')`); err != nil {
				t.Fatalf("insert probe row: %v", err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("stat exact database path: %v", err)
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil {
				t.Fatalf("read database directory: %v", err)
			}
			for _, entry := range entries {
				if entry.Name() == filepath.Base(path) {
					continue
				}
				if strings.HasPrefix(entry.Name(), "ledger") && strings.HasSuffix(entry.Name(), ".db") {
					t.Fatalf("unexpected sibling database %q for path %q", entry.Name(), path)
				}
			}
		})
	}
}

func TestOpenAppliesForeignKeysToReplacementConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "finance.db")
	db, err := database.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", path, err)
	}
	defer openTestCloseDB(t, db)

	if got := openTestQueryInt64(t, db, "PRAGMA foreign_keys"); got != 1 {
		t.Fatalf("initial foreign_keys = %d, want 1", got)
	}

	// With one open connection, MaxIdleConns(0) forces database/sql to close
	// the current connection when it becomes idle. The next query must create a
	// replacement and the DSN must apply the pragma to that connection too.
	db.SetMaxIdleConns(0)
	if _, err := db.Exec(`SELECT 1`); err != nil {
		t.Fatalf("prime replacement connection: %v", err)
	}
	if got := openTestQueryInt64(t, db, "PRAGMA foreign_keys"); got != 1 {
		t.Fatalf("replacement foreign_keys = %d, want 1", got)
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	db, err := database.Open(context.Background(), "")
	if err == nil {
		if db != nil {
			db.Close()
		}
		t.Fatal("Open(\"\") error = nil")
	}
	if db != nil {
		t.Fatal("Open(\"\") returned a database")
	}
}

func TestOpenRejectsRelativePathWithoutCreatingFile(t *testing.T) {
	path := filepath.Join("relative", "finance.db")
	db, err := database.Open(context.Background(), path)
	if err == nil {
		if db != nil {
			db.Close()
		}
		t.Fatalf("Open(%q) error = nil", path)
	}
	if db != nil {
		t.Fatal("Open(relative path) returned a database")
	}
}

func TestOpenRejectsNewerSchemaVersionAndReturnsNoDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	seed, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open(%q): %v", path, err)
	}
	if _, err := seed.Exec("PRAGMA user_version = 8"); err != nil {
		seed.Close()
		t.Fatalf("set newer user_version: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed database: %v", err)
	}

	db, err := database.Open(context.Background(), path)
	if err == nil {
		if db != nil {
			db.Close()
		}
		t.Fatal("Open(newer schema) error = nil")
	}
	if db != nil {
		t.Fatal("Open(newer schema) returned a usable database")
	}
	if !strings.Contains(err.Error(), "newer than the latest known migration") {
		t.Fatalf("Open(newer schema) error = %v, want newer-version context", err)
	}

	// The failed startup must have closed its handle; the file remains usable
	// for a later caller to inspect or migrate once a compatible binary exists.
	check, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen newer schema: %v", err)
	}
	defer openTestCloseDB(t, check)
	if got := openTestQueryInt64(t, check, "PRAGMA user_version"); got != 8 {
		t.Fatalf("schema version after failed startup = %d, want 8", got)
	}
}

func openTestQueryInt64(t *testing.T, db *sql.DB, query string) int64 {
	t.Helper()
	var value int64
	if err := db.QueryRow(query).Scan(&value); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return value
}

func openTestCloseDB(t *testing.T, db *sql.DB) {
	t.Helper()
	if db != nil {
		if err := db.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			t.Errorf("close database: %v", err)
		}
	}
}
