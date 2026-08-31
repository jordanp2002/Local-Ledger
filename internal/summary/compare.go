package summary

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

// Compare compares two stored monthly budget snapshots and their spending.
// All reads and calculations use one SQLite read transaction so both sides
// describe the same database snapshot.
func (s *Store) Compare(ctx context.Context, fromMonth, toMonth string) (ComparisonResult, []contract.FieldIssue, error) {
	fields := make([]contract.FieldIssue, 0, 2)
	from, fromIssue := validateMonthField(fromMonth, "from_month")
	if fromIssue != nil {
		fields = append(fields, *fromIssue)
	}
	to, toIssue := validateMonthField(toMonth, "to_month")
	if toIssue != nil {
		fields = append(fields, *toIssue)
	}
	if fromIssue == nil && toIssue == nil && to <= from {
		fields = append(fields, contract.FieldIssue{
			Field:  "to_month",
			Reason: "must be later than from_month",
		})
	}
	if len(fields) != 0 {
		return ComparisonResult{}, fields, nil
	}
	if s == nil || s.DB == nil {
		return ComparisonResult{}, nil, errors.New("summary store database is nil")
	}
	return s.compare(ctx, from, to)
}

type comparisonMonth struct {
	month                   string
	amounts                 map[int64]*categoryAmounts
	totalBaseBudget         int64
	totalRolloverAdjustment int64
	totalBudget             int64
	totalSpending           int64
}

func (s *Store) compare(ctx context.Context, fromMonth, toMonth string) (ComparisonResult, []contract.FieldIssue, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ComparisonResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	from, err := loadComparisonMonth(ctx, tx, fromMonth)
	if err != nil {
		return ComparisonResult{}, nil, err
	}
	to, err := loadComparisonMonth(ctx, tx, toMonth)
	if err != nil {
		return ComparisonResult{}, nil, err
	}

	fromRemaining, ok := checkedSubtract(from.totalBudget, from.totalSpending)
	if !ok {
		return ComparisonResult{}, nil, fmtOverflow("remaining")
	}
	toRemaining, ok := checkedSubtract(to.totalBudget, to.totalSpending)
	if !ok {
		return ComparisonResult{}, nil, fmtOverflow("remaining")
	}

	baseBudgetChange, ok := checkedSubtract(to.totalBaseBudget, from.totalBaseBudget)
	if !ok {
		return ComparisonResult{}, nil, fmtOverflow("base budget change")
	}
	rolloverAdjustmentChange, ok := checkedSubtract(to.totalRolloverAdjustment, from.totalRolloverAdjustment)
	if !ok {
		return ComparisonResult{}, nil, fmtOverflow("rollover adjustment change")
	}
	budgetChange, ok := checkedSubtract(to.totalBudget, from.totalBudget)
	if !ok {
		return ComparisonResult{}, nil, fmtOverflow("budget change")
	}
	spendingChange, ok := checkedSubtract(to.totalSpending, from.totalSpending)
	if !ok {
		return ComparisonResult{}, nil, fmtOverflow("spending change")
	}
	remainingChange, ok := checkedSubtract(toRemaining, fromRemaining)
	if !ok {
		return ComparisonResult{}, nil, fmtOverflow("remaining change")
	}

	categories, err := comparisonCategories(ctx, tx, from.amounts, to.amounts)
	if err != nil {
		return ComparisonResult{}, nil, err
	}

	fromResult, err := formatComparisonMonth(from, fromRemaining)
	if err != nil {
		return ComparisonResult{}, nil, err
	}
	toResult, err := formatComparisonMonth(to, toRemaining)
	if err != nil {
		return ComparisonResult{}, nil, err
	}
	change, err := formatComparisonChange(baseBudgetChange, rolloverAdjustmentChange, budgetChange, spendingChange, remainingChange)
	if err != nil {
		return ComparisonResult{}, nil, err
	}

	if err := tx.Commit(); err != nil {
		return ComparisonResult{}, nil, err
	}
	return ComparisonResult{
		From:       fromResult,
		To:         toResult,
		Change:     change,
		Categories: categories,
	}, nil, nil
}

