// Package budget implements explicit current-month budget snapshots.
package budget

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

var (
	ErrAlreadyExists    = errors.New("monthly budget already exists")
	ErrCategoryNotFound = errors.New("category not found")
	ErrCategoryInactive = errors.New("category inactive")

	// ErrMonthlyBudgetAlreadyExists aliases ErrAlreadyExists.
	ErrMonthlyBudgetAlreadyExists = ErrAlreadyExists
)

// Allocation is one category amount in an explicit monthly budget request.
type Allocation struct {
	Category string
	Amount   string
}

// CreateResult is the canonical result of an explicit budget creation.
type CreateResult struct {
	Month       string
	TotalBudget string
	Budgets     []contract.Budget
}

// Store owns explicit budget validation and persistence.
type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

// AlreadyExistsError identifies a month with an existing snapshot.
type AlreadyExistsError struct {
	Month string
}

func (e *AlreadyExistsError) Error() string {
	if e == nil {
		return ErrAlreadyExists.Error()
	}
	return fmt.Sprintf("monthly budget already exists for %s", e.Month)
}

func (e *AlreadyExistsError) Is(target error) bool {
	return target == ErrAlreadyExists
}

// CategoryNotFoundError identifies a missing category.
type CategoryNotFoundError struct {
	Requested        string
	ActiveCategories []contract.Category
}

func (e *CategoryNotFoundError) Error() string {
	if e == nil {
		return ErrCategoryNotFound.Error()
	}
	return fmt.Sprintf("category %q not found", e.Requested)
}

func (e *CategoryNotFoundError) Is(target error) bool {
	return target == ErrCategoryNotFound
}

// CategoryInactiveError identifies an inactive category.
type CategoryInactiveError struct {
	Category         contract.Category
	ActiveCategories []contract.Category
}

func (e *CategoryInactiveError) Error() string {
	if e == nil {
		return ErrCategoryInactive.Error()
	}
	return fmt.Sprintf("category %q is inactive", e.Category.Name)
}

func (e *CategoryInactiveError) Is(target error) bool {
	return target == ErrCategoryInactive
}

type normalizedAllocation struct {
	category string
	amount   int64
}

type resolvedAllocation struct {
	categoryID int64
	amount     int64
}

const categoryColumns = `id, name, active, created_at, updated_at`

