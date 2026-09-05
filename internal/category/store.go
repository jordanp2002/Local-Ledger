// Package category implements maintenance for spending categories.
package category

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
)

type Store struct {
	DB *sql.DB
	// Now supplies the current local time for month derivation. nil uses time.Now.
	Now func() time.Time
}

// LocalMonth formats YYYY-MM in t's location without converting to UTC first.
func LocalMonth(t time.Time) string {
	return t.Format("2006-01")
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

type queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const categoryColumns = `id, name, active, created_at, updated_at`

func (s *Store) Create(ctx context.Context, name string) (contract.Category, bool, bool, error) {
	normalized, err := validateName(name)
	if err != nil {
		return contract.Category{}, false, false, err
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return contract.Category{}, false, false, err
	}
	defer func() { _ = tx.Rollback() }()

	existing, found, err := lookupCategory(ctx, tx, normalized)
	if err != nil {
		return contract.Category{}, false, false, err
	}
	if found && existing.Active {
		return existing, false, false, ErrAlreadyExists
	}
	if found {
		if _, err := tx.ExecContext(ctx, `
			UPDATE categories
			SET active = 1,
			    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
			WHERE id = ?
		`, existing.ID); err != nil {
			return contract.Category{}, false, false, err
		}
		reactivated, err := getCategoryByID(ctx, tx, existing.ID)
		if err != nil {
			return contract.Category{}, false, false, err
		}
		if err := tx.Commit(); err != nil {
			return contract.Category{}, false, false, err
		}
		return reactivated, false, true, nil
	}

	result, err := tx.ExecContext(ctx, `INSERT INTO categories (name) VALUES (?)`, normalized)
	if err != nil {
		return contract.Category{}, false, false, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return contract.Category{}, false, false, err
	}
	created, err := getCategoryByID(ctx, tx, id)
	if err != nil {
		return contract.Category{}, false, false, err
	}
	if err := tx.Commit(); err != nil {
		return contract.Category{}, false, false, err
	}
	return created, true, false, nil
}

func (s *Store) List(ctx context.Context) ([]contract.Category, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT `+categoryColumns+`
		FROM categories
		WHERE active = 1
		ORDER BY name COLLATE NOCASE ASC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	categories := make([]contract.Category, 0)
	for rows.Next() {
		cat, err := scanCategory(rows)
		if err != nil {
			return nil, err
		}
		categories = append(categories, cat)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return categories, nil
}

func lookupCategory(ctx context.Context, q queryer, name string) (contract.Category, bool, error) {
	cat, err := scanCategory(q.QueryRowContext(ctx, `
		SELECT `+categoryColumns+`
		FROM categories
		WHERE name = ? COLLATE NOCASE
	`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return contract.Category{}, false, nil
	}
	if err != nil {
		return contract.Category{}, false, err
	}
	return cat, true, nil
}

func getCategoryByID(ctx context.Context, q queryer, id int64) (contract.Category, error) {
	return scanCategory(q.QueryRowContext(ctx, `
		SELECT `+categoryColumns+`
		FROM categories
		WHERE id = ?
	`, id))
}

func scanCategory(row interface{ Scan(dest ...any) error }) (contract.Category, error) {
	var cat contract.Category
	err := row.Scan(&cat.ID, &cat.Name, &cat.Active, &cat.CreatedAt, &cat.UpdatedAt)
	return cat, err
}
