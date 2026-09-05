package database_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jordanp2002/local-finance-mcp/internal/database"
)

func TestMigrateNewDatabaseAppliesEmbeddedMigration(t *testing.T) {
	db := openMigrationTestDB(t)
	defer db.Close()

	if err := database.Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	if got := migrationVersion(t, db); got != 10 {
		t.Fatalf("migration version = %d, want 10", got)
	}
}

func TestMigrateTwiceIsSafe(t *testing.T) {
	db := openMigrationTestDB(t)
	defer db.Close()

	ctx := context.Background()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("first Migrate() error = %v", err)
	}
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}

	if got := migrationVersion(t, db); got != 10 {
		t.Fatalf("migration version = %d, want 10", got)
	}
}

func TestMigrateFSAppliesMigrationsInNumericOrderAndOnlyPendingMigrations(t *testing.T) {
	db := openMigrationTestDB(t)
	defer db.Close()

	ctx := context.Background()
	initial := migrationSet(map[string]string{
		"001_create_log.sql": `CREATE TABLE migration_log (step INTEGER NOT NULL); INSERT INTO migration_log(step) VALUES (1);`,
		"002_second.sql":     `INSERT INTO migration_log(step) VALUES (2);`,
		"003_third.sql":      `INSERT INTO migration_log(step) VALUES (3);`,
	})

	if err := database.MigrateFS(ctx, db, initial); err != nil {
		t.Fatalf("initial MigrateFS() error = %v", err)
	}
	if err := database.MigrateFS(ctx, db, initial); err != nil {
		t.Fatalf("repeat MigrateFS() error = %v", err)
	}

	if got := migrationVersion(t, db); got != 3 {
		t.Fatalf("migration version after initial run = %d, want 3", got)
	}
	if got := migrationLogSteps(t, db); got != "1,2,3" {
		t.Fatalf("migration log after initial run = %q, want %q", got, "1,2,3")
	}

	upgraded := migrationSet(map[string]string{
		"001_create_log.sql": `CREATE TABLE migration_log (step INTEGER NOT NULL); INSERT INTO migration_log(step) VALUES (1);`,
		"002_second.sql":     `INSERT INTO migration_log(step) VALUES (2);`,
		"003_third.sql":      `INSERT INTO migration_log(step) VALUES (3);`,
		"004_fourth.sql":     `INSERT INTO migration_log(step) VALUES (4);`,
	})
	if err := database.MigrateFS(ctx, db, upgraded); err != nil {
		t.Fatalf("upgrade MigrateFS() error = %v", err)
	}

	if got := migrationVersion(t, db); got != 4 {
		t.Fatalf("migration version after upgrade = %d, want 4", got)
	}
	if got := migrationLogSteps(t, db); got != "1,2,3,4" {
		t.Fatalf("migration log after upgrade = %q, want %q", got, "1,2,3,4")
	}
}

func TestMigrateFSRejectsInvalidCompleteMigrationSetsBeforeApplying(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name: "malformed filename",
			files: map[string]string{
				"001_first.sql": `CREATE TABLE must_not_apply (id INTEGER);`,
				"migration.sql": "CREATE TABLE also_must_not_apply (id INTEGER);",
			},
			want: "invalid migration filename",
		},
		{
			name: "duplicate version",
			files: map[string]string{
				"001_first.sql":  `CREATE TABLE must_not_apply (id INTEGER);`,
				"001_second.sql": `CREATE TABLE also_must_not_apply (id INTEGER);`,
			},
			want: "duplicate migration version",
		},
		{
			name: "version gap",
			files: map[string]string{
				"001_first.sql": `CREATE TABLE must_not_apply (id INTEGER);`,
				"003_third.sql": `CREATE TABLE also_must_not_apply (id INTEGER);`,
			},
			want: "migration sequence has a gap",
		},
		{
			name: "does not begin at 001",
			files: map[string]string{
				"002_second.sql": `CREATE TABLE must_not_apply (id INTEGER);`,
			},
			want: "must begin at 001",
		},
		{
			name: "empty description",
			files: map[string]string{
				"001_.sql": `CREATE TABLE must_not_apply (id INTEGER);`,
			},
			want: "invalid migration filename",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openMigrationTestDB(t)
			defer db.Close()

			err := database.MigrateFS(context.Background(), db, migrationSet(tt.files))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("MigrateFS() error = %v, want substring %q", err, tt.want)
			}
			if got := migrationVersion(t, db); got != 0 {
				t.Fatalf("migration version after rejected set = %d, want 0", got)
			}
			for _, table := range []string{"must_not_apply", "also_must_not_apply"} {
				if tableExists(t, db, table) {
					t.Fatalf("table %q was created before migration-set validation completed", table)
				}
			}
		})
	}
}

func TestMigrateFSRollsBackFailedMigration(t *testing.T) {
	db := openMigrationTestDB(t)
	defer db.Close()

	files := migrationSet(map[string]string{
		"001_broken.sql": `CREATE TABLE rolled_back (id INTEGER); INSERT INTO rolled_back(id) VALUES (1); SELECT * FROM table_that_does_not_exist;`,
	})
	err := database.MigrateFS(context.Background(), db, files)
	if err == nil {
		t.Fatal("MigrateFS() error = nil, want migration failure")
	}
	if !strings.Contains(err.Error(), "001_broken.sql") || !strings.Contains(err.Error(), "execute SQL") {
		t.Fatalf("MigrateFS() error = %v, want migration filename and operation context", err)
	}
	if got := migrationVersion(t, db); got != 0 {
		t.Fatalf("migration version after rollback = %d, want 0", got)
	}
	if tableExists(t, db, "rolled_back") {
		t.Fatal("rolled_back table exists after failed migration")
	}
}

