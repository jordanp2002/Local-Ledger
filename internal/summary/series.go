package summary

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
)

// SeriesInput identifies an inclusive range of canonical months and an
// optional category filter. IncludeCategories requests the rectangular
// category-by-month view.
type SeriesInput struct {
	FromMonth         string
	ToMonth           string
	Category          *string
	IncludeCategories bool
}

type SeriesResult struct {
	FromMonth         string
	ToMonth           string
	Category          *string
	IncludeCategories bool
	Months            []SeriesMonth
}

type SeriesMonth struct {
	Month                   string
	TotalBaseBudget         *string
	TotalSinkingFundOpening *string
	TotalRolloverAdjustment *string
	TotalBudget             *string
	TotalSpending           string
	Remaining               *string
	SpentOfBudget           *string
	TransactionCount        int64
	Categories              []contract.MonthlySeriesCategory
}

type validatedSeries struct {
	fromMonth         string
	toMonth           string
	category          *string
	includeCategories bool
}

type seriesFundPeriod struct {
	id         int64
	categoryID int64
	name       string
	startMonth string
	endMonth   *string
}

type seriesFundValue struct {
	opening      int64
	contribution int64
	available    int64
}

// seriesRangeData contains all range reads and their in-memory aggregates.
// Historical rows before from_month are retained only as needed to derive
// sinking-fund opening balances for periods already in progress.
type seriesRangeData struct {
	fromMonth string
	toMonth   string

	allMonths []string
	months    []string

	budgetsByMonth    map[string]map[int64]int64
	spendingByMonth   map[string]map[int64]int64
	categoryCounts    map[string]map[int64]int64
	totalSpending     map[string]int64
	totalTransactions map[string]int64
	rolloversByMonth  map[string]map[int64]int64
	snapshots         map[string]bool
	fundsByMonth      map[string]map[int64]seriesFundValue
	categoryNames     map[int64]string
	axisIDs           []int64
}

// Series returns one row for every calendar month in an inclusive range.
// Spending is reported even when a month has no budget snapshot.
func (s *Store) Series(ctx context.Context, in SeriesInput) (SeriesResult, []contract.FieldIssue, error) {
	validated, fields := validateSeries(in)
	if len(fields) != 0 {
		return SeriesResult{}, fields, nil
	}
	if s == nil || s.DB == nil {
		return SeriesResult{}, nil, errors.New("summary store database is nil")
	}
	return s.series(ctx, validated)
}

func validateSeries(in SeriesInput) (validatedSeries, []contract.FieldIssue) {
	fields := make([]contract.FieldIssue, 0, 4)
	validated := validatedSeries{includeCategories: in.IncludeCategories}

	from, fromIssue := validateMonthField(in.FromMonth, "from_month")
	if fromIssue != nil {
		fields = append(fields, *fromIssue)
	} else {
		validated.fromMonth = from
	}

	to, toIssue := validateMonthField(in.ToMonth, "to_month")
	if toIssue != nil {
		fields = append(fields, *toIssue)
	} else {
		validated.toMonth = to
	}

	if fromIssue == nil && toIssue == nil {
		switch {
		case to < from:
			fields = append(fields, contract.FieldIssue{
				Field:  "to_month",
				Reason: "must be on or after from_month",
			})
		case seriesMonthCount(from, to) > 24:
			fields = append(fields, contract.FieldIssue{
				Field:  "to_month",
				Reason: "must be at most 24 months after from_month",
			})
		}
	}

	if in.Category != nil {
		category, issue := validateCategoryName(*in.Category)
		if issue != nil {
			fields = append(fields, *issue)
		} else {
			validated.category = &category
			if in.IncludeCategories {
				fields = append(fields, contract.FieldIssue{
					Field:  "category",
					Reason: "cannot be combined with include_categories",
				})
			}
		}
	}

	return validated, fields
}

func seriesMonthCount(from, to string) int64 {
	fromYear, _ := strconv.Atoi(from[:4])
	fromMonth, _ := strconv.Atoi(from[5:])
	toYear, _ := strconv.Atoi(to[:4])
	toMonth, _ := strconv.Atoi(to[5:])
	return int64(toYear-fromYear)*12 + int64(toMonth-fromMonth) + 1
}

