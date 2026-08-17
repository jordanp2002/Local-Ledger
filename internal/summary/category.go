package summary

import (
	"context"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

func (s *Store) category(ctx context.Context, categoryName, month string) (contract.CategorySummary, []contract.FieldIssue, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return contract.CategorySummary{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	exists, err := budgetMonthExists(ctx, tx, month)
	if err != nil {
		return contract.CategorySummary{}, nil, err
	}
	if !exists {
		return contract.CategorySummary{}, nil, missingMonthError(ctx, tx, month)
	}

	category, found, err := lookupCategory(ctx, tx, categoryName)
	if err != nil {
		return contract.CategorySummary{}, nil, err
	}
	if !found {
		activeCategories, err := listActiveCategories(ctx, tx)
		if err != nil {
			return contract.CategorySummary{}, nil, err
		}
		return contract.CategorySummary{}, nil, &CategoryNotFoundError{
			Requested:        categoryName,
			ActiveCategories: activeCategories,
		}
	}

	startDate, endDate, err := contract.MonthDateRange(month)
	if err != nil {
		return contract.CategorySummary{}, nil, err
	}

	budget, err := loadCategoryBudget(ctx, tx, month, category.ID)
	if err != nil {
		return contract.CategorySummary{}, nil, err
	}
	spending, count, err := loadCategorySpending(ctx, tx, category.ID, startDate, endDate)
	if err != nil {
		return contract.CategorySummary{}, nil, err
	}

	formattedBudget, err := contract.FormatAmount(budget)
	if err != nil {
		return contract.CategorySummary{}, nil, err
	}
	formattedSpending, err := contract.FormatAmount(spending)
	if err != nil {
		return contract.CategorySummary{}, nil, err
	}
	remaining, err := formatRemaining(budget, spending)
	if err != nil {
		return contract.CategorySummary{}, nil, err
	}

	if err := tx.Commit(); err != nil {
		return contract.CategorySummary{}, nil, err
	}
	return contract.CategorySummary{
		CategoryID:       category.ID,
		Category:         category.Name,
		Month:            month,
		Budget:           formattedBudget,
		TotalSpending:    formattedSpending,
		Remaining:        remaining,
		TransactionCount: count,
	}, nil, nil
}