func loadComparisonMonth(ctx context.Context, tx *sql.Tx, month string) (comparisonMonth, error) {
	exists, err := budgetMonthExists(ctx, tx, month)
	if err != nil {
		return comparisonMonth{}, err
	}
	if !exists {
		return comparisonMonth{}, missingMonthError(ctx, tx, month)
	}

	startDate, endDate, err := contract.MonthDateRange(month)
	if err != nil {
		return comparisonMonth{}, err
	}
	amounts, totals, err := loadMonthBudgets(ctx, tx, month)
	if err != nil {
		return comparisonMonth{}, err
	}
	totalSpending, err := addMonthSpending(ctx, tx, startDate, endDate, amounts)
	if err != nil {
		return comparisonMonth{}, err
	}
	return comparisonMonth{
		month:                   month,
		amounts:                 amounts,
		totalBaseBudget:         totals.baseBudget,
		totalRolloverAdjustment: totals.rolloverAdjustment,
		totalBudget:             totals.totalBudget,
		totalSpending:           totalSpending,
	}, nil
}

func comparisonCategories(ctx context.Context, tx *sql.Tx, from, to map[int64]*categoryAmounts) ([]contract.ComparisonCategory, error) {
	ids := make(map[int64]struct{}, len(from)+len(to))
	for id, row := range from {
		if row.baseBudget != 0 || row.rolloverAdjustment != 0 || row.budget != 0 || row.spending != 0 {
			ids[id] = struct{}{}
		}
	}
	for id, row := range to {
		if row.baseBudget != 0 || row.rolloverAdjustment != 0 || row.budget != 0 || row.spending != 0 {
			ids[id] = struct{}{}
		}
	}

	allIDs := make([]int64, 0, len(ids))
	for id := range ids {
		allIDs = append(allIDs, id)
	}
	ordered, err := orderCategoryIDs(ctx, tx, allIDs)
	if err != nil {
		return nil, err
	}
	categories := make([]contract.ComparisonCategory, 0, len(ordered))
	for _, id := range ordered {
		fromBase, fromAdjustment, fromBudget, fromSpending := categoryAmountsFor(from, id)
		toBase, toAdjustment, toBudget, toSpending := categoryAmountsFor(to, id)

		baseBudgetChange, ok := checkedSubtract(toBase, fromBase)
		if !ok {
			return nil, fmtOverflow("category base budget change")
		}
		rolloverAdjustmentChange, ok := checkedSubtract(toAdjustment, fromAdjustment)
		if !ok {
			return nil, fmtOverflow("category rollover adjustment change")
		}
		budgetChange, ok := checkedSubtract(toBudget, fromBudget)
		if !ok {
			return nil, fmtOverflow("category budget change")
		}
		spendingChange, ok := checkedSubtract(toSpending, fromSpending)
		if !ok {
			return nil, fmtOverflow("category spending change")
		}
		name, err := categoryName(ctx, tx, id)
		if err != nil {
			return nil, err
		}

		row, err := formatComparisonCategory(id, name,
			fromBase, toBase, baseBudgetChange,
			fromAdjustment, toAdjustment, rolloverAdjustmentChange,
			fromBudget, toBudget, budgetChange,
			fromSpending, toSpending, spendingChange,
		)
		if err != nil {
			return nil, err
		}
		categories = append(categories, row)
	}
	return categories, nil
}

func categoryAmountsFor(amounts map[int64]*categoryAmounts, id int64) (int64, int64, int64, int64) {
	row, ok := amounts[id]
	if !ok {
		return 0, 0, 0, 0
	}
	return row.baseBudget, row.rolloverAdjustment, row.budget, row.spending
}

func categoryName(ctx context.Context, tx *sql.Tx, id int64) (string, error) {
	var name string
	if err := tx.QueryRowContext(ctx, `SELECT name FROM categories WHERE id = ?`, id).Scan(&name); err != nil {
		return "", err
	}
	return name, nil
}

func formatComparisonMonth(month comparisonMonth, remaining int64) (contract.ComparisonMonth, error) {
	totalBaseBudget, err := contract.FormatAmount(month.totalBaseBudget)
	if err != nil {
		return contract.ComparisonMonth{}, err
	}
	totalRolloverAdjustment, err := contract.FormatSignedAmount(month.totalRolloverAdjustment)
	if err != nil {
		return contract.ComparisonMonth{}, err
	}
	totalBudget, err := contract.FormatSignedAmount(month.totalBudget)
	if err != nil {
		return contract.ComparisonMonth{}, err
	}
	totalSpending, err := contract.FormatAmount(month.totalSpending)
	if err != nil {
		return contract.ComparisonMonth{}, err
	}
	formattedRemaining, err := contract.FormatSignedAmount(remaining)
	if err != nil {
		return contract.ComparisonMonth{}, err
	}
	return contract.ComparisonMonth{
		Month:                   month.month,
		TotalBaseBudget:         totalBaseBudget,
		TotalRolloverAdjustment: totalRolloverAdjustment,
		TotalBudget:             totalBudget,
		TotalSpending:           totalSpending,
		Remaining:               formattedRemaining,
	}, nil
}

