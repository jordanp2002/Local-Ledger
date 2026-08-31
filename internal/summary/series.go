package summary

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

// SeriesInput identifies an inclusive range of canonical months and an
// optional category filter.
type SeriesInput struct {
	FromMonth string
	ToMonth   string
	Category  *string
}

type SeriesResult struct {
	FromMonth string
	ToMonth   string
	Category  *string
	Months    []SeriesMonth
}

type SeriesMonth struct {
	Month            string
	TotalBudget      *string
	TotalSpending    string
	Remaining        *string
	SpentOfBudget    *string
	TransactionCount int64
}

type validatedSeries struct {
	fromMonth string
	toMonth   string
	category  *string
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
	fields := make([]contract.FieldIssue, 0, 3)
	validated := validatedSeries{}

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

	from, err := time.Parse("2006-01", in.fromMonth)
	if err != nil {
		return SeriesResult{}, nil, err
	}
	to, err := time.Parse("2006-01", in.toMonth)
	if err != nil {
		return SeriesResult{}, nil, err
	}

	months := make([]SeriesMonth, 0, int(seriesMonthCount(in.fromMonth, in.toMonth)))
	for current := from; !current.After(to); current = current.AddDate(0, 1, 0) {
		month := current.Format("2006-01")
		row, err := loadSeriesMonth(ctx, tx, month, categoryID)
		if err != nil {
			return SeriesResult{}, nil, err
		}
		months = append(months, row)
	}

	if err := tx.Commit(); err != nil {
		return SeriesResult{}, nil, err
	}
	return SeriesResult{
		FromMonth: in.fromMonth,
		ToMonth:   in.toMonth,
		Category:  categoryName,
		Months:    months,
	}, nil, nil
}

func loadSeriesMonth(ctx context.Context, tx *sql.Tx, month string, categoryID *int64) (SeriesMonth, error) {
	startDate, endDate, err := contract.MonthDateRange(month)
	if err != nil {
		return SeriesMonth{}, err
	}

	snapshot, err := budgetMonthExists(ctx, tx, month)
	if err != nil {
		return SeriesMonth{}, err
	}

	var budget int64
	if snapshot {
		if categoryID == nil {
			_, budget, err = loadMonthBudgets(ctx, tx, month)
		} else {
			budget, err = loadCategoryBudget(ctx, tx, month, *categoryID)
		}
		if err != nil {
			return SeriesMonth{}, err
		}
	}

	var spending, count int64
	if categoryID == nil {
		spending, count, err = loadUnfilteredSeriesSpending(ctx, tx, startDate, endDate)
	} else {
		spending, count, err = loadCategorySpending(ctx, tx, *categoryID, startDate, endDate)
	}
	if err != nil {
		return SeriesMonth{}, err
	}

	formattedSpending, err := contract.FormatAmount(spending)
	if err != nil {
		return SeriesMonth{}, err
	}
	row := SeriesMonth{
		Month:            month,
		TotalSpending:    formattedSpending,
		TransactionCount: count,
	}
	if !snapshot {
		return row, nil
	}

	formattedBudget, err := contract.FormatAmount(budget)
	if err != nil {
		return SeriesMonth{}, err
	}
	remaining, err := formatRemaining(budget, spending)
	if err != nil {
		return SeriesMonth{}, err
	}
	spentOfBudget, err := SpentOfBudget(spending, budget)
	if err != nil {
		return SeriesMonth{}, err
	}
	row.TotalBudget = &formattedBudget
	row.Remaining = &remaining
	row.SpentOfBudget = spentOfBudget
	return row, nil
}

func loadUnfilteredSeriesSpending(ctx context.Context, tx *sql.Tx, startDate, endDate string) (int64, int64, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT a.transaction_id, a.amount_hundredths
		FROM transaction_allocations AS a
		INNER JOIN transactions AS t ON t.id = a.transaction_id
		WHERE t.date >= ? AND t.date <= ?
	`, startDate, endDate)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = rows.Close() }()

	var total int64
	var count int64
	seenTransactions := make(map[int64]struct{})
	for rows.Next() {
		var transactionID int64
		var amount int64
		if err := rows.Scan(&transactionID, &amount); err != nil {
			return 0, 0, err
		}
		next, ok := checkedAdd(total, amount)
		if !ok {
			return 0, 0, fmtOverflow("spending")
		}
		total = next
		if _, seen := seenTransactions[transactionID]; !seen {
			if count == math.MaxInt64 {
				return 0, 0, fmtOverflow("transaction count")
			}
			seenTransactions[transactionID] = struct{}{}
			count++
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, 0, err
	}
	return total, count, nil
}
