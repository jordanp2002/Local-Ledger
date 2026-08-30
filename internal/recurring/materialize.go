package recurring

import (
	"context"
	"errors"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

func (s *Store) MaterializeDue(ctx context.Context) (MaterializeDueResult, error) {
	if s == nil || s.DB == nil {
		return MaterializeDueResult{}, errors.New("recurring store database is nil")
	}
	if s.Now == nil {
		return MaterializeDueResult{}, errors.New("recurring store clock is nil")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return MaterializeDueResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	now := s.Now()
	timestamp := now.UTC().Format("2006-01-02T15:04:05.000Z")
	preview, err := calculateDue(ctx, tx, now)
	if err != nil {
		return MaterializeDueResult{}, err
	}

	if len(preview.Blocked) > 0 {
		first := preview.Blocked[0]
		return MaterializeDueResult{}, &RecurringCategoryInactiveError{
			RecurringTransactionID: first.RecurringTransactionID,
			Merchant:               first.Merchant,
			Category:               first.Category,
			DueDate:                first.DueDate,
		}
	}

	if len(preview.DueTransactions) == 0 {
		if err := tx.Commit(); err != nil {
			return MaterializeDueResult{}, err
		}
		return MaterializeDueResult{
			AsOfDate:     preview.AsOfDate,
			Month:        preview.Month,
			Created:      0,
			TotalAmount:  "0.00",
			Transactions: []contract.Transaction{},
		}, nil
	}

	createdList := make([]contract.Transaction, 0, len(preview.DueTransactions))
	for _, item := range preview.DueTransactions {
		amountHundredths, err := contract.ParseAmount(item.Amount)
		if err != nil {
			return MaterializeDueResult{}, err
		}

		res, err := tx.ExecContext(ctx, `
			INSERT INTO transactions (
				merchant, amount_hundredths, category_id, date, note, created_at, updated_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, item.Merchant, amountHundredths, item.CategoryID, item.DueDate, item.Note, timestamp, timestamp)
		if err != nil {
			return MaterializeDueResult{}, err
		}

		txnID, err := res.LastInsertId()
		if err != nil {
			return MaterializeDueResult{}, err
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO recurring_transaction_runs (
				recurring_transaction_id, month, transaction_id, created_at
			)
			VALUES (?, ?, ?, ?)
		`, item.RecurringTransactionID, preview.Month, txnID, timestamp); err != nil {
			return MaterializeDueResult{}, err
		}

		createdList = append(createdList, contract.Transaction{
			ID:         txnID,
			Amount:     item.Amount,
			Merchant:   item.Merchant,
			Date:       item.DueDate,
			CategoryID: item.CategoryID,
			Category:   item.Category,
			Note:       item.Note,
			CreatedAt:  timestamp,
			UpdatedAt:  timestamp,
		})
	}

	if err := tx.Commit(); err != nil {
		return MaterializeDueResult{}, err
	}

	return MaterializeDueResult{
		AsOfDate:     preview.AsOfDate,
		Month:        preview.Month,
		Created:      int64(len(createdList)),
		TotalAmount:  preview.TotalAmount,
		Transactions: createdList,
	}, nil
}