func (s *Store) series(ctx context.Context, in validatedSeries) (SeriesResult, []contract.FieldIssue, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return SeriesResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var categoryID *int64
	var categoryName *string
	if in.category != nil {
		category, found, err := lookupCategory(ctx, tx, *in.category)
		if err != nil {
			return SeriesResult{}, nil, err
		}
		if !found {
			activeCategories, err := listActiveCategories(ctx, tx)
			if err != nil {
				return SeriesResult{}, nil, err
			}
			return SeriesResult{}, nil, &CategoryNotFoundError{
				Requested:        *in.category,
				ActiveCategories: activeCategories,
			}
		}
		categoryID = &category.ID
		name := category.Name
		categoryName = &name
	}

	periods, minDataMonth, err := loadSeriesFundPeriods(ctx, tx, in.fromMonth, in.toMonth)
	if err != nil {
		return SeriesResult{}, nil, err
	}
	data, err := loadSeriesRangeData(ctx, tx, in.fromMonth, in.toMonth, minDataMonth, periods)
	if err != nil {
		return SeriesResult{}, nil, err
	}
	if categoryID != nil {
		data.categoryNames[*categoryID] = *categoryName
	}

	months := make([]SeriesMonth, 0, len(data.months))
	for _, month := range data.months {
		row, err := formatSeriesMonth(data, month, categoryID, in.includeCategories)
		if err != nil {
			return SeriesResult{}, nil, err
		}
		months = append(months, row)
	}

	if err := tx.Commit(); err != nil {
		return SeriesResult{}, nil, err
	}
	return SeriesResult{
		FromMonth:         in.fromMonth,
		ToMonth:           in.toMonth,
		Category:          categoryName,
		IncludeCategories: in.includeCategories,
		Months:            months,
	}, nil, nil
}

func loadSeriesFundPeriods(ctx context.Context, tx *sql.Tx, fromMonth, toMonth string) ([]seriesFundPeriod, string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT p.id, p.category_id, c.name, p.start_month, p.end_month
		FROM sinking_fund_periods AS p
		INNER JOIN categories AS c ON c.id = p.category_id
		WHERE p.start_month <= ?
		  AND (p.end_month IS NULL OR p.end_month >= ?)
		ORDER BY p.start_month ASC, c.name COLLATE NOCASE ASC, p.category_id ASC, p.id ASC
	`, toMonth, fromMonth)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rows.Close() }()

	periods := make([]seriesFundPeriod, 0)
	minDataMonth := fromMonth
	for rows.Next() {
		var period seriesFundPeriod
		var end sql.NullString
		if err := rows.Scan(&period.id, &period.categoryID, &period.name, &period.startMonth, &end); err != nil {
			return nil, "", err
		}
		if end.Valid {
			period.endMonth = &end.String
		}
		if period.startMonth < minDataMonth {
			minDataMonth = period.startMonth
		}
		periods = append(periods, period)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if err := rows.Close(); err != nil {
		return nil, "", err
	}
	return periods, minDataMonth, nil
}

func loadSeriesRangeData(ctx context.Context, tx *sql.Tx, fromMonth, toMonth, minDataMonth string, periods []seriesFundPeriod) (*seriesRangeData, error) {
	data := &seriesRangeData{
		fromMonth:         fromMonth,
		toMonth:           toMonth,
		allMonths:         seriesMonths(minDataMonth, toMonth),
		months:            seriesMonths(fromMonth, toMonth),
		budgetsByMonth:    make(map[string]map[int64]int64),
		spendingByMonth:   make(map[string]map[int64]int64),
		categoryCounts:    make(map[string]map[int64]int64),
		totalSpending:     make(map[string]int64),
		totalTransactions: make(map[string]int64),
		rolloversByMonth:  make(map[string]map[int64]int64),
		snapshots:         make(map[string]bool),
		fundsByMonth:      make(map[string]map[int64]seriesFundValue),
		categoryNames:     make(map[int64]string),
	}

	if err := loadSeriesBudgets(ctx, tx, data, minDataMonth, toMonth); err != nil {
		return nil, err
	}
	if err := loadSeriesRollovers(ctx, tx, data, fromMonth, toMonth); err != nil {
		return nil, err
	}
	if err := loadSeriesSpending(ctx, tx, data, minDataMonth, toMonth); err != nil {
		return nil, err
	}
	if err := loadSeriesFundBalances(data, periods); err != nil {
		return nil, err
	}
	data.axisIDs = seriesAxisIDs(data)
	return data, nil
}

func loadSeriesBudgets(ctx context.Context, tx *sql.Tx, data *seriesRangeData, fromMonth, toMonth string) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT b.month, b.category_id, c.name, b.amount_hundredths
		FROM budgets AS b
		INNER JOIN categories AS c ON c.id = b.category_id
		WHERE b.month >= ? AND b.month <= ?
		ORDER BY b.month ASC, c.name COLLATE NOCASE ASC, b.category_id ASC
	`, fromMonth, toMonth)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var month, name string
		var categoryID, amount int64
		if err := rows.Scan(&month, &categoryID, &name, &amount); err != nil {
			return err
		}
		if amount < 0 {
			return fmtOverflow("budget")
		}
		byCategory := data.budgetsByMonth[month]
		if byCategory == nil {
			byCategory = make(map[int64]int64)
			data.budgetsByMonth[month] = byCategory
		}
		byCategory[categoryID] = amount
		data.categoryNames[categoryID] = name
		if month >= data.fromMonth && month <= data.toMonth {
			data.snapshots[month] = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return rows.Close()
}

