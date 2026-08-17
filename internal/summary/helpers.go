package summary

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

const categoryColumns = `id, name, active, created_at, updated_at`

type categoryAmounts struct {
	name     string
	budget   int64
	spending int64
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

func lookupLatestEarlierMonth(ctx context.Context, tx *sql.Tx, month string) (string, bool, error) {
	var source string
	err := tx.QueryRowContext(ctx, `
		SELECT month
		FROM budgets
		WHERE month < ?
		ORDER BY month DESC
		LIMIT 1
	`, month).Scan(&source)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, err
	default:
		return source, true, nil
	}
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

func loadMonthBudgets(ctx context.Context, tx *sql.Tx, month string) (map[int64]*categoryAmounts, int64, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT b.category_id, c.name, b.amount_hundredths
		FROM budgets AS b
		INNER JOIN categories AS c ON c.id = b.category_id
		WHERE b.month = ?
	`, month)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	amounts := make(map[int64]*categoryAmounts)
	var total int64
	for rows.Next() {
		var id int64
		var name string
		var amount int64
		if err := rows.Scan(&id, &name, &amount); err != nil {
			return nil, 0, err
		}
		next, ok := checkedAdd(total, amount)
		if !ok {
			return nil, 0, fmtOverflow("budget")
		}
		total = next
		amounts[id] = &categoryAmounts{name: name, budget: amount}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}
	return amounts, total, nil
}

func addMonthSpending(ctx context.Context, tx *sql.Tx, startDate, endDate string, amounts map[int64]*categoryAmounts) (int64, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT t.category_id, c.name, t.amount_hundredths
		FROM transactions AS t
		INNER JOIN categories AS c ON c.id = t.category_id
		WHERE t.date >= ? AND t.date <= ?
	`, startDate, endDate)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()

	var total int64
	for rows.Next() {
		var id int64
		var name string
		var amount int64
		if err := rows.Scan(&id, &name, &amount); err != nil {
			return 0, err
		}
		next, ok := checkedAdd(total, amount)
		if !ok {
			return 0, fmtOverflow("spending")
		}
		total = next
		row, exists := amounts[id]
		if !exists {
			row = &categoryAmounts{name: name}
			amounts[id] = row
		}
		nextSpending, ok := checkedAdd(row.spending, amount)
		if !ok {
			return 0, fmtOverflow("spending")
		}
		row.spending = nextSpending
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	return total, nil
}

func orderCategoryIDs(ctx context.Context, tx *sql.Tx, ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return []int64{}, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id
		FROM categories
		WHERE id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY name COLLATE NOCASE ASC, id ASC
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	ordered := make([]int64, 0, len(ids))
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ordered = append(ordered, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return ordered, nil
}

func loadCategoryBudget(ctx context.Context, tx *sql.Tx, month string, categoryID int64) (int64, error) {
	var amount int64
	err := tx.QueryRowContext(ctx, `
		SELECT amount_hundredths
		FROM budgets
		WHERE month = ? AND category_id = ?
	`, month, categoryID).Scan(&amount)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, err
	default:
		return amount, nil
	}
}

func loadCategorySpending(ctx context.Context, tx *sql.Tx, categoryID int64, startDate, endDate string) (int64, int64, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT amount_hundredths
		FROM transactions
		WHERE category_id = ? AND date >= ? AND date <= ?
	`, categoryID, startDate, endDate)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = rows.Close() }()

	var total int64
	var count int64
	for rows.Next() {
		var amount int64
		if err := rows.Scan(&amount); err != nil {
			return 0, 0, err
		}
		next, ok := checkedAdd(total, amount)
		if !ok {
			return 0, 0, fmtOverflow("spending")
		}
		total = next
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, 0, err
	}
	return total, count, nil
}

func fmtOverflow(kind string) error {
	return fmt.Errorf("summary %s total overflow", kind)
}
