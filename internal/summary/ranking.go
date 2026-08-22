package summary

import (
	"context"
	"errors"
	"sort"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

const (
	defaultTopMerchantsLimit int64 = 10
	maxTopMerchantsLimit     int64 = 50
)

type TopMerchantsInput struct {
	StartDate *string
	EndDate   *string
	Category  *string
	Limit     *int64
}

type TopMerchantsResult struct {
	StartDate        *string
	EndDate          *string
	Category         *string
	TotalSpending    string
	TransactionCount int64
	Limit            int64
	Returned         int64
	MerchantCount    int64
	Merchants        []contract.MerchantSpending
}

type validatedTopMerchants struct {
	validatedSpending
	limit int64
}

type merchantTotals struct {
	merchant string
	total    int64
	count    int64
}

func (s *Store) TopMerchants(ctx context.Context, in TopMerchantsInput) (TopMerchantsResult, []contract.FieldIssue, error) {
	validated, fields := validateTopMerchants(in)
	if len(fields) != 0 {
		return TopMerchantsResult{}, fields, nil
	}
	if s == nil || s.DB == nil {
		return TopMerchantsResult{}, nil, errors.New("summary store database is nil")
	}
	return s.topMerchants(ctx, validated)
}

func validateTopMerchants(in TopMerchantsInput) (validatedTopMerchants, []contract.FieldIssue) {
	filters, fields := validateSpending(SpendingInput{
		StartDate: in.StartDate,
		EndDate:   in.EndDate,
		Category:  in.Category,
	})
	validated := validatedTopMerchants{
		validatedSpending: filters,
		limit:             defaultTopMerchantsLimit,
	}
	if in.Limit != nil {
		validated.limit = *in.Limit
		if validated.limit < 1 || validated.limit > maxTopMerchantsLimit {
			fields = append(fields, contract.FieldIssue{
				Field:  "limit",
				Reason: "must be between 1 and 50",
			})
		}
	}
	return validated, fields
}

func (s *Store) topMerchants(ctx context.Context, in validatedTopMerchants) (TopMerchantsResult, []contract.FieldIssue, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return TopMerchantsResult{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var categoryID *int64
	var categoryName *string
	if in.category != nil {
		category, found, err := lookupCategory(ctx, tx, *in.category)
		if err != nil {
			return TopMerchantsResult{}, nil, err
		}
		if !found {
			activeCategories, err := listActiveCategories(ctx, tx)
			if err != nil {
				return TopMerchantsResult{}, nil, err
			}
			return TopMerchantsResult{}, nil, &CategoryNotFoundError{
				Requested:        *in.category,
				ActiveCategories: activeCategories,
			}
		}
		categoryID = &category.ID
		categoryName = &category.Name
	}

	where, args := spendingFilter(in.startDate, in.endDate, categoryID, nil)
	rows, err := tx.QueryContext(ctx, `
		SELECT t.merchant, t.amount_hundredths
		FROM transactions AS t`+where+`
		ORDER BY t.date DESC, t.id DESC
	`, args...)
	if err != nil {
		return TopMerchantsResult{}, nil, err
	}
	defer func() { _ = rows.Close() }()

	byMerchant := make(map[string]*merchantTotals)
	var total int64
	var count int64
	for rows.Next() {
		var merchant string
		var amount int64
		if err := rows.Scan(&merchant, &amount); err != nil {
			return TopMerchantsResult{}, nil, err
		}

		next, ok := checkedAdd(total, amount)
		if !ok {
			return TopMerchantsResult{}, nil, fmtOverflow("spending")
		}
		total = next
		count++

		key := merchantNoCaseKey(merchant)
		group, exists := byMerchant[key]
		if !exists {
			group = &merchantTotals{merchant: merchant}
			byMerchant[key] = group
		}
		nextGroupTotal, ok := checkedAdd(group.total, amount)
		if !ok {
			return TopMerchantsResult{}, nil, fmtOverflow("spending")
		}
		group.total = nextGroupTotal
		group.count++
	}
	if err := rows.Err(); err != nil {
		return TopMerchantsResult{}, nil, err
	}
	if err := rows.Close(); err != nil {
		return TopMerchantsResult{}, nil, err
	}

	groups := make([]merchantTotals, 0, len(byMerchant))
	for _, group := range byMerchant {
		groups = append(groups, *group)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].total != groups[j].total {
			return groups[i].total > groups[j].total
		}
		if groups[i].count != groups[j].count {
			return groups[i].count > groups[j].count
		}
		leftKey := merchantNoCaseKey(groups[i].merchant)
		rightKey := merchantNoCaseKey(groups[j].merchant)
		if leftKey != rightKey {
			return leftKey < rightKey
		}
		return groups[i].merchant < groups[j].merchant
	})

	formattedTotal, err := contract.FormatAmount(total)
	if err != nil {
		return TopMerchantsResult{}, nil, err
	}
	merchantCount := int64(len(groups))
	returnedCount := merchantCount
	if returnedCount > in.limit {
		returnedCount = in.limit
	}
	merchants := make([]contract.MerchantSpending, 0, int(returnedCount))
	for _, group := range groups[:int(returnedCount)] {
		formatted, err := contract.FormatAmount(group.total)
		if err != nil {
			return TopMerchantsResult{}, nil, err
		}
		merchants = append(merchants, contract.MerchantSpending{
			Merchant:         group.merchant,
			Spending:         formatted,
			TransactionCount: group.count,
		})
	}

	if err := tx.Commit(); err != nil {
		return TopMerchantsResult{}, nil, err
	}
	return TopMerchantsResult{
		StartDate:        in.startDate,
		EndDate:          in.endDate,
		Category:         categoryName,
		TotalSpending:    formattedTotal,
		TransactionCount: count,
		Limit:            in.limit,
		Returned:         returnedCount,
		MerchantCount:    merchantCount,
		Merchants:        merchants,
	}, nil, nil
}

// SQLite's built-in NOCASE collation folds ASCII letters only. Mirror that
// behavior when grouping and ordering merchant text in Go.
func merchantNoCaseKey(value string) string {
	key := []byte(value)
	for i, character := range key {
		if character >= 'A' && character <= 'Z' {
			key[i] = character + ('a' - 'A')
		}
	}
	return string(key)
}
