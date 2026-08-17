// Package summary compares stored monthly budgets with actual spending.
package summary

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

var (
	ErrNotFound         = errors.New("monthly budget not found")
	ErrCategoryNotFound = errors.New("category not found")
)

// MonthlyResult is the canonical result of get_monthly_summary.
type MonthlyResult struct {
	Month         string
	TotalBudget   string
	TotalSpending string
	Remaining     string
	Categories    []contract.MonthlySummaryCategory
}

// Store owns summary validation and read-only persistence.
type Store struct {
	DB *sql.DB
}

// NotFoundError identifies a month with no budget snapshot.
type NotFoundError struct {
	Month              string
	LatestEarlierMonth *string
}

func (e *NotFoundError) Error() string {
	if e == nil {
		return ErrNotFound.Error()
	}
	return fmt.Sprintf("monthly budget not found for %s", e.Month)
}

func (e *NotFoundError) Is(target error) bool {
	return target == ErrNotFound
}

// CategoryNotFoundError identifies a missing category.
type CategoryNotFoundError struct {
	Requested        string
	ActiveCategories []contract.Category
}

func (e *CategoryNotFoundError) Error() string {
	if e == nil {
		return ErrCategoryNotFound.Error()
	}
	return fmt.Sprintf("category %q not found", e.Requested)
}

func (e *CategoryNotFoundError) Is(target error) bool {
	return target == ErrCategoryNotFound
}

// Monthly returns the monthly budget-versus-spending summary.
func (s *Store) Monthly(ctx context.Context, month string) (MonthlyResult, []contract.FieldIssue, error) {
	parsed, issue := validateMonth(month)
	if issue != nil {
		return MonthlyResult{}, []contract.FieldIssue{*issue}, nil
	}
	if s == nil || s.DB == nil {
		return MonthlyResult{}, nil, errors.New("summary store database is nil")
	}
	return s.monthly(ctx, parsed)
}

// Category returns one category's budget-versus-spending summary for a month.
func (s *Store) Category(ctx context.Context, categoryName, month string) (contract.CategorySummary, []contract.FieldIssue, error) {
	fields := make([]contract.FieldIssue, 0, 2)
	category, categoryIssue := validateCategoryName(categoryName)
	if categoryIssue != nil {
		fields = append(fields, *categoryIssue)
	}
	parsed, monthIssue := validateMonth(month)
	if monthIssue != nil {
		fields = append(fields, *monthIssue)
	}
	if len(fields) != 0 {
		return contract.CategorySummary{}, fields, nil
	}
	if s == nil || s.DB == nil {
		return contract.CategorySummary{}, nil, errors.New("summary store database is nil")
	}
	return s.category(ctx, category, parsed)
}

func validateMonth(month string) (string, *contract.FieldIssue) {
	parsed, err := contract.ParseMonth(month)
	if err != nil {
		return "", &contract.FieldIssue{
			Field:  "month",
			Reason: "must be a valid YYYY-MM month",
		}
	}
	return parsed, nil
}

func validateCategoryName(value string) (string, *contract.FieldIssue) {
	category := contract.TrimASCIIWhitespace(value)
	switch {
	case category == "":
		return "", &contract.FieldIssue{
			Field:  "category",
			Reason: "must not be empty",
		}
	case strings.ContainsRune(category, '\x00'):
		return "", &contract.FieldIssue{
			Field:  "category",
			Reason: "must not contain NUL characters",
		}
	default:
		return category, nil
	}
}

func checkedAdd(left, right int64) (int64, bool) {
	if left < 0 || right < 0 || right > math.MaxInt64-left {
		return 0, false
	}
	return left + right, true
}

func formatRemaining(budget, spending int64) (string, error) {
	return contract.FormatSignedAmount(budget - spending)
}