func formatComparisonChange(baseBudget, rolloverAdjustment, budget, spending, remaining int64) (contract.ComparisonChange, error) {
	totalBaseBudget, err := contract.FormatSignedAmount(baseBudget)
	if err != nil {
		return contract.ComparisonChange{}, err
	}
	totalRolloverAdjustment, err := contract.FormatSignedAmount(rolloverAdjustment)
	if err != nil {
		return contract.ComparisonChange{}, err
	}
	totalBudget, err := contract.FormatSignedAmount(budget)
	if err != nil {
		return contract.ComparisonChange{}, err
	}
	totalSpending, err := contract.FormatSignedAmount(spending)
	if err != nil {
		return contract.ComparisonChange{}, err
	}
	formattedRemaining, err := contract.FormatSignedAmount(remaining)
	if err != nil {
		return contract.ComparisonChange{}, err
	}
	return contract.ComparisonChange{
		TotalBaseBudget:         totalBaseBudget,
		TotalRolloverAdjustment: totalRolloverAdjustment,
		TotalBudget:             totalBudget,
		TotalSpending:           totalSpending,
		Remaining:               formattedRemaining,
	}, nil
}

func formatComparisonCategory(
	id int64,
	name string,
	fromBaseBudget, toBaseBudget, baseBudgetChange int64,
	fromRolloverAdjustment, toRolloverAdjustment, rolloverAdjustmentChange int64,
	fromBudget, toBudget, budgetChange int64,
	fromSpending, toSpending, spendingChange int64,
) (contract.ComparisonCategory, error) {
	formattedFromBaseBudget, err := contract.FormatAmount(fromBaseBudget)
	if err != nil {
		return contract.ComparisonCategory{}, err
	}
	formattedToBaseBudget, err := contract.FormatAmount(toBaseBudget)
	if err != nil {
		return contract.ComparisonCategory{}, err
	}
	formattedBaseBudgetChange, err := contract.FormatSignedAmount(baseBudgetChange)
	if err != nil {
		return contract.ComparisonCategory{}, err
	}
	formattedFromRolloverAdjustment, err := contract.FormatSignedAmount(fromRolloverAdjustment)
	if err != nil {
		return contract.ComparisonCategory{}, err
	}
	formattedToRolloverAdjustment, err := contract.FormatSignedAmount(toRolloverAdjustment)
	if err != nil {
		return contract.ComparisonCategory{}, err
	}
	formattedRolloverAdjustmentChange, err := contract.FormatSignedAmount(rolloverAdjustmentChange)
	if err != nil {
		return contract.ComparisonCategory{}, err
	}
	formattedFromBudget, err := contract.FormatSignedAmount(fromBudget)
	if err != nil {
		return contract.ComparisonCategory{}, err
	}
	formattedToBudget, err := contract.FormatSignedAmount(toBudget)
	if err != nil {
		return contract.ComparisonCategory{}, err
	}
	formattedBudgetChange, err := contract.FormatSignedAmount(budgetChange)
	if err != nil {
		return contract.ComparisonCategory{}, err
	}
	formattedFromSpending, err := contract.FormatAmount(fromSpending)
	if err != nil {
		return contract.ComparisonCategory{}, err
	}
	formattedToSpending, err := contract.FormatAmount(toSpending)
	if err != nil {
		return contract.ComparisonCategory{}, err
	}
	formattedSpendingChange, err := contract.FormatSignedAmount(spendingChange)
	if err != nil {
		return contract.ComparisonCategory{}, err
	}
	return contract.ComparisonCategory{
		CategoryID:               id,
		Category:                 name,
		FromBaseBudget:           formattedFromBaseBudget,
		ToBaseBudget:             formattedToBaseBudget,
		BaseBudgetChange:         formattedBaseBudgetChange,
		FromRolloverAdjustment:   formattedFromRolloverAdjustment,
		ToRolloverAdjustment:     formattedToRolloverAdjustment,
		RolloverAdjustmentChange: formattedRolloverAdjustmentChange,
		FromBudget:               formattedFromBudget,
		ToBudget:                 formattedToBudget,
		BudgetChange:             formattedBudgetChange,
		FromSpending:             formattedFromSpending,
		ToSpending:               formattedToSpending,
		SpendingChange:           formattedSpendingChange,
	}, nil
}
