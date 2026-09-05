package summary

import (
	"context"
	"errors"
	"strings"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
)

type SpendingInput struct {
	StartDate *string
	EndDate   *string
	Category  *string
	Merchant  *string
}

type SpendingResult struct {
	StartDate        *string
	EndDate          *string
	Category         *string
	Merchant         *string
	TotalSpending    string
	TransactionCount int64
	Categories       []contract.SpendingSummaryCategory
}

type validatedSpending struct {
	startDate *string
	endDate   *string
	category  *string
	merchant  *string
}

type categorySpending struct {
	name  string
	total int64
	count int64
}

func (s *Store) Spending(ctx context.Context, in SpendingInput) (SpendingResult, []contract.FieldIssue, error) {
	validated, fields := validateSpending(in)
	if len(fields) != 0 {
		return SpendingResult{}, fields, nil
	}
	if s == nil || s.DB == nil {
		return SpendingResult{}, nil, errors.New("summary store database is nil")
	}
	return s.spending(ctx, validated)
}

func validateSpending(in SpendingInput) (validatedSpending, []contract.FieldIssue) {
	fields := make([]contract.FieldIssue, 0)
	validated := validatedSpending{}

	startValid := false
	if in.StartDate != nil {
		if date, issue := validateSpendingDate("start_date", *in.StartDate); issue != nil {
			fields = append(fields, *issue)
		} else {
			validated.startDate = &date
			startValid = true
		}
	}

	endValid := false
	if in.EndDate != nil {
		if date, issue := validateSpendingDate("end_date", *in.EndDate); issue != nil {
			fields = append(fields, *issue)
		} else {
			validated.endDate = &date
			endValid = true
		}
	}

	if startValid && endValid && *validated.startDate > *validated.endDate {
		fields = append(fields, contract.FieldIssue{
			Field:  "end_date",
			Reason: "must be on or after start_date",
		})
	}

	if in.Category != nil {
		if category, issue := validateCategoryName(*in.Category); issue != nil {
			fields = append(fields, *issue)
		} else {
			validated.category = &category
		}
	}

	if in.Merchant != nil {
		if merchant, issue := validateMerchant(*in.Merchant); issue != nil {
			fields = append(fields, *issue)
		} else {
			validated.merchant = &merchant
		}
	}

	return validated, fields
}

func validateSpendingDate(field, value string) (string, *contract.FieldIssue) {
	parsed, err := contract.ParseDate(value)
	if err != nil {
		return "", &contract.FieldIssue{
			Field:  field,
			Reason: "must be a valid YYYY-MM-DD date",
		}
	}
	return parsed, nil
}

func (s *Store) spending(ctx context.Context, in validatedSpending) (SpendingResult, []contract.FieldIssue, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return SpendingResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var categoryID *int64
	var categoryName *string
	if in.category != nil {
		category, found, err := lookupCategory(ctx, tx, *in.category)
		if err != nil {
			return SpendingResult{}, nil, err
		}
		if !found {
			activeCategories, err := listActiveCategories(ctx, tx)
			if err != nil {
				return SpendingResult{}, nil, err
			}
			return SpendingResult{}, nil, &CategoryNotFoundError{
				Requested:        *in.category,
				ActiveCategories: activeCategories,
			}
		}
		categoryID = &category.ID
		categoryName = &category.Name
	}

	where, args := spendingFilter(in.startDate, in.endDate, categoryID, in.merchant)
	rows, err := tx.QueryContext(ctx, `
		SELECT t.id, a.category_id, c.name, a.amount_hundredths
		FROM transaction_allocations AS a
		INNER JOIN transactions AS t ON t.id = a.transaction_id
		INNER JOIN categories AS c ON c.id = a.category_id`+where, args...)
	if err != nil {
		return SpendingResult{}, nil, err
	}
	defer rows.Close()

	amounts := make(map[int64]*categorySpending)
	var total int64
	var count int64
	seenTransactions := make(map[int64]struct{})
	for rows.Next() {
		var transactionID int64
		var id int64
		var name string
		var amount int64
		if err := rows.Scan(&transactionID, &id, &name, &amount); err != nil {
			return SpendingResult{}, nil, err
		}
		next, ok := checkedAdd(total, amount)
		if !ok {
			return SpendingResult{}, nil, fmtOverflow("spending")
		}
		total = next
		if _, seen := seenTransactions[transactionID]; !seen {
			seenTransactions[transactionID] = struct{}{}
			count++
		}
		row, exists := amounts[id]
		if !exists {
			row = &categorySpending{name: name}
			amounts[id] = row
		}
		nextSpending, ok := checkedAdd(row.total, amount)
		if !ok {
			return SpendingResult{}, nil, fmtOverflow("spending")
		}
		row.total = nextSpending
		row.count++
	}
	if err := rows.Err(); err != nil {
		return SpendingResult{}, nil, err
	}
	if err := rows.Close(); err != nil {
		return SpendingResult{}, nil, err
	}

	ids := make([]int64, 0, len(amounts))
	for id := range amounts {
		ids = append(ids, id)
	}
	ordered, err := orderCategoryIDs(ctx, tx, ids)
	if err != nil {
		return SpendingResult{}, nil, err
	}

	categories := make([]contract.SpendingSummaryCategory, 0, len(ordered))
	for _, id := range ordered {
		row := amounts[id]
		formatted, err := contract.FormatAmount(row.total)
		if err != nil {
			return SpendingResult{}, nil, err
		}
		categories = append(categories, contract.SpendingSummaryCategory{
			CategoryID:       id,
			Category:         row.name,
			Spending:         formatted,
			TransactionCount: row.count,
		})
	}

	formattedTotal, err := contract.FormatAmount(total)
	if err != nil {
		return SpendingResult{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return SpendingResult{}, nil, err
	}
	return SpendingResult{
		StartDate:        in.startDate,
		EndDate:          in.endDate,
		Category:         categoryName,
		Merchant:         in.merchant,
		TotalSpending:    formattedTotal,
		TransactionCount: count,
		Categories:       categories,
	}, nil, nil
}

func spendingFilter(startDate, endDate *string, categoryID *int64, merchant *string) (string, []any) {
	clauses := make([]string, 0, 4)
	args := make([]any, 0, 4)
	if startDate != nil {
		clauses = append(clauses, "t.date >= ?")
		args = append(args, *startDate)
	}
	if endDate != nil {
		clauses = append(clauses, "t.date <= ?")
		args = append(args, *endDate)
	}
	if categoryID != nil {
		clauses = append(clauses, "a.category_id = ?")
		args = append(args, *categoryID)
	}
	if merchant != nil {
		clauses = append(clauses, "t.merchant = ? COLLATE NOCASE")
		args = append(args, *merchant)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}
