package summary

import (
	"context"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
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

	amounts, _, err := loadMonthBudgets(ctx, tx, month)
	if err != nil {
		return contract.CategorySummary{}, nil, err
	}
	row := amounts[category.ID]
	var baseBudget, adjustment, budget, fundOpening int64
	var fund bool
	if row != nil {
		baseBudget = row.baseBudget
		adjustment = row.rolloverAdjustment
		budget = row.budget
		fund = row.sinkingFund
		fundOpening = row.sinkingFundOpening
	}
	spending, count, err := loadCategorySpending(ctx, tx, category.ID, startDate, endDate)
	if err != nil {
		return contract.CategorySummary{}, nil, err
	}

	formattedBaseBudget, err := contract.FormatAmount(baseBudget)
	if err != nil {
		return contract.CategorySummary{}, nil, err
	}
	formattedAdjustment, err := contract.FormatSignedAmount(adjustment)
	if err != nil {
		return contract.CategorySummary{}, nil, err
	}
	formattedBudget, err := contract.FormatSignedAmount(budget)
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
	spent, err := SpentOfBudget(spending, budget)
	if err != nil {
		return contract.CategorySummary{}, nil, err
	}

	if err := tx.Commit(); err != nil {
		return contract.CategorySummary{}, nil, err
	}
	return contract.CategorySummary{
		CategoryID:         category.ID,
		Category:           category.Name,
		Month:              month,
		BaseBudget:         formattedBaseBudget,
		SinkingFund:        fund,
		SinkingFundOpening: formatSignedOrZero(fundOpening),
		RolloverAdjustment: formattedAdjustment,
		Budget:             formattedBudget,
		TotalSpending:      formattedSpending,
		Remaining:          remaining,
		SpentOfBudget:      spent,
		TransactionCount:   count,
	}, nil, nil
}
