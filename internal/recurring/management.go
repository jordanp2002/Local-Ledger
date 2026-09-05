package recurring

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
)

type NotePatch struct {
	Present bool
	Value   *string
}

type UpdateInput struct {
	ID             int64
	Merchant       *string
	MerchantNull   bool
	Amount         *string
	AmountNull     bool
	Category       *string
	CategoryNull   bool
	DayOfMonth     *int64
	DayOfMonthNull bool
	Note           NotePatch
}

type UpdateResult struct {
	RecurringTransaction contract.RecurringTransaction
	Changed              bool
}

type EnableResult struct {
	RecurringTransaction contract.RecurringTransaction
	Changed              bool
}

type PreviewUpcomingResult struct {
	AsOfDate             string
	Month                string
	TotalAmount          string
	UpcomingTransactions []contract.UpcomingTransaction
	Blocked              []contract.BlockedDueTransaction
}

// Update patches one recurring expense template without changing its history.
func (s *Store) Update(ctx context.Context, in UpdateInput) (UpdateResult, []contract.FieldIssue, error) {
	validated, issues := validateUpdate(in)
	if len(issues) != 0 {
		return UpdateResult{}, issues, nil
	}
	if s == nil || s.DB == nil {
		return UpdateResult{}, nil, errors.New("recurring store database is nil")
	}
	if s.Now == nil {
		return UpdateResult{}, nil, errors.New("recurring store clock is nil")
	}

	now := s.Now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return UpdateResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := getRecurringByID(ctx, tx, validated.id)
	if errors.Is(err, sql.ErrNoRows) {
		return UpdateResult{}, nil, &NotFoundError{ID: validated.id}
	}
	if err != nil {
		return UpdateResult{}, nil, err
	}

	merchant := existing.Merchant
	if validated.merchant != nil {
		merchant = *validated.merchant
	}
	existingAmountHundredths, err := contract.ParseAmount(existing.Amount)
	if err != nil {
		return UpdateResult{}, nil, err
	}
	amountHundredths := existingAmountHundredths
	if validated.amountHundredths != nil {
		amountHundredths = *validated.amountHundredths
	}
	categoryID := existing.CategoryID
	if validated.category != nil {
		category, err := resolveActiveCategory(ctx, tx, *validated.category)
		if err != nil {
			return UpdateResult{}, nil, err
		}
		categoryID = category.ID
	}
	dayOfMonth := existing.DayOfMonth
	if validated.dayOfMonth != nil {
		dayOfMonth = *validated.dayOfMonth
	}
	note := sql.NullString{}
	if existing.Note != nil {
		note = sql.NullString{String: *existing.Note, Valid: true}
	}
	if validated.note != nil {
		note = *validated.note
	}

	changed := merchant != existing.Merchant || amountHundredths != existingAmountHundredths ||
		categoryID != existing.CategoryID || dayOfMonth != existing.DayOfMonth || !sameNote(note, existing.Note)
	if !changed {
		return UpdateResult{RecurringTransaction: existing, Changed: false}, nil, nil
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE recurring_transactions
		SET merchant = ?,
		    amount_hundredths = ?,
		    category_id = ?,
		    day_of_month = ?,
		    note = ?,
		    updated_at = ?
		WHERE id = ?
	`, merchant, amountHundredths, categoryID, dayOfMonth, note,
		now.UTC().Format("2006-01-02T15:04:05.000Z"), validated.id); err != nil {
		return UpdateResult{}, nil, err
	}

	updated, err := getRecurringByID(ctx, tx, validated.id)
	if err != nil {
		return UpdateResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return UpdateResult{}, nil, err
	}
	return UpdateResult{RecurringTransaction: updated, Changed: true}, nil, nil
}

func validateUpdate(in UpdateInput) (validatedUpdate, []contract.FieldIssue) {
	issues := make([]contract.FieldIssue, 0)
	validated := validatedUpdate{id: in.ID}
	if issue := validateID(in.ID); issue != nil {
		issues = append(issues, *issue)
	}
	if !in.MerchantNull && in.Merchant == nil && !in.AmountNull && in.Amount == nil &&
		!in.CategoryNull && in.Category == nil && !in.DayOfMonthNull && in.DayOfMonth == nil && !in.Note.Present {
		issues = append(issues, contract.FieldIssue{
			Field:  "id",
			Reason: "at least one of merchant, amount, category, day_of_month, or note must be supplied",
		})
	}

	if in.MerchantNull {
		issues = append(issues, contract.FieldIssue{Field: "merchant", Reason: "must not be null"})
	} else if in.Merchant != nil {
		if merchant, issue := validateMerchant(*in.Merchant); issue != nil {
			issues = append(issues, *issue)
		} else {
			validated.merchant = &merchant
		}
	}

	if in.AmountNull {
		issues = append(issues, contract.FieldIssue{Field: "amount", Reason: "must not be null"})
	} else if in.Amount != nil {
		if amount, issue := validateAmount(*in.Amount); issue != nil {
			issues = append(issues, *issue)
		} else {
			validated.amountHundredths = &amount
		}
	}

	if in.CategoryNull {
		issues = append(issues, contract.FieldIssue{Field: "category", Reason: "must not be null"})
	} else if in.Category != nil {
		if category, issue := validateCategoryName(*in.Category); issue != nil {
			issues = append(issues, *issue)
		} else {
			validated.category = &category
		}
	}

	if in.DayOfMonthNull {
		issues = append(issues, contract.FieldIssue{Field: "day_of_month", Reason: "must not be null"})
	} else if in.DayOfMonth != nil {
		if day, issue := validateDayOfMonth(*in.DayOfMonth); issue != nil {
			issues = append(issues, *issue)
		} else {
			validated.dayOfMonth = &day
		}
	}

	if in.Note.Present {
		if in.Note.Value == nil {
			validated.note = &sql.NullString{}
		} else if note, issue := validateNote(in.Note.Value); issue != nil {
			issues = append(issues, *issue)
		} else {
			validated.note = &note
		}
	}
	return validated, issues
}

type validatedUpdate struct {
	id               int64
	merchant         *string
	amountHundredths *int64
	category         *string
	dayOfMonth       *int64
	note             *sql.NullString
}

func sameNote(value sql.NullString, existing *string) bool {
	if existing == nil {
		return !value.Valid
	}
	return value.Valid && value.String == *existing
}

// Enable reactivates one recurring expense template after validating its category.
func (s *Store) Enable(ctx context.Context, id int64) (EnableResult, []contract.FieldIssue, error) {
	if issue := validateID(id); issue != nil {
		return EnableResult{}, []contract.FieldIssue{*issue}, nil
	}
	if s == nil || s.DB == nil {
		return EnableResult{}, nil, errors.New("recurring store database is nil")
	}
	if s.Now == nil {
		return EnableResult{}, nil, errors.New("recurring store clock is nil")
	}
	now := s.Now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return EnableResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := getRecurringByID(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return EnableResult{}, nil, &NotFoundError{ID: id}
	}
	if err != nil {
		return EnableResult{}, nil, err
	}
	if !existing.CategoryActive {
		activeCategories, err := listActiveCategories(ctx, tx)
		if err != nil {
			return EnableResult{}, nil, err
		}
		category, found, err := lookupCategory(ctx, tx, existing.Category)
		if err != nil {
			return EnableResult{}, nil, err
		}
		if !found {
			return EnableResult{}, nil, errors.New("recurring category disappeared")
		}
		return EnableResult{}, nil, &CategoryInactiveError{Category: category, ActiveCategories: activeCategories}
	}
	if existing.Active {
		return EnableResult{RecurringTransaction: existing, Changed: false}, nil, nil
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE recurring_transactions
		SET active = 1, updated_at = ?
		WHERE id = ?
	`, now.UTC().Format("2006-01-02T15:04:05.000Z"), id); err != nil {
		return EnableResult{}, nil, err
	}
	updated, err := getRecurringByID(ctx, tx, id)
	if err != nil {
		return EnableResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return EnableResult{}, nil, err
	}
	return EnableResult{RecurringTransaction: updated, Changed: true}, nil, nil
}
