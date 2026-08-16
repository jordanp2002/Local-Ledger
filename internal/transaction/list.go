package transaction

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

const transactionColumns = `
	t.id,
	t.amount_hundredths,
	t.merchant,
	t.date,
	t.category_id,
	c.name,
	t.note,
	t.created_at,
	t.updated_at
`

func scanTransaction(row interface{ Scan(dest ...any) error }) (contract.Transaction, error) {
	var recorded contract.Transaction
	var hundredths int64
	var note sql.NullString
	if err := row.Scan(
		&recorded.ID,
		&hundredths,
		&recorded.Merchant,
		&recorded.Date,
		&recorded.CategoryID,
		&recorded.Category,
		&note,
		&recorded.CreatedAt,
		&recorded.UpdatedAt,
	); err != nil {
		return contract.Transaction{}, err
	}
	formatted, err := contract.FormatAmount(hundredths)
	if err != nil {
		return contract.Transaction{}, err
	}
	recorded.Amount = formatted
	if note.Valid {
		recorded.Note = &note.String
	}
	return recorded, nil
}

const (
	DefaultLimit int64 = 50
	MaxLimit     int64 = 200
)

// ListInput is one list_transactions request at the store boundary.
// Nil pointers are omitted fields.
type ListInput struct {
	StartDate *string
	EndDate   *string
	Category  *string
	Limit     *int64
	Offset    *int64
}

// ListResult is one snapshot-consistent page of canonical transactions.
type ListResult struct {
	Transactions []contract.Transaction
	Page         contract.Page
}

type validatedList struct {
	startDate *string
	endDate   *string
	category  *string
	limit     int64
	offset    int64
}

// List returns transactions matching optional inclusive date and category
// filters. It never writes transactions, mappings, or budgets.
func (s *Store) List(ctx context.Context, in ListInput) (ListResult, []contract.FieldIssue, error) {
	validated, fields := validateList(in)
	if len(fields) != 0 {
		return ListResult{}, fields, nil
	}
	if s == nil || s.DB == nil {
		return ListResult{}, nil, errors.New("transaction store database is nil")
	}
	return s.list(ctx, validated)
}

func validateList(in ListInput) (validatedList, []contract.FieldIssue) {
	fields := make([]contract.FieldIssue, 0)
	validated := validatedList{}

	startValid := false
	if in.StartDate != nil {
		if date, issue := validateListDate("start_date", *in.StartDate); issue != nil {
			fields = append(fields, *issue)
		} else {
			validated.startDate = &date
			startValid = true
		}
	}

	endValid := false
	if in.EndDate != nil {
		if date, issue := validateListDate("end_date", *in.EndDate); issue != nil {
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

	validated.limit = DefaultLimit
	if in.Limit != nil {
		validated.limit = *in.Limit
		if validated.limit < 1 || validated.limit > MaxLimit {
			fields = append(fields, contract.FieldIssue{
				Field:  "limit",
				Reason: "must be between 1 and 200",
			})
		}
	}

	validated.offset = 0
	if in.Offset != nil {
		validated.offset = *in.Offset
		if validated.offset < 0 {
			fields = append(fields, contract.FieldIssue{
				Field:  "offset",
				Reason: "must be zero or greater",
			})
		}
	}

	return validated, fields
}

func validateListDate(field, value string) (string, *contract.FieldIssue) {
	parsed, err := contract.ParseDate(value)
	if err != nil {
		return "", &contract.FieldIssue{
			Field:  field,
			Reason: "must be a valid YYYY-MM-DD date",
		}
	}
	return parsed, nil
}

func (s *Store) list(ctx context.Context, in validatedList) (ListResult, []contract.FieldIssue, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ListResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var categoryID *int64
	if in.category != nil {
		category, found, err := lookupCategory(ctx, tx, *in.category)
		if err != nil {
			return ListResult{}, nil, err
		}
		if !found {
			activeCategories, err := listActiveCategories(ctx, tx)
			if err != nil {
				return ListResult{}, nil, err
			}
			return ListResult{}, nil, &CategoryNotFoundError{
				Requested:        *in.category,
				ActiveCategories: activeCategories,
			}
		}
		categoryID = &category.ID
	}

	where, args := listFilter(in.startDate, in.endDate, categoryID)

	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM transactions AS t`+where, args...).Scan(&total); err != nil {
		return ListResult{}, nil, err
	}

	pageArgs := append(append([]any(nil), args...), in.limit, in.offset)
	rows, err := tx.QueryContext(ctx, `
		SELECT `+transactionColumns+`
		FROM transactions AS t
		INNER JOIN categories AS c ON c.id = t.category_id`+where+`
		ORDER BY t.date DESC, t.id DESC
		LIMIT ? OFFSET ?
	`, pageArgs...)
	if err != nil {
		return ListResult{}, nil, err
	}
	defer rows.Close()

	transactions := make([]contract.Transaction, 0)
	for rows.Next() {
		recorded, err := scanTransaction(rows)
		if err != nil {
			return ListResult{}, nil, err
		}
		transactions = append(transactions, recorded)
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, nil, err
	}

	returned := int64(len(transactions))
	hasMore := in.offset < total && returned < total-in.offset
	result := ListResult{
		Transactions: transactions,
		Page: contract.Page{
			Limit:    in.limit,
			Offset:   in.offset,
			Returned: returned,
			Total:    total,
			HasMore:  hasMore,
		},
	}
	if err := tx.Commit(); err != nil {
		return ListResult{}, nil, err
	}
	return result, nil, nil
}

func listFilter(startDate, endDate *string, categoryID *int64) (string, []any) {
	clauses := make([]string, 0, 3)
	args := make([]any, 0, 3)
	if startDate != nil {
		clauses = append(clauses, "t.date >= ?")
		args = append(args, *startDate)
	}
	if endDate != nil {
		clauses = append(clauses, "t.date <= ?")
		args = append(args, *endDate)
	}
	if categoryID != nil {
		clauses = append(clauses, "t.category_id = ?")
		args = append(args, *categoryID)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}
