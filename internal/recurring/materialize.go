package recurring

import (
	"context"
	"errors"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/rollover"
	"github.com/jordanp2002/Local-Ledger/internal/transaction"
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
	before, err := rollover.Snapshot(ctx, tx)
	if err != nil {
		return MaterializeDueResult{}, err
	}

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
			AsOfDate:       preview.AsOfDate,
			Month:          preview.Month,
			Created:        0,
			TotalAmount:    "0.00",
			Transactions:   []contract.Transaction{},
			RolloverOffers: []contract.RolloverOffer{},
		}, nil
	}

	createdList := make([]contract.Transaction, 0, len(preview.DueTransactions))
	for _, item := range preview.DueTransactions {
		amountHundredths, err := contract.ParseAmount(item.Amount)
		if err != nil {
			return MaterializeDueResult{}, err
		}

		recorded, err := transaction.AddInTx(ctx, tx, transaction.InTxInput{
			Merchant: item.Merchant,
			Date:     item.DueDate,
			Note:     item.Note,
			Allocations: []transaction.InTxAllocation{{
				CategoryID:       item.CategoryID,
				AmountHundredths: amountHundredths,
			}},
			CreatedAt: timestamp,
			UpdatedAt: timestamp,
		})
		if err != nil {
			return MaterializeDueResult{}, err
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO recurring_transaction_runs (
				recurring_transaction_id, month, transaction_id, created_at
			)
			VALUES (?, ?, ?, ?)
		`, item.RecurringTransactionID, preview.Month, recorded.ID, timestamp); err != nil {
			return MaterializeDueResult{}, err
		}

		createdList = append(createdList, recorded)
	}
	flattened := make([]rollover.OfferChange, 0)
	for _, recorded := range createdList {
		flattened = append(flattened, rollover.OfferChangesForTransaction(recorded)...)
	}
	offers, err := rollover.BuildOffers(ctx, tx, before, flattened)
	if err != nil {
		return MaterializeDueResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return MaterializeDueResult{}, err
	}

	return MaterializeDueResult{
		AsOfDate:       preview.AsOfDate,
		Month:          preview.Month,
		Created:        int64(len(createdList)),
		TotalAmount:    preview.TotalAmount,
		Transactions:   createdList,
		RolloverOffers: offers,
	}, nil
}
