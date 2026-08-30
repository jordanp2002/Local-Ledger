package transaction

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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
	allocations      *[]AllocationInput
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
		in.Note.Present || in.Allocations != nil || in.AllocationsNull
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

	if in.AllocationsNull {
		fields = append(fields, contract.FieldIssue{Field: "allocations", Reason: "must not be null"})
	} else if in.Allocations != nil {
		if in.Amount != nil || in.AmountNull {
			fields = append(fields, contract.FieldIssue{Field: "amount", Reason: "cannot be supplied with allocations"})
		}
		if in.Category != nil || in.CategoryNull {
			fields = append(fields, contract.FieldIssue{Field: "category", Reason: "cannot be supplied with allocations"})
		}
		if len(*in.Allocations) == 0 {
			fields = append(fields, contract.FieldIssue{Field: "allocations", Reason: "must contain at least one item"})
		} else {
			validated.allocations = in.Allocations
			var total int64
			seen := make(map[string]struct{}, len(*in.Allocations))
			for i, allocation := range *in.Allocations {
				category, issue := validateCategoryName(allocation.Category)
				if issue != nil {
					issue.Field = fmt.Sprintf("allocations[%d].category", i)
					fields = append(fields, *issue)
				} else {
					key := strings.ToLower(category)
					if _, exists := seen[key]; exists {
						fields = append(fields, contract.FieldIssue{Field: fmt.Sprintf("allocations[%d].category", i), Reason: "must not repeat a category"})
					}
					seen[key] = struct{}{}
				}
				amount, amountIssue := validateAmount(allocation.Amount)
				if amountIssue != nil {
					amountIssue.Field = fmt.Sprintf("allocations[%d].amount", i)
					fields = append(fields, *amountIssue)
					continue
				}
				next, ok := checkedAdd(total, amount)
				if !ok {
					fields = append(fields, contract.FieldIssue{Field: "allocations", Reason: "total must fit the supported amount range"})
				} else {
					total = next
				}
			}
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

	date := loaded.Date
	if in.date != nil {
		date = *in.date
	}

	note := sql.NullString{}
	if loaded.Note != nil {
		note = sql.NullString{String: *loaded.Note, Valid: true}
	}
	if in.note != nil {
		note = *in.note
	}

	if in.allocations == nil && (in.amountHundredths != nil || in.category != nil) && len(loaded.Allocations) > 1 {
		return UpdateResult{}, nil, &SplitTransactionRequiresAllocationsError{ID: in.id}
	}

	var replacement []validatedAllocation
	if in.allocations != nil {
		replacement, err = resolveSplitAllocations(ctx, tx, *in.allocations)
		if err != nil {
			return UpdateResult{}, nil, err
		}
	}

	if in.allocations == nil && len(loaded.Allocations) == 1 {
		currentAmount, parseErr := contract.ParseAmount(loaded.Allocations[0].Amount)
		if parseErr != nil {
			return UpdateResult{}, nil, parseErr
		}
		newAmount := currentAmount
		if in.amountHundredths != nil {
			newAmount = *in.amountHundredths
		}
		newCategoryID := loaded.Allocations[0].CategoryID
		if in.category != nil {
			category, resolveErr := resolveActiveCategory(ctx, tx, *in.category)
			if resolveErr != nil {
				return UpdateResult{}, nil, resolveErr
			}
			newCategoryID = category.ID
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE transaction_allocations
			SET category_id = ?, amount_hundredths = ?
			WHERE transaction_id = ?
		`, newCategoryID, newAmount, in.id); err != nil {
			return UpdateResult{}, nil, err
		}
	}
	if in.allocations != nil && !sameAllocations(loaded.Allocations, replacement) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM transaction_allocations WHERE transaction_id = ?`, in.id); err != nil {
			return UpdateResult{}, nil, err
		}
		for _, allocation := range replacement {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO transaction_allocations (transaction_id, category_id, amount_hundredths)
				VALUES (?, ?, ?)
			`, in.id, allocation.categoryID, allocation.amount); err != nil {
				return UpdateResult{}, nil, err
			}
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE transactions
		SET merchant = ?, date = ?, note = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE id = ?
	`, merchant, date, note, in.id)
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

func sameAllocations(existing []contract.TransactionAllocation, replacement []validatedAllocation) bool {
	if len(existing) != len(replacement) {
		return false
	}
	existingByCategory := make(map[int64]string, len(existing))
	for _, allocation := range existing {
		existingByCategory[allocation.CategoryID] = allocation.Amount
	}
	for _, allocation := range replacement {
		amountText, found := existingByCategory[allocation.categoryID]
		if !found {
			return false
		}
		amount, err := contract.ParseAmount(amountText)
		if err != nil || amount != allocation.amount {
			return false
		}
	}
	return true
}
