package summary

import (
	"context"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
)

func (s *Store) monthly(ctx context.Context, month string) (MonthlyResult, []contract.FieldIssue, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return MonthlyResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	exists, err := budgetMonthExists(ctx, tx, month)
	if err != nil {
		return MonthlyResult{}, nil, err
	}
	if !exists {
		return MonthlyResult{}, nil, missingMonthError(ctx, tx, month)
	}

	startDate, endDate, err := contract.MonthDateRange(month)
	if err != nil {
		return MonthlyResult{}, nil, err
	}

	amounts, totals, err := loadMonthBudgets(ctx, tx, month)
	if err != nil {
		return MonthlyResult{}, nil, err
	}
	totalSpending, err := addMonthSpending(ctx, tx, startDate, endDate, amounts)
	if err != nil {
		return MonthlyResult{}, nil, err
	}

	kept := make([]int64, 0, len(amounts))
	for id, row := range amounts {
		if row.budget != 0 || row.baseBudget != 0 || row.rolloverAdjustment != 0 || row.spending > 0 {
			kept = append(kept, id)
		}
	}
	ordered, err := orderCategoryIDs(ctx, tx, kept)
	if err != nil {
		return MonthlyResult{}, nil, err
	}

	categories := make([]contract.MonthlySummaryCategory, 0, len(ordered))
	for _, id := range ordered {
		row := amounts[id]
		baseBudget, err := contract.FormatAmount(row.baseBudget)
		if err != nil {
			return MonthlyResult{}, nil, err
		}
		adjustment, err := contract.FormatSignedAmount(row.rolloverAdjustment)
		if err != nil {
			return MonthlyResult{}, nil, err
		}
		budget, err := contract.FormatSignedAmount(row.budget)
		if err != nil {
			return MonthlyResult{}, nil, err
		}
		spending, err := contract.FormatAmount(row.spending)
		if err != nil {
			return MonthlyResult{}, nil, err
		}
		remaining, err := formatRemaining(row.budget, row.spending)
		if err != nil {
			return MonthlyResult{}, nil, err
		}
		spent, err := SpentOfBudget(row.spending, row.budget)
		if err != nil {
			return MonthlyResult{}, nil, err
		}
		shareOfBaseBudget, err := compositionShare(row.baseBudget, totals.baseBudget)
		if err != nil {
			return MonthlyResult{}, nil, err
		}
		shareOfSpending, err := compositionShare(row.spending, totalSpending)
		if err != nil {
			return MonthlyResult{}, nil, err
		}
		categories = append(categories, contract.MonthlySummaryCategory{
			CategoryID:         id,
			Category:           row.name,
			BaseBudget:         baseBudget,
			SinkingFund:        row.sinkingFund,
			SinkingFundOpening: formatSignedOrZero(row.sinkingFundOpening),
			RolloverAdjustment: adjustment,
			Budget:             budget,
			Spending:           spending,
			Remaining:          remaining,
			SpentOfBudget:      spent,
			ShareOfBaseBudget:  shareOfBaseBudget,
			ShareOfSpending:    shareOfSpending,
		})
	}

	formattedBaseBudget, err := contract.FormatAmount(totals.baseBudget)
	if err != nil {
		return MonthlyResult{}, nil, err
	}
	formattedAdjustment, err := contract.FormatSignedAmount(totals.rolloverAdjustment)
	if err != nil {
		return MonthlyResult{}, nil, err
	}
	formattedFundOpening, err := contract.FormatSignedAmount(totals.sinkingFundOpening)
	if err != nil {
		return MonthlyResult{}, nil, err
	}
	formattedBudget, err := contract.FormatSignedAmount(totals.totalBudget)
	if err != nil {
		return MonthlyResult{}, nil, err
	}
	formattedSpending, err := contract.FormatAmount(totalSpending)
	if err != nil {
		return MonthlyResult{}, nil, err
	}
	formattedRemaining, err := formatRemaining(totals.totalBudget, totalSpending)
	if err != nil {
		return MonthlyResult{}, nil, err
	}
	spentOfBudget, err := SpentOfBudget(totalSpending, totals.totalBudget)
	if err != nil {
		return MonthlyResult{}, nil, err
	}

	if err := tx.Commit(); err != nil {
		return MonthlyResult{}, nil, err
	}
	return MonthlyResult{
		Month:                   month,
		TotalBaseBudget:         formattedBaseBudget,
		TotalSinkingFundOpening: formattedFundOpening,
		TotalRolloverAdjustment: formattedAdjustment,
		TotalBudget:             formattedBudget,
		TotalSpending:           formattedSpending,
		Remaining:               formattedRemaining,
		SpentOfBudget:           spentOfBudget,
		Categories:              categories,
	}, nil, nil
}

func formatSignedOrZero(value int64) string {
	formatted, err := contract.FormatSignedAmount(value)
	if err != nil {
		return "0.00"
	}
	return formatted
}
