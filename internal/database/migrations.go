package database

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
)

//go:embed migrations/*.sql
var embeddedMigrationFiles embed.FS

var migrationFilenamePattern = regexp.MustCompile(`^([0-9]{3})_([A-Za-z0-9][A-Za-z0-9._-]*)\.sql$`)

type migration struct {
	version  int
	filename string
	contents []byte
}

func Migrate(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errors.New("run migrations: database handle is nil")
	}

	migrationFS, err := fs.Sub(embeddedMigrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("run migrations: locate embedded migrations: %w", err)
	}

	return migrateFS(ctx, db, migrationFS)
}

// MigrateFS applies pending migrations from migrationFS.
// Tests use it to provide deterministic migration sets.
func MigrateFS(ctx context.Context, db *sql.DB, migrationFS fs.FS) error {
	if db == nil {
		return errors.New("run migrations: database handle is nil")
	}
	if migrationFS == nil {
		return errors.New("run migrations: migration filesystem is nil")
	}

	return migrateFS(ctx, db, migrationFS)
}

// migrateFS contains the shared migration logic.
// It validates all migrations before applying any.
func migrateFS(ctx context.Context, db *sql.DB, migrationFS fs.FS) error {
	migrations, err := discoverMigrations(migrationFS)
	if err != nil {
		return fmt.Errorf("run migrations: discover migration files: %w", err)
	}

	currentVersion, err := currentMigrationVersion(ctx, db)
	if err != nil {
		return fmt.Errorf("run migrations: read PRAGMA user_version: %w", err)
	}
	if currentVersion < 0 {
		return fmt.Errorf("run migrations: invalid negative PRAGMA user_version %d", currentVersion)
	}
	if currentVersion > int64(migrations[len(migrations)-1].version) {
		return fmt.Errorf(
			"run migrations: database schema version %d is newer than the latest known migration %d",
			currentVersion,
			migrations[len(migrations)-1].version,
		)
	}

	for _, migration := range migrations {
		if migration.version <= int(currentVersion) {
			continue
		}
		if err := applyMigration(ctx, db, migration); err != nil {
			return err
		}
	}

	return nil
}

func discoverMigrations(migrationFS fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, ".")
	if err != nil {
		return nil, fmt.Errorf("read migration directory: %w", err)
	}
	if len(entries) == 0 {
		return nil, errors.New("migration directory is empty")
	}

	migrations := make([]migration, 0, len(entries))
	seenVersions := make(map[int]string, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		match := migrationFilenamePattern.FindStringSubmatch(name)
		if entry.IsDir() || match == nil {
			return nil, fmt.Errorf("invalid migration filename %q: expected NNN_description.sql", name)
		}

		version, err := strconv.Atoi(match[1])
		if err != nil {
			// The regular expression limits this to three ASCII digits, but keep
			// the conversion failure contextual if the parser changes later.
			return nil, fmt.Errorf("parse migration version from %q: %w", name, err)
		}
		if version == 0 {
			return nil, fmt.Errorf("invalid migration filename %q: migration versions begin at 001", name)
		}
		if previous, exists := seenVersions[version]; exists {
			return nil, fmt.Errorf("duplicate migration version %03d in %q and %q", version, previous, name)
		}

		contents, err := fs.ReadFile(migrationFS, name)
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", name, err)
		}

		seenVersions[version] = name
		migrations = append(migrations, migration{
			version:  version,
			filename: name,
			contents: contents,
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	if migrations[0].version != 1 {
		return nil, fmt.Errorf("migration sequence must begin at 001, found %03d", migrations[0].version)
	}
	for i := 1; i < len(migrations); i++ {
		want := migrations[i-1].version + 1
		if migrations[i].version != want {
			return nil, fmt.Errorf(
				"migration sequence has a gap: expected %03d after %03d, found %03d",
				want,
				migrations[i-1].version,
				migrations[i].version,
			)
		}
	}

	return migrations, nil
}

func currentMigrationVersion(ctx context.Context, db *sql.DB) (int64, error) {
	var version int64
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}

func applyMigration(ctx context.Context, db *sql.DB, migration migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("run migration %03d (%s): begin transaction: %w", migration.version, migration.filename, err)
	}

	rollback := func(cause error) error {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return errors.Join(
				cause,
				fmt.Errorf("run migration %03d (%s): rollback transaction: %w", migration.version, migration.filename, rollbackErr),
			)
		}
		return cause
	}

	if _, err := tx.ExecContext(ctx, string(migration.contents)); err != nil {
		return rollback(fmt.Errorf("run migration %03d (%s): execute SQL: %w", migration.version, migration.filename, err))
	}

	versionStatement := fmt.Sprintf("PRAGMA user_version = %d", migration.version)
	if _, err := tx.ExecContext(ctx, versionStatement); err != nil {
		return rollback(fmt.Errorf("run migration %03d (%s): set PRAGMA user_version: %w", migration.version, migration.filename, err))
	}

	if err := tx.Commit(); err != nil {
		return rollback(fmt.Errorf("run migration %03d (%s): commit transaction: %w", migration.version, migration.filename, err))
	}

	return nil
}
