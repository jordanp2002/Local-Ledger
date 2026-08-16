package transaction

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

type validatedUpdate struct {
	id               int64
	amountHundredths *int64
	merchant         *string
	category         *string
	date             *string
	note             *sql.NullString
}

// Update patches one existing transaction. It never reads or writes
// known_merchants or budgets.
func (s *Store) Update(ctx context.Context, in UpdateInput) (UpdateResult, []contract.FieldIssue, error) {
	now := time.Now()
	if s != nil && s.Now != nil {
		now = s.Now()
	}

	validated, fields := validateUpdate(in, now)
	if len(fields) != 0 {
		return UpdateResult{}, fields, nil
	}
	if s == nil || s.DB == nil {
		return UpdateResult{}, nil, errors.New("transaction store database is nil")
	}
	return s.update(ctx, validated)
}

func validateUpdate(in UpdateInput, now time.Time) (validatedUpdate, []contract.FieldIssue) {
	fields := make([]contract.FieldIssue, 0)
	validated := validatedUpdate{id: in.ID}

	if in.ID < 1 {
		fields = append(fields, contract.FieldIssue{
			Field:  "id",
			Reason: "must be a positive integer",
		})
	}

	hasMutable := in.Amount != nil || in.AmountNull ||
		in.Merchant != nil || in.MerchantNull ||
		in.Category != nil || in.CategoryNull ||
		in.Date != nil || in.DateNull ||
		in.Note.Present
	if !hasMutable {
		fields = append(fields, contract.FieldIssue{
			Field:  "id",
			Reason: "at least one of amount, merchant, category, date, or note must be supplied",
		})
	}

	if in.AmountNull {
		fields = append(fields, contract.FieldIssue{Field: "amount", Reason: "must not be null"})
	} else if in.Amount != nil {
		if amount, issue := validateAmount(*in.Amount); issue != nil {
			fields = append(fields, *issue)
		} else {
			validated.amountHundredths = &amount
		}
	}

	if in.MerchantNull {
		fields = append(fields, contract.FieldIssue{Field: "merchant", Reason: "must not be null"})
	} else if in.Merchant != nil {
		if merchant, issue := validateMerchant(*in.Merchant); issue != nil {
			fields = append(fields, *issue)
		} else {
			validated.merchant = &merchant
		}
	}

	if in.CategoryNull {
		fields = append(fields, contract.FieldIssue{Field: "category", Reason: "must not be null"})
	} else if in.Category != nil {
		if category, issue := validateCategoryName(*in.Category); issue != nil {
			fields = append(fields, *issue)
		} else {
			validated.category = &category
		}
	}

	today := LocalDate(now)
	if in.DateNull {
		fields = append(fields, contract.FieldIssue{Field: "date", Reason: "must not be null"})
	} else if in.Date != nil {
		if date, issue := validateDate(*in.Date, today); issue != nil {
			fields = append(fields, *issue)
		} else {
			validated.date = &date
		}
	}

	if in.Note.Present {
		if in.Note.Value == nil {
			note := sql.NullString{}
			validated.note = &note
		} else if note, issue := validateNote(*in.Note.Value); issue != nil {
			fields = append(fields, *issue)
		} else {
			validated.note = &note
		}
	}

	return validated, fields
}

func (s *Store) update(ctx context.Context, in validatedUpdate) (UpdateResult, []contract.FieldIssue, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return UpdateResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	loaded, err := getTransactionByID(ctx, tx, in.id)
	if errors.Is(err, sql.ErrNoRows) {
		return UpdateResult{}, nil, &TransactionNotFoundError{ID: in.id}
	}
	if err != nil {
		return UpdateResult{}, nil, err
	}

	merchant := loaded.Merchant
	if in.merchant != nil {
		merchant = *in.merchant
	}

	amountHundredths, err := contract.ParseAmount(loaded.Amount)
	if err != nil {
		return UpdateResult{}, nil, err
	}
	if in.amountHundredths != nil {
		amountHundredths = *in.amountHundredths
	}

	date := loaded.Date
	if in.date != nil {
		date = *in.date
	}

	categoryID := loaded.CategoryID
	if in.category != nil {
		category, err := resolveActiveCategory(ctx, tx, *in.category)
		if err != nil {
			return UpdateResult{}, nil, err
		}
		categoryID = category.ID
	}

	note := sql.NullString{}
	if loaded.Note != nil {
		note = sql.NullString{String: *loaded.Note, Valid: true}
	}
	if in.note != nil {
		note = *in.note
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE transactions
		SET merchant = ?,
		    amount_hundredths = ?,
		    date = ?,
		    category_id = ?,
		    note = ?,
		    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE id = ?
	`, merchant, amountHundredths, date, categoryID, note, in.id)
	if err != nil {
		return UpdateResult{}, nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return UpdateResult{}, nil, err
	}
	if affected != 1 {
		return UpdateResult{}, nil, fmt.Errorf("updated %d transactions, want 1", affected)
	}

	recorded, err := getTransactionByID(ctx, tx, in.id)
	if err != nil {
		return UpdateResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return UpdateResult{}, nil, err
	}
	return UpdateResult{Transaction: recorded}, nil, nil
}
