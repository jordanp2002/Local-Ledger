package merchant

import (
	"context"
	"errors"
	"fmt"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
)

// Rename changes only a known merchant's spelling, preserving its identity,
// category, and creation timestamp.
func (s *Store) Rename(ctx context.Context, merchantName, newMerchantName string) (contract.KnownMerchant, string, bool, error) {
	merchantName, newMerchantName, validationErr := validateRenameInputs(merchantName, newMerchantName)
	if validationErr != nil {
		return contract.KnownMerchant{}, "", false, validationErr
	}
	if s == nil || s.DB == nil {
		return contract.KnownMerchant{}, "", false, errors.New("merchant store database is nil")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return contract.KnownMerchant{}, "", false, err
	}
	defer func() { _ = tx.Rollback() }()

	existing, found, err := lookupKnownMerchant(ctx, tx, merchantName)
	if err != nil {
		return contract.KnownMerchant{}, "", false, err
	}
	if !found {
		return contract.KnownMerchant{}, "", false, &NotFoundError{Requested: merchantName}
	}

	conflict, conflictFound, err := lookupKnownMerchant(ctx, tx, newMerchantName)
	if err != nil {
		return contract.KnownMerchant{}, "", false, err
	}
	if conflictFound && conflict.ID != existing.ID {
		return contract.KnownMerchant{}, "", false, &AlreadyExistsError{KnownMerchant: *conflict}
	}

	if newMerchantName == existing.Merchant {
		if err := tx.Commit(); err != nil {
			return contract.KnownMerchant{}, "", false, err
		}
		return *existing, existing.Merchant, false, nil
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE known_merchants
		SET merchant = ?,
		    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE id = ?
	`, newMerchantName, existing.ID)
	if err != nil {
		return contract.KnownMerchant{}, "", false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return contract.KnownMerchant{}, "", false, err
	}
	if affected != 1 {
		return contract.KnownMerchant{}, "", false, fmt.Errorf("renamed %d known merchants, want 1", affected)
	}

	renamed, err := getKnownMerchantByID(ctx, tx, existing.ID)
	if err != nil {
		return contract.KnownMerchant{}, "", false, err
	}
	if err := tx.Commit(); err != nil {
		return contract.KnownMerchant{}, "", false, err
	}
	return renamed, existing.Merchant, true, nil
}
