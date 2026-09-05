package transaction

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/rollover"
)

func (s *Store) Remove(ctx context.Context, id int64) (contract.Transaction, []contract.FieldIssue, error) {
	if fields := validateRemove(id); len(fields) != 0 {
		return contract.Transaction{}, fields, nil
	}
	if s == nil || s.DB == nil {
		return contract.Transaction{}, nil, errors.New("transaction store database is nil")
	}
	return s.remove(ctx, id)
}

func validateRemove(id int64) []contract.FieldIssue {
	if id < 1 {
		return []contract.FieldIssue{{
			Field:  "id",
			Reason: "must be a positive integer",
		}}
	}
	return nil
}

func (s *Store) remove(ctx context.Context, id int64) (contract.Transaction, []contract.FieldIssue, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return contract.Transaction{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	loaded, err := getTransactionByID(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return contract.Transaction{}, nil, &TransactionNotFoundError{ID: id}
	}
	if err != nil {
		return contract.Transaction{}, nil, err
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM transactions WHERE id = ?`, id)
	if err != nil {
		return contract.Transaction{}, nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return contract.Transaction{}, nil, err
	}
	if affected != 1 {
		return contract.Transaction{}, nil, fmt.Errorf("deleted %d transactions, want 1", affected)
	}
	if err := rollover.ValidateOutgoing(ctx, tx); err != nil {
		return contract.Transaction{}, nil, err
	}

	if err := tx.Commit(); err != nil {
		return contract.Transaction{}, nil, err
	}
	return loaded, nil, nil
}