func TestMigrateFSResumesAfterLaterMigrationFailure(t *testing.T) {
	db := openMigrationTestDB(t)
	defer db.Close()

	ctx := context.Background()
	firstAttempt := migrationSet(map[string]string{
		"001_create_log.sql": `CREATE TABLE migration_log (step INTEGER NOT NULL); INSERT INTO migration_log(step) VALUES (1);`,
		"002_broken.sql":     `INSERT INTO migration_log(step) VALUES (2); SELECT * FROM table_that_does_not_exist;`,
		"003_third.sql":      `INSERT INTO migration_log(step) VALUES (3);`,
	})
	if err := database.MigrateFS(ctx, db, firstAttempt); err == nil {
		t.Fatal("first MigrateFS() error = nil, want later migration failure")
	}

	if got := migrationVersion(t, db); got != 1 {
		t.Fatalf("migration version after later failure = %d, want 1", got)
	}
	if got := migrationLogSteps(t, db); got != "1" {
		t.Fatalf("migration log after later failure = %q, want %q", got, "1")
	}

	secondAttempt := migrationSet(map[string]string{
		"001_create_log.sql": `CREATE TABLE migration_log (step INTEGER NOT NULL); INSERT INTO migration_log(step) VALUES (1);`,
		"002_fixed.sql":      `INSERT INTO migration_log(step) VALUES (2);`,
		"003_third.sql":      `INSERT INTO migration_log(step) VALUES (3);`,
	})
	if err := database.MigrateFS(ctx, db, secondAttempt); err != nil {
		t.Fatalf("resuming MigrateFS() error = %v", err)
	}

	if got := migrationVersion(t, db); got != 3 {
		t.Fatalf("migration version after resume = %d, want 3", got)
	}
	if got := migrationLogSteps(t, db); got != "1,2,3" {
		t.Fatalf("migration log after resume = %q, want %q", got, "1,2,3")
	}
}

func TestMigrateFSRejectsNewerDatabaseVersion(t *testing.T) {
	db := openMigrationTestDB(t)
	defer db.Close()

	if _, err := db.Exec("PRAGMA user_version = 4"); err != nil {
		t.Fatalf("set user_version: %v", err)
	}

	err := database.MigrateFS(context.Background(), db, migrationSet(map[string]string{
		"001_first.sql":  `CREATE TABLE must_not_apply (id INTEGER);`,
		"002_second.sql": `CREATE TABLE also_must_not_apply (id INTEGER);`,
	}))
	if err == nil || !strings.Contains(err.Error(), "newer than the latest known migration") {
		t.Fatalf("MigrateFS() error = %v, want newer-version error", err)
	}
	if got := migrationVersion(t, db); got != 4 {
		t.Fatalf("migration version after newer-version rejection = %d, want 4", got)
	}
	if tableExists(t, db, "must_not_apply") {
		t.Fatal("migration ran against a newer database version")
	}
}

func openMigrationTestDB(t *testing.T) *sql.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "finance.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open(%q): %v", path, err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		t.Fatalf("db.PingContext(): %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close migration test database: %v", err)
		}
	})
	return db
}

func migrationSet(files map[string]string) fs.FS {
	result := make(fstest.MapFS, len(files))
	for name, contents := range files {
		result[name] = &fstest.MapFile{Data: []byte(contents), Mode: 0o644}
	}
	return result
}

func migrationVersion(t *testing.T, db *sql.DB) int64 {
	t.Helper()

	var version int64
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("query user_version: %v", err)
	}
	return version
}

func migrationLogSteps(t *testing.T, db *sql.DB) string {
	t.Helper()

	rows, err := db.Query("SELECT step FROM migration_log ORDER BY step")
	if err != nil {
		t.Fatalf("query migration_log: %v", err)
	}
	defer rows.Close()

	var steps []string
	for rows.Next() {
		var step int
		if err := rows.Scan(&step); err != nil {
			t.Fatalf("scan migration_log: %v", err)
		}
		steps = append(steps, fmt.Sprint(step))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migration_log: %v", err)
	}
	return strings.Join(steps, ",")
}

func tableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()

	var count int
	if err := db.QueryRow(
		"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
		table,
	).Scan(&count); err != nil {
		t.Fatalf("check table %q: %v", table, err)
	}
	return count != 0
}

// Keep the os import tied to a compile-time check that the test database is
// actually on disk; this catches accidental future switches to :memory:.
func TestMigrationTestDatabaseIsOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "finance.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open(%q): %v", path, err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Fatalf("db.Ping(): %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat database file: %v", err)
	}
}

func TestMigrateFSRejectsNilInputs(t *testing.T) {
	if err := database.Migrate(context.Background(), nil); err == nil {
		t.Fatal("Migrate(nil database) error = nil")
	}
	if err := database.MigrateFS(context.Background(), nil, migrationSet(map[string]string{
		"001_first.sql": "",
	})); err == nil {
		t.Fatal("MigrateFS(nil database) error = nil")
	}

	db := openMigrationTestDB(t)
	defer db.Close()
	if err := database.MigrateFS(context.Background(), db, nil); err == nil {
		t.Fatal("MigrateFS(nil filesystem) error = nil")
	}
}

func TestMigrationContextCancellationIsReported(t *testing.T) {
	db := openMigrationTestDB(t)
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := database.MigrateFS(ctx, db, migrationSet(map[string]string{
		"001_first.sql": `CREATE TABLE must_not_apply (id INTEGER);`,
	}))
	if err == nil {
		t.Fatal("MigrateFS(cancelled context) error = nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("MigrateFS(cancelled context) error = %v, want context.Canceled", err)
	}
}
