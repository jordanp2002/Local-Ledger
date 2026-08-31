package category

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/jordanp2002/local-finance-mcp/internal/rollover"
	"github.com/jordanp2002/local-finance-mcp/internal/sinkingfund"
)

func (s *Store) Disable(ctx context.Context, name string) (contract.Category, bool, *contract.Budget, error) {
	normalized, err := validateName(name)
	if err != nil {
		return contract.Category{}, false, nil, err
	}

	existing, found, err := lookupCategory(ctx, s.DB, normalized)
	if err != nil {
		return contract.Category{}, false, nil, err
	}
	if !found {
		return contract.Category{}, false, nil, ErrNotFound
	}
	if !existing.Active {
		return existing, false, nil, nil
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return contract.Category{}, false, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	current, err := getCategoryByID(ctx, tx, existing.ID)
	if err != nil {
		return contract.Category{}, false, nil, err
	}
	if !current.Active {
		return current, false, nil, nil
	}

	month := LocalMonth(s.now())
	if open, err := sinkingfund.HasOpenPeriod(ctx, tx, current.ID); err != nil {
		return contract.Category{}, false, nil, err
	} else if open {
		return contract.Category{}, false, nil, sinkingfund.ErrActive
	}
	removed, err := loadCurrentMonthBudget(ctx, tx, current.ID, month)
	if err != nil {
		return contract.Category{}, false, nil, err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE categories
		SET active = 0,
		    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE id = ?
	`, current.ID); err != nil {
		return contract.Category{}, false, nil, err
	}

	if removed != nil {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM budgets
			WHERE category_id = ? AND month = ?
		`, current.ID, month); err != nil {
			return contract.Category{}, false, nil, err
		}
	}

	disabled, err := getCategoryByID(ctx, tx, current.ID)
	if err != nil {
		return contract.Category{}, false, nil, err
	}
	if err := rollover.ValidateOutgoing(ctx, tx); err != nil {
		return contract.Category{}, false, nil, err
	}
	if err := tx.Commit(); err != nil {
		return contract.Category{}, false, nil, err
	}
	return disabled, true, removed, nil
}

func loadCurrentMonthBudget(ctx context.Context, q queryer, categoryID int64, month string) (*contract.Budget, error) {
	var budget contract.Budget
	var hundredths int64
	err := q.QueryRowContext(ctx, `
		SELECT b.id, b.month, b.category_id, c.name, b.amount_hundredths, b.created_at, b.updated_at
		FROM budgets AS b
		INNER JOIN categories AS c ON c.id = b.category_id
		WHERE b.category_id = ? AND b.month = ?
	`, categoryID, month).Scan(
		&budget.ID,
		&budget.Month,
		&budget.CategoryID,
		&budget.Category,
		&hundredths,
		&budget.CreatedAt,
		&budget.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	amount, err := contract.FormatAmount(hundredths)
	if err != nil {
		return nil, err
	}
	budget.Amount = amount
	return &budget, nil
}
