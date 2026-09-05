package budget

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/rollover"
)

var (
	ErrNotFound              = errors.New("monthly budget not found")
	ErrMonthlyBudgetNotFound = ErrNotFound
)

type SetChange struct {
	Budget  contract.Budget
	Created bool
}

type SetResult struct {
	Month   string
	Changes []SetChange
}

type NotFoundError struct {
	Month              string
	LatestEarlierMonth *string
}

func (e *NotFoundError) Error() string {
	if e == nil {
		return ErrNotFound.Error()
	}
	return fmt.Sprintf("monthly budget not found for %s", e.Month)
}

func (e *NotFoundError) Is(target error) bool {
	return target == ErrNotFound
}

type setOperation struct {
	categoryID int64
	amount     int64
	created    bool
}

// Set edits allocations on an existing current or past month snapshot.
func (s *Store) Set(ctx context.Context, month string, allocations []Allocation) (SetResult, []contract.FieldIssue, error) {
	now := time.Now()
	if s != nil && s.Now != nil {
		now = s.Now()
	}

	normalized, fields := validateSet(month, allocations, now)
	if len(fields) != 0 {
		return SetResult{}, fields, nil
	}
	if s == nil || s.DB == nil {
		return SetResult{}, nil, errors.New("budget store database is nil")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return SetResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	exists, err := budgetMonthExists(ctx, tx, month)
	if err != nil {
		return SetResult{}, nil, err
	}
	if !exists {
		return SetResult{}, nil, missingMonthError(ctx, tx, month)
	}

	existingAmounts, err := loadMonthAmounts(ctx, tx, month)
	if err != nil {
		return SetResult{}, nil, err
	}

	operations := make([]setOperation, 0, len(normalized))
	for _, allocation := range normalized {
		category, err := resolveActiveCategory(ctx, tx, allocation.category)
		if err != nil {
			return SetResult{}, nil, err
		}
		_, exists := existingAmounts[category.ID]
		operations = append(operations, setOperation{
			categoryID: category.ID,
			amount:     allocation.amount,
			created:    !exists,
		})
	}

	merged := make(map[int64]int64, len(existingAmounts)+len(operations))
	for categoryID, amount := range existingAmounts {
		merged[categoryID] = amount
	}
	for _, operation := range operations {
		merged[operation.categoryID] = operation.amount
	}
	var total int64
	for _, amount := range merged {
		next, ok := checkedAdd(total, amount)
		if !ok {
			return SetResult{}, []contract.FieldIssue{{
				Field:  "budgets",
				Reason: "total must fit the supported amount range",
			}}, nil
		}
		total = next
	}

	for _, operation := range operations {
		if !operation.created {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO budgets (category_id, month, amount_hundredths)
			VALUES (?, ?, ?)
		`, operation.categoryID, month, operation.amount); err != nil {
			return SetResult{}, nil, err
		}
	}
	for _, operation := range operations {
		if operation.created {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE budgets
			SET amount_hundredths = ?,
			    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
			WHERE category_id = ? AND month = ?
		`, operation.amount, operation.categoryID, month); err != nil {
			return SetResult{}, nil, err
		}
	}
	if err := rollover.ValidateOutgoing(ctx, tx); err != nil {
		return SetResult{}, nil, err
	}

	categoryIDs := make([]int64, 0, len(operations))
	createdByCategory := make(map[int64]bool, len(operations))
	for _, operation := range operations {
		categoryIDs = append(categoryIDs, operation.categoryID)
		createdByCategory[operation.categoryID] = operation.created
	}

	budgets, err := listBudgetsForMonthCategoryIDs(ctx, tx, month, categoryIDs)
	if err != nil {
		return SetResult{}, nil, err
	}
	changes := make([]SetChange, 0, len(budgets))
	for _, row := range budgets {
		changes = append(changes, SetChange{
			Budget:  row,
			Created: createdByCategory[row.CategoryID],
		})
	}

	if err := tx.Commit(); err != nil {
		return SetResult{}, nil, err
	}
	return SetResult{Month: month, Changes: changes}, nil, nil
}

func validateSet(month string, allocations []Allocation, now time.Time) ([]normalizedAllocation, []contract.FieldIssue) {
	fields := make([]contract.FieldIssue, 0)
	parsedMonth, monthErr := contract.ParseMonth(month)
	if monthErr != nil {
		fields = append(fields, contract.FieldIssue{
			Field:  "month",
			Reason: "must be a valid YYYY-MM month",
		})
	} else if issue := futureMonthIssue(parsedMonth, now); issue != nil {
		fields = append(fields, *issue)
	}

	if len(allocations) == 0 {
		fields = append(fields, contract.FieldIssue{
			Field:  "budgets",
			Reason: "must contain at least one allocation",
		})
	}

	normalized, _, allocFields := validateAllocations("budgets", allocations)
	fields = append(fields, allocFields...)
	return normalized, fields
}

func missingMonthError(ctx context.Context, tx *sql.Tx, month string) error {
	latest, found, err := lookupLatestEarlierMonth(ctx, tx, month)
	if err != nil {
		return err
	}
	notFound := &NotFoundError{Month: month}
	if found {
		notFound.LatestEarlierMonth = &latest
	}
	return notFound
}

func loadMonthAmounts(ctx context.Context, tx *sql.Tx, month string) (map[int64]int64, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT category_id, amount_hundredths
		FROM budgets
		WHERE month = ?
	`, month)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	amounts := make(map[int64]int64)
	for rows.Next() {
		var categoryID, amount int64
		if err := rows.Scan(&categoryID, &amount); err != nil {
			return nil, err
		}
		amounts[categoryID] = amount
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return amounts, nil
}

func listBudgetsForMonthCategoryIDs(ctx context.Context, tx *sql.Tx, month string, categoryIDs []int64) ([]contract.Budget, error) {
	if len(categoryIDs) == 0 {
		return make([]contract.Budget, 0), nil
	}

	placeholders := strings.Repeat("?,", len(categoryIDs))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, 1+len(categoryIDs))
	args = append(args, month)
	for _, categoryID := range categoryIDs {
		args = append(args, categoryID)
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT
			b.id,
			b.month,
			b.category_id,
			c.name,
			b.amount_hundredths,
			b.created_at,
			b.updated_at
		FROM budgets AS b
		INNER JOIN categories AS c ON c.id = b.category_id
		WHERE b.month = ? AND b.category_id IN (`+placeholders+`)
		ORDER BY c.name COLLATE NOCASE ASC, b.id ASC
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	budgets := make([]contract.Budget, 0, len(categoryIDs))
	for rows.Next() {
		var budget contract.Budget
		var amount int64
		if err := rows.Scan(
			&budget.ID,
			&budget.Month,
			&budget.CategoryID,
			&budget.Category,
			&amount,
			&budget.CreatedAt,
			&budget.UpdatedAt,
		); err != nil {
			return nil, err
		}
		formatted, err := contract.FormatAmount(amount)
		if err != nil {
			return nil, err
		}
		budget.Amount = formatted
		budgets = append(budgets, budget)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(budgets) != len(categoryIDs) {
		return nil, fmt.Errorf("changed budget rows returned %d, expected %d", len(budgets), len(categoryIDs))
	}
	return budgets, nil
}