// CreateExplicit validates and atomically creates the current month's snapshot.
func (s *Store) CreateExplicit(ctx context.Context, month string, allocations []Allocation) (CreateResult, []contract.FieldIssue, error) {
	var now time.Time
	if s != nil && s.Now != nil {
		now = s.Now()
	} else {
		now = time.Now()
	}

	normalized, total, fields := validateRequest(month, allocations, now)
	if len(fields) != 0 {
		return CreateResult{}, fields, nil
	}
	if s == nil || s.DB == nil {
		return CreateResult{}, nil, errors.New("budget store database is nil")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return CreateResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	exists, err := budgetMonthExists(ctx, tx, month)
	if err != nil {
		return CreateResult{}, nil, err
	}
	if exists {
		return CreateResult{}, nil, &AlreadyExistsError{Month: month}
	}

	resolved := make([]resolvedAllocation, 0, len(normalized))
	for _, allocation := range normalized {
		category, found, err := lookupCategory(ctx, tx, allocation.category)
		if err != nil {
			return CreateResult{}, nil, err
		}
		if !found {
			activeCategories, err := listActiveCategories(ctx, tx)
			if err != nil {
				return CreateResult{}, nil, err
			}
			return CreateResult{}, nil, &CategoryNotFoundError{
				Requested:        allocation.category,
				ActiveCategories: activeCategories,
			}
		}
		if !category.Active {
			activeCategories, err := listActiveCategories(ctx, tx)
			if err != nil {
				return CreateResult{}, nil, err
			}
			return CreateResult{}, nil, &CategoryInactiveError{
				Category:         category,
				ActiveCategories: activeCategories,
			}
		}
		resolved = append(resolved, resolvedAllocation{
			categoryID: category.ID,
			amount:     allocation.amount,
		})
	}

	// All categories are resolved before writing.
	for _, allocation := range resolved {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO budgets (category_id, month, amount_hundredths)
			VALUES (?, ?, ?)
		`, allocation.categoryID, month, allocation.amount); err != nil {
			return CreateResult{}, nil, err
		}
	}

	budgets, err := listBudgetsForMonth(ctx, tx, month, len(resolved))
	if err != nil {
		return CreateResult{}, nil, err
	}
	totalBudget, err := contract.FormatAmount(total)
	if err != nil {
		return CreateResult{}, nil, err
	}

	if err := tx.Commit(); err != nil {
		return CreateResult{}, nil, err
	}
	return CreateResult{
		Month:       month,
		TotalBudget: totalBudget,
		Budgets:     budgets,
	}, nil, nil
}

func validateRequest(month string, allocations []Allocation, now time.Time) ([]normalizedAllocation, int64, []contract.FieldIssue) {
	fields := make([]contract.FieldIssue, 0)
	parsedMonth, monthErr := contract.ParseMonth(month)
	if monthErr != nil {
		fields = append(fields, contract.FieldIssue{
			Field:  "month",
			Reason: "must be a valid YYYY-MM month",
		})
	} else if parsedMonth != now.Format("2006-01") {
		fields = append(fields, contract.FieldIssue{
			Field:  "month",
			Reason: "must equal the current local month",
		})
	}

	if len(allocations) == 0 {
		fields = append(fields, contract.FieldIssue{
			Field:  "budgets",
			Reason: "must contain at least one allocation",
		})
		return nil, 0, fields
	}

	normalized := make([]normalizedAllocation, 0, len(allocations))
	seenCategories := make([]string, 0, len(allocations))
	allAmountsValid := true
	for index, allocation := range allocations {
		categoryName := contract.TrimASCIIWhitespace(allocation.Category)
		categoryValid := true
		switch {
		case categoryName == "":
			fields = append(fields, contract.FieldIssue{
				Field:  fmt.Sprintf("budgets[%d].category", index),
				Reason: "must not be empty",
			})
			categoryValid = false
		case strings.ContainsRune(categoryName, '\x00'):
			fields = append(fields, contract.FieldIssue{
				Field:  fmt.Sprintf("budgets[%d].category", index),
				Reason: "must not contain NUL characters",
			})
			categoryValid = false
		}

		if categoryValid {
			for _, previous := range seenCategories {
				if asciiNoCaseEqual(previous, categoryName) {
					fields = append(fields, contract.FieldIssue{
						Field:  fmt.Sprintf("budgets[%d].category", index),
						Reason: "must not repeat a category",
					})
					break
				}
			}
			seenCategories = append(seenCategories, categoryName)
		}

		amount, amountErr := contract.ParseAmount(allocation.Amount)
		if amountErr != nil {
			fields = append(fields, contract.FieldIssue{
				Field:  fmt.Sprintf("budgets[%d].amount", index),
				Reason: "must be a non-negative amount with at most two decimal places",
			})
			allAmountsValid = false
		}

		normalized = append(normalized, normalizedAllocation{
			category: categoryName,
			amount:   amount,
		})
	}

	var total int64
	if allAmountsValid {
		for _, allocation := range normalized {
			var ok bool
			total, ok = checkedAdd(total, allocation.amount)
			if !ok {
				fields = append(fields, contract.FieldIssue{
					Field:  "budgets",
					Reason: "total must fit the supported amount range",
				})
				break
			}
		}
	}

	return normalized, total, fields
}

func checkedAdd(left, right int64) (int64, bool) {
	if left < 0 || right < 0 || right > math.MaxInt64-left {
		return 0, false
	}
	return left + right, true
}

func asciiNoCaseEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := 0; index < len(left); index++ {
		leftByte, rightByte := left[index], right[index]
		if leftByte >= 'A' && leftByte <= 'Z' {
			leftByte += 'a' - 'A'
		}
		if rightByte >= 'A' && rightByte <= 'Z' {
			rightByte += 'a' - 'A'
		}
		if leftByte != rightByte {
			return false
		}
	}
	return true
}

func budgetMonthExists(ctx context.Context, tx *sql.Tx, month string) (bool, error) {
	var marker int64
	err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM budgets
		WHERE month = ?
		LIMIT 1
	`, month).Scan(&marker)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, err
	default:
		return true, nil
	}
}

func lookupCategory(ctx context.Context, tx *sql.Tx, name string) (contract.Category, bool, error) {
	category, err := scanCategory(tx.QueryRowContext(ctx, `
		SELECT `+categoryColumns+`
		FROM categories
		WHERE name = ? COLLATE NOCASE
	`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return contract.Category{}, false, nil
	}
	if err != nil {
		return contract.Category{}, false, err
	}
	return category, true, nil
}

func listActiveCategories(ctx context.Context, tx *sql.Tx) ([]contract.Category, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT `+categoryColumns+`
		FROM categories
		WHERE active = 1
		ORDER BY name COLLATE NOCASE ASC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	categories := make([]contract.Category, 0)
	for rows.Next() {
		category, err := scanCategory(rows)
		if err != nil {
			return nil, err
		}
		categories = append(categories, category)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return categories, nil
}

func listBudgetsForMonth(ctx context.Context, tx *sql.Tx, month string, expected int) ([]contract.Budget, error) {
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
		WHERE b.month = ?
		ORDER BY c.name COLLATE NOCASE ASC, b.id ASC
	`, month)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	budgets := make([]contract.Budget, 0, expected)
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
	if len(budgets) != expected {
		return nil, fmt.Errorf("budget snapshot returned %d rows, expected %d", len(budgets), expected)
	}
	return budgets, nil
}

func scanCategory(row interface{ Scan(dest ...any) error }) (contract.Category, error) {
	var category contract.Category
	err := row.Scan(
		&category.ID,
		&category.Name,
		&category.Active,
		&category.CreatedAt,
		&category.UpdatedAt,
	)
	return category, err
}
