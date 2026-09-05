package category

import (
	"context"
	"errors"
	"fmt"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
)

// Rename changes only a category's name, preserving its identity and related
// records.
func (s *Store) Rename(ctx context.Context, name, newName string) (contract.Category, string, bool, error) {
	name, newName, validationErr := validateRenameInputs(name, newName)
	if validationErr != nil {
		return contract.Category{}, "", false, validationErr
	}
	if s == nil || s.DB == nil {
		return contract.Category{}, "", false, errors.New("category store database is nil")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return contract.Category{}, "", false, err
	}
	defer func() { _ = tx.Rollback() }()

	existing, found, err := lookupCategory(ctx, tx, name)
	if err != nil {
		return contract.Category{}, "", false, err
	}
	if !found {
		return contract.Category{}, "", false, ErrNotFound
	}

	conflict, conflictFound, err := lookupCategory(ctx, tx, newName)
	if err != nil {
		return contract.Category{}, "", false, err
	}
	if conflictFound && conflict.ID != existing.ID {
		return contract.Category{}, "", false, &AlreadyExistsError{Category: conflict}
	}

	if newName == existing.Name {
		if err := tx.Commit(); err != nil {
			return contract.Category{}, "", false, err
		}
		return existing, existing.Name, false, nil
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE categories
		SET name = ?,
		    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE id = ?
	`, newName, existing.ID)
	if err != nil {
		return contract.Category{}, "", false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return contract.Category{}, "", false, err
	}
	if affected != 1 {
		return contract.Category{}, "", false, fmt.Errorf("renamed %d categories, want 1", affected)
	}

	renamed, err := getCategoryByID(ctx, tx, existing.ID)
	if err != nil {
		return contract.Category{}, "", false, err
	}
	if err := tx.Commit(); err != nil {
		return contract.Category{}, "", false, err
	}
	return renamed, existing.Name, true, nil
}