func loadSeriesRollovers(ctx context.Context, tx *sql.Tx, data *seriesRangeData, fromMonth, toMonth string) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT r.target_month, r.category_id, c.name, r.amount_hundredths
		FROM budget_rollovers AS r
		INNER JOIN categories AS c ON c.id = r.category_id
		WHERE r.target_month >= ? AND r.target_month <= ?
		ORDER BY r.target_month ASC, c.name COLLATE NOCASE ASC, r.category_id ASC, r.id ASC
	`, fromMonth, toMonth)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var month, name string
		var categoryID, amount int64
		if err := rows.Scan(&month, &categoryID, &name, &amount); err != nil {
			return err
		}
		if amount < 0 {
			return fmtOverflow("rollover adjustment")
		}
		byCategory := data.rolloversByMonth[month]
		if byCategory == nil {
			byCategory = make(map[int64]int64)
			data.rolloversByMonth[month] = byCategory
		}
		adjustment, ok := checkedSubtract(byCategory[categoryID], amount)
		if !ok {
			return fmtOverflow("rollover adjustment")
		}
		byCategory[categoryID] = adjustment
		data.categoryNames[categoryID] = name
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return rows.Close()
}

func loadSeriesSpending(ctx context.Context, tx *sql.Tx, data *seriesRangeData, fromMonth, toMonth string) error {
	startDate, _, err := contract.MonthDateRange(fromMonth)
	if err != nil {
		return err
	}
	_, endDate, err := contract.MonthDateRange(toMonth)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT substr(t.date, 1, 7), t.id, a.category_id, c.name, a.amount_hundredths
		FROM transaction_allocations AS a
		INNER JOIN transactions AS t ON t.id = a.transaction_id
		INNER JOIN categories AS c ON c.id = a.category_id
		WHERE t.date >= ? AND t.date <= ?
		ORDER BY t.date ASC, t.id ASC, a.category_id ASC
	`, startDate, endDate)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	seenTransactions := make(map[string]map[int64]struct{})
	seenCategoryTransactions := make(map[string]map[int64]map[int64]struct{})
	for rows.Next() {
		var month, name string
		var transactionID, categoryID, amount int64
		if err := rows.Scan(&month, &transactionID, &categoryID, &name, &amount); err != nil {
			return err
		}
		if amount < 0 {
			return fmtOverflow("spending")
		}
		total, ok := checkedAdd(data.totalSpending[month], amount)
		if !ok {
			return fmtOverflow("spending")
		}
		data.totalSpending[month] = total
		byCategory := data.spendingByMonth[month]
		if byCategory == nil {
			byCategory = make(map[int64]int64)
			data.spendingByMonth[month] = byCategory
		}
		categoryTotal, ok := checkedAdd(byCategory[categoryID], amount)
		if !ok {
			return fmtOverflow("spending")
		}
		byCategory[categoryID] = categoryTotal
		data.categoryNames[categoryID] = name

		monthTransactions := seenTransactions[month]
		if monthTransactions == nil {
			monthTransactions = make(map[int64]struct{})
			seenTransactions[month] = monthTransactions
		}
		if _, seen := monthTransactions[transactionID]; !seen {
			monthTransactions[transactionID] = struct{}{}
			if data.totalTransactions[month] == math.MaxInt64 {
				return fmtOverflow("transaction count")
			}
			data.totalTransactions[month]++
		}

		monthCategories := seenCategoryTransactions[month]
		if monthCategories == nil {
			monthCategories = make(map[int64]map[int64]struct{})
			seenCategoryTransactions[month] = monthCategories
		}
		categoryTransactions := monthCategories[categoryID]
		if categoryTransactions == nil {
			categoryTransactions = make(map[int64]struct{})
			monthCategories[categoryID] = categoryTransactions
		}
		if _, seen := categoryTransactions[transactionID]; !seen {
			categoryTransactions[transactionID] = struct{}{}
			counts := data.categoryCounts[month]
			if counts == nil {
				counts = make(map[int64]int64)
				data.categoryCounts[month] = counts
			}
			if counts[categoryID] == math.MaxInt64 {
				return fmtOverflow("category transaction count")
			}
			counts[categoryID]++
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return rows.Close()
}

func loadSeriesFundBalances(data *seriesRangeData, periods []seriesFundPeriod) error {
	monthIndexes := make(map[string]int, len(data.allMonths))
	for index, month := range data.allMonths {
		monthIndexes[month] = index
	}
	for _, period := range periods {
		startIndex, ok := monthIndexes[period.startMonth]
		if !ok {
			return fmtOverflow("sinking fund month")
		}
		lastMonth := data.toMonth
		if period.endMonth != nil && *period.endMonth < lastMonth {
			lastMonth = *period.endMonth
		}
		lastIndex, ok := monthIndexes[lastMonth]
		if !ok {
			return fmtOverflow("sinking fund month")
		}

		var priorBudget, priorSpending int64
		for index := startIndex; index <= lastIndex; index++ {
			month := data.allMonths[index]
			opening, ok := checkedSubtract(priorBudget, priorSpending)
			if !ok {
				return fmtOverflow("sinking fund opening balance")
			}
			contribution := data.budgetsByMonth[month][period.categoryID]
			if month >= data.fromMonth {
				available, ok := checkedAddSigned(opening, contribution)
				if !ok {
					return fmtOverflow("sinking fund available balance")
				}
				byCategory := data.fundsByMonth[month]
				if byCategory == nil {
					byCategory = make(map[int64]seriesFundValue)
					data.fundsByMonth[month] = byCategory
				}
				byCategory[period.categoryID] = seriesFundValue{
					opening:      opening,
					contribution: contribution,
					available:    available,
				}
				data.categoryNames[period.categoryID] = period.name
			}

			currentBudget, ok := checkedAdd(priorBudget, contribution)
			if !ok {
				return fmtOverflow("sinking fund budget")
			}
			priorBudget = currentBudget
			currentSpending := data.spendingByMonth[month][period.categoryID]
			currentTotalSpending, ok := checkedAdd(priorSpending, currentSpending)
			if !ok {
				return fmtOverflow("sinking fund spending")
			}
			priorSpending = currentTotalSpending
		}
	}
	return nil
}

func seriesAxisIDs(data *seriesRangeData) []int64 {
	ids := make(map[int64]struct{})
	for _, month := range data.months {
		for id := range data.budgetsByMonth[month] {
			ids[id] = struct{}{}
		}
		for id := range data.rolloversByMonth[month] {
			ids[id] = struct{}{}
		}
		for id := range data.spendingByMonth[month] {
			ids[id] = struct{}{}
		}
		for id := range data.fundsByMonth[month] {
			ids[id] = struct{}{}
		}
	}
	ordered := make([]int64, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool {
		left := strings.ToLower(data.categoryNames[ordered[i]])
		right := strings.ToLower(data.categoryNames[ordered[j]])
		if left != right {
			return left < right
		}
		return ordered[i] < ordered[j]
	})
	return ordered
}

func formatSeriesMonth(data *seriesRangeData, month string, categoryID *int64, includeCategories bool) (SeriesMonth, error) {
	rowIDs := data.axisIDs
	if categoryID != nil {
		rowIDs = []int64{*categoryID}
	}
	rows := make(map[int64]*categoryAmounts, len(rowIDs))
	for _, id := range rowIDs {
		rows[id] = &categoryAmounts{name: data.categoryNames[id]}
	}
	for id, amount := range data.budgetsByMonth[month] {
		row := rows[id]
		if row == nil {
			continue
		}
		row.baseBudget = amount
	}
	for id, adjustment := range data.rolloversByMonth[month] {
		row := rows[id]
		if row == nil {
			continue
		}
		row.rolloverAdjustment = adjustment
	}
	for id, spending := range data.spendingByMonth[month] {
		row := rows[id]
		if row == nil {
			continue
		}
		row.spending = spending
		row.transactionCount = data.categoryCounts[month][id]
	}
	for _, row := range rows {
		budget, ok := checkedAddSigned(row.baseBudget, row.rolloverAdjustment)
		if !ok {
			return SeriesMonth{}, fmtOverflow("budget")
		}
		row.budget = budget
	}
	for id, fund := range data.fundsByMonth[month] {
		row := rows[id]
		if row == nil {
			continue
		}
		row.sinkingFund = true
		row.sinkingFundOpening = fund.opening
		row.baseBudget = fund.contribution
		row.rolloverAdjustment = 0
		row.budget = fund.available
		row.transactionCount = data.categoryCounts[month][id]
	}

	var totalBaseBudget, totalFundOpening, totalAdjustment, totalBudget int64
	for _, id := range rowIDs {
		row := rows[id]
		var ok bool
		totalBaseBudget, ok = checkedAddSigned(totalBaseBudget, row.baseBudget)
		if !ok {
			return SeriesMonth{}, fmtOverflow("base budget")
		}
		totalFundOpening, ok = checkedAddSigned(totalFundOpening, row.sinkingFundOpening)
		if !ok {
			return SeriesMonth{}, fmtOverflow("sinking fund opening balance")
		}
		totalAdjustment, ok = checkedAddSigned(totalAdjustment, row.rolloverAdjustment)
		if !ok {
			return SeriesMonth{}, fmtOverflow("rollover adjustment")
		}
		totalBudget, ok = checkedAddSigned(totalBudget, row.budget)
		if !ok {
			return SeriesMonth{}, fmtOverflow("budget")
		}
	}

	var totalSpending int64
	var transactionCount int64
	if categoryID == nil {
		totalSpending = data.totalSpending[month]
		transactionCount = data.totalTransactions[month]
	} else {
		totalSpending = rows[*categoryID].spending
		transactionCount = rows[*categoryID].transactionCount
	}
	formattedSpending, err := contract.FormatAmount(totalSpending)
	if err != nil {
		return SeriesMonth{}, err
	}
	result := SeriesMonth{
		Month:            month,
		TotalSpending:    formattedSpending,
		TransactionCount: transactionCount,
	}
	snapshot := data.snapshots[month]
	if snapshot {
		formattedBaseBudget, err := contract.FormatAmount(totalBaseBudget)
		if err != nil {
			return SeriesMonth{}, err
		}
		formattedFundOpening, err := contract.FormatSignedAmount(totalFundOpening)
		if err != nil {
			return SeriesMonth{}, err
		}
		formattedAdjustment, err := contract.FormatSignedAmount(totalAdjustment)
		if err != nil {
			return SeriesMonth{}, err
		}
		formattedBudget, err := contract.FormatSignedAmount(totalBudget)
		if err != nil {
			return SeriesMonth{}, err
		}
		remaining, err := formatRemaining(totalBudget, totalSpending)
		if err != nil {
			return SeriesMonth{}, err
		}
		spentOfBudget, err := SpentOfBudget(totalSpending, totalBudget)
		if err != nil {
			return SeriesMonth{}, err
		}
		result.TotalBaseBudget = &formattedBaseBudget
		result.TotalSinkingFundOpening = &formattedFundOpening
		result.TotalRolloverAdjustment = &formattedAdjustment
		result.TotalBudget = &formattedBudget
		result.Remaining = &remaining
		result.SpentOfBudget = spentOfBudget
	}

	if includeCategories {
		categories := make([]contract.MonthlySeriesCategory, 0, len(data.axisIDs))
		for _, id := range data.axisIDs {
			category, err := formatSeriesCategory(id, rows[id], snapshot, totalBaseBudget, totalSpending)
			if err != nil {
				return SeriesMonth{}, err
			}
			categories = append(categories, category)
		}
		result.Categories = categories
	}
	return result, nil
}

func formatSeriesCategory(id int64, row *categoryAmounts, snapshot bool, totalBaseBudget, totalSpending int64) (contract.MonthlySeriesCategory, error) {
	result := contract.MonthlySeriesCategory{
		CategoryID:       id,
		Category:         row.name,
		SinkingFund:      row.sinkingFund,
		Spending:         "0.00",
		TransactionCount: row.transactionCount,
	}
	formattedSpending, err := contract.FormatAmount(row.spending)
	if err != nil {
		return contract.MonthlySeriesCategory{}, err
	}
	result.Spending = formattedSpending
	result.ShareOfSpending, err = compositionShare(row.spending, totalSpending)
	if err != nil {
		return contract.MonthlySeriesCategory{}, err
	}
	if !snapshot {
		return result, nil
	}

	formattedBaseBudget, err := contract.FormatAmount(row.baseBudget)
	if err != nil {
		return contract.MonthlySeriesCategory{}, err
	}
	formattedAdjustment, err := contract.FormatSignedAmount(row.rolloverAdjustment)
	if err != nil {
		return contract.MonthlySeriesCategory{}, err
	}
	formattedOpening, err := contract.FormatSignedAmount(row.sinkingFundOpening)
	if err != nil {
		return contract.MonthlySeriesCategory{}, err
	}
	formattedBudget, err := contract.FormatSignedAmount(row.budget)
	if err != nil {
		return contract.MonthlySeriesCategory{}, err
	}
	remaining, err := formatRemaining(row.budget, row.spending)
	if err != nil {
		return contract.MonthlySeriesCategory{}, err
	}
	spentOfBudget, err := SpentOfBudget(row.spending, row.budget)
	if err != nil {
		return contract.MonthlySeriesCategory{}, err
	}
	result.BaseBudget = &formattedBaseBudget
	result.RolloverAdjustment = &formattedAdjustment
	result.SinkingFundOpening = &formattedOpening
	result.Budget = &formattedBudget
	result.Remaining = &remaining
	result.SpentOfBudget = spentOfBudget
	result.ShareOfBaseBudget, err = compositionShare(row.baseBudget, totalBaseBudget)
	if err != nil {
		return contract.MonthlySeriesCategory{}, err
	}
	return result, nil
}

func seriesMonths(fromMonth, toMonth string) []string {
	from, _ := time.Parse("2006-01", fromMonth)
	to, _ := time.Parse("2006-01", toMonth)
	months := make([]string, 0, int(seriesMonthCount(fromMonth, toMonth)))
	for current := from; !current.After(to); current = current.AddDate(0, 1, 0) {
		months = append(months, current.Format("2006-01"))
	}
	return months
}
