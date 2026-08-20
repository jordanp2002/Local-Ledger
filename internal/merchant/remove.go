package merchant

import (
	"context"
	"errors"
	"fmt"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

// Remove deletes one known-merchant default and returns its pre-delete
// canonical record. Transactions and categories are not modified.
func (s *Store) Remove(ctx context.Context, merchantName string) (contract.KnownMerchant, error) {
	merchantName, validationErr := validateRemoveInput(merchantName)
	if validationErr != nil {
		return contract.KnownMerchant{}, validationErr
	}
	if s == nil || s.DB == nil {
		return contract.KnownMerchant{}, errors.New("merchant store database is nil")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return contract.KnownMerchant{}, err
	}
	defer func() { _ = tx.Rollback() }()

	existing, found, err := lookupKnownMerchant(ctx, tx, merchantName)
	if err != nil {
		return contract.KnownMerchant{}, err
	}
	if !found {
		return contract.KnownMerchant{}, &NotFoundError{Requested: merchantName}
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM known_merchants WHERE id = ?`, existing.ID)
	if err != nil {
		return contract.KnownMerchant{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return contract.KnownMerchant{}, err
	}
	if affected != 1 {
		return contract.KnownMerchant{}, fmt.Errorf("removed %d known merchants, want 1", affected)
	}

	if err := tx.Commit(); err != nil {
		return contract.KnownMerchant{}, err
	}
	return *existing, nil
}
