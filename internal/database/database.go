package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const sqliteDriverName = "sqlite"

// Open opens path, verifies the connection settings, and applies the embedded
// schema migrations. The caller owns the returned database and must close it
// after a successful call.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	if path == "" {
		return nil, errors.New("open database: path is empty")
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("open database: path %q is not absolute", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("open database: create parent directory: %w", err)
	}

	dsn := sqliteDSN(path)

	db, err := sql.Open(sqliteDriverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: sql.Open: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	startupFailure := func(cause error) (*sql.DB, error) {
		if closeErr := db.Close(); closeErr != nil {
			return nil, errors.Join(cause, fmt.Errorf("open database: close after startup failure: %w", closeErr))
		}
		return nil, cause
	}

	if err := db.PingContext(ctx); err != nil {
		return startupFailure(fmt.Errorf("open database: ping: %w", err))
	}

	var foreignKeys int64
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return startupFailure(fmt.Errorf("open database: verify foreign keys: %w", err))
	}
	if foreignKeys != 1 {
		return startupFailure(fmt.Errorf("open database: foreign-key enforcement is %d, want 1", foreignKeys))
	}

	if err := Migrate(ctx, db); err != nil {
		return startupFailure(fmt.Errorf("open database: migrate: %w", err))
	}

	return db, nil
}

func sqliteDSN(path string) string {
	uri := &url.URL{
		Scheme: "file",
		Path:   path,
	}
	query := uri.Query()
	query.Set("_pragma", "foreign_keys(1)")
	uri.RawQuery = query.Encode()
	return uri.String()
}
