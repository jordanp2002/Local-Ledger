package server

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/jordanp2002/local-finance-mcp/internal/summary"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type monthlySummaryInput struct {
	Month string `json:"month"`
}

type monthlySummaryOutput struct {
	OK            bool                              `json:"ok"`
	Month         string                            `json:"month"`
	TotalBudget   string                            `json:"total_budget"`
	TotalSpending string                            `json:"total_spending"`
	Remaining     string                            `json:"remaining"`
	SpentOfBudget *string                           `json:"spent_of_budget"`
	Categories    []contract.MonthlySummaryCategory `json:"categories"`
}

type categorySummaryInput struct {
	Category string `json:"category"`
	Month    string `json:"month"`
}

type categorySummaryOutput struct {
	OK               bool    `json:"ok"`
	CategoryID       int64   `json:"category_id"`
	Category         string  `json:"category"`
	Month            string  `json:"month"`
	Budget           string  `json:"budget"`
	TotalSpending    string  `json:"total_spending"`
	Remaining        string  `json:"remaining"`
	SpentOfBudget    *string `json:"spent_of_budget"`
	TransactionCount int64   `json:"transaction_count"`
}

type compareMonthsInput struct {
	FromMonth string `json:"from_month"`
	ToMonth   string `json:"to_month"`
}

type compareMonthsOutput struct {
	OK         bool                          `json:"ok"`
	From       contract.ComparisonMonth      `json:"from"`
	To         contract.ComparisonMonth      `json:"to"`
	Change     contract.ComparisonChange     `json:"change"`
	Categories []contract.ComparisonCategory `json:"categories"`
}

type spendingSummaryInput struct {
	StartDate *string `json:"start_date,omitempty"`
	EndDate   *string `json:"end_date,omitempty"`
	Category  *string `json:"category,omitempty"`
	Merchant  *string `json:"merchant,omitempty"`
}

type spendingSummaryOutput struct {
	OK               bool                               `json:"ok"`
	StartDate        *string                            `json:"start_date"`
	EndDate          *string                            `json:"end_date"`
	Category         *string                            `json:"category"`
	Merchant         *string                            `json:"merchant"`
	TotalSpending    string                             `json:"total_spending"`
	TransactionCount int64                              `json:"transaction_count"`
	Categories       []contract.SpendingSummaryCategory `json:"categories"`
}

type topMerchantsInput struct {
	StartDate *string `json:"start_date,omitempty"`
	EndDate   *string `json:"end_date,omitempty"`
	Category  *string `json:"category,omitempty"`
	Limit     *int64  `json:"limit,omitempty"`
}

type topMerchantsOutput struct {
	OK               bool                        `json:"ok"`
	StartDate        *string                     `json:"start_date"`
	EndDate          *string                     `json:"end_date"`
	Category         *string                     `json:"category"`
	TotalSpending    string                      `json:"total_spending"`
	TransactionCount int64                       `json:"transaction_count"`
	Limit            int64                       `json:"limit"`
	Returned         int64                       `json:"returned"`
	MerchantCount    int64                       `json:"merchant_count"`
	Merchants        []contract.MerchantSpending `json:"merchants"`
}

type summaryTools struct {
	store  *summary.Store
	logger *log.Logger
}

func registerSummaryTools(srv *mcp.Server, store *summary.Store, logger *log.Logger) {
	tools := &summaryTools{store: store, logger: logger}

	mcp.AddTool[monthlySummaryInput, any](srv, &mcp.Tool{
		Name:        "get_monthly_summary",
		Description: "Compare a stored monthly budget snapshot with actual spending by category.",
		Annotations: readOnlyToolAnnotations(),
	}, tools.getMonthlySummary)

	mcp.AddTool[categorySummaryInput, any](srv, &mcp.Tool{
		Name:        "get_category_summary",
		Description: "Compare one category's monthly allocation with its spending and transaction count.",
		Annotations: readOnlyToolAnnotations(),
	}, tools.getCategorySummary)

	mcp.AddTool[compareMonthsInput, any](srv, &mcp.Tool{
		Name:        "compare_months",
		Description: "Compare two stored monthly budget snapshots and their spending.",
		Annotations: readOnlyToolAnnotations(),
	}, tools.compareMonths)

	mcp.AddTool[spendingSummaryInput, any](srv, &mcp.Tool{
		Name:        "get_spending_summary",
		Description: "Total spending over an optional inclusive date range, with optional category and exact merchant filters. Does not require a monthly budget snapshot.",
		Annotations: readOnlyToolAnnotations(),
	}, tools.getSpendingSummary)

	mcp.AddTool[topMerchantsInput, any](srv, &mcp.Tool{
		Name:        "list_top_merchants",
		Description: "Rank merchants by spending over an optional inclusive date range, with an optional category filter and top-N limit. Does not require a monthly budget snapshot.",
		Annotations: readOnlyToolAnnotations(),
	}, tools.listTopMerchants)
}

func (t *summaryTools) getMonthlySummary(ctx context.Context, _ *mcp.CallToolRequest, in monthlySummaryInput) (*mcp.CallToolResult, any, error) {
	result, fields, err := t.store.Monthly(ctx, in.Month)
	if len(fields) != 0 {
		return toolError(invalidSummaryInputEnvelope(fields))
	}
	if err != nil {
		return t.mapSummaryError("get_monthly_summary", err)
	}

	categories := result.Categories
	if categories == nil {
		categories = []contract.MonthlySummaryCategory{}
	}
	return toolOK(monthlySummaryOutput{
		OK:            true,
		Month:         result.Month,
		TotalBudget:   result.TotalBudget,
		TotalSpending: result.TotalSpending,
		Remaining:     result.Remaining,
		SpentOfBudget: result.SpentOfBudget,
		Categories:    categories,
	})
}

func (t *summaryTools) getCategorySummary(ctx context.Context, _ *mcp.CallToolRequest, in categorySummaryInput) (*mcp.CallToolResult, any, error) {
	result, fields, err := t.store.Category(ctx, in.Category, in.Month)
	if len(fields) != 0 {
		return toolError(invalidSummaryInputEnvelope(fields))
	}
	if err != nil {
		return t.mapSummaryError("get_category_summary", err)
	}

	return toolOK(categorySummaryOutput{
		OK:               true,
		CategoryID:       result.CategoryID,
		Category:         result.Category,
		Month:            result.Month,
		Budget:           result.Budget,
		TotalSpending:    result.TotalSpending,
		Remaining:        result.Remaining,
		SpentOfBudget:    result.SpentOfBudget,
		TransactionCount: result.TransactionCount,
	})
}

func (t *summaryTools) compareMonths(ctx context.Context, _ *mcp.CallToolRequest, in compareMonthsInput) (*mcp.CallToolResult, any, error) {
	result, fields, err := t.store.Compare(ctx, in.FromMonth, in.ToMonth)
	if len(fields) != 0 {
		return toolError(invalidSummaryInputEnvelope(fields))
	}
	if err != nil {
		return t.mapSummaryError("compare_months", err)
	}

	categories := result.Categories
	if categories == nil {
		categories = []contract.ComparisonCategory{}
	}
	return toolOK(compareMonthsOutput{
		OK:         true,
		From:       result.From,
		To:         result.To,
		Change:     result.Change,
		Categories: categories,
	})
}

func (t *summaryTools) getSpendingSummary(ctx context.Context, _ *mcp.CallToolRequest, in spendingSummaryInput) (*mcp.CallToolResult, any, error) {
	result, fields, err := t.store.Spending(ctx, summary.SpendingInput{
		StartDate: in.StartDate,
		EndDate:   in.EndDate,
		Category:  in.Category,
		Merchant:  in.Merchant,
	})
	if len(fields) != 0 {
		return toolError(invalidSummaryInputEnvelope(fields))
	}
	if err != nil {
		return t.mapSummaryError("get_spending_summary", err)
	}

	categories := result.Categories
	if categories == nil {
		categories = []contract.SpendingSummaryCategory{}
	}
	return toolOK(spendingSummaryOutput{
		OK:               true,
		StartDate:        result.StartDate,
		EndDate:          result.EndDate,
		Category:         result.Category,
		Merchant:         result.Merchant,
		TotalSpending:    result.TotalSpending,
		TransactionCount: result.TransactionCount,
		Categories:       categories,
	})
}

func (t *summaryTools) listTopMerchants(ctx context.Context, _ *mcp.CallToolRequest, in topMerchantsInput) (*mcp.CallToolResult, any, error) {
	result, fields, err := t.store.TopMerchants(ctx, summary.TopMerchantsInput{
		StartDate: in.StartDate,
		EndDate:   in.EndDate,
		Category:  in.Category,
		Limit:     in.Limit,
	})
	if len(fields) != 0 {
		return toolError(invalidSummaryInputEnvelope(fields))
	}
	if err != nil {
		return t.mapSummaryError("list_top_merchants", err)
	}

	merchants := result.Merchants
	if merchants == nil {
		merchants = []contract.MerchantSpending{}
	}
	return toolOK(topMerchantsOutput{
		OK:               true,
		StartDate:        result.StartDate,
		EndDate:          result.EndDate,
		Category:         result.Category,
		TotalSpending:    result.TotalSpending,
		TransactionCount: result.TransactionCount,
		Limit:            result.Limit,
		Returned:         result.Returned,
		MerchantCount:    result.MerchantCount,
		Merchants:        merchants,
	})
}

func (t *summaryTools) mapSummaryError(tool string, err error) (*mcp.CallToolResult, any, error) {
	var monthNotFound *summary.NotFoundError
	if errors.As(err, &monthNotFound) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeMonthlyBudgetNotFound,
			fmt.Sprintf("No monthly budget exists for %s.", monthNotFound.Month),
			false,
			map[string]any{
				"month":                monthNotFound.Month,
				"latest_earlier_month": monthNotFound.LatestEarlierMonth,
			},
		)))
	}

	var categoryNotFound *summary.CategoryNotFoundError
	if errors.As(err, &categoryNotFound) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeCategoryNotFound,
			fmt.Sprintf("Category '%s' does not exist.", categoryNotFound.Requested),
			false,
			map[string]any{
				"requested_category": categoryNotFound.Requested,
				"categories":         activeCategories(categoryNotFound.ActiveCategories),
			},
		)))
	}

	return t.internalError(tool, err)
}

func invalidSummaryInputEnvelope(fields []contract.FieldIssue) contract.ErrorEnvelope {
	return contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{"fields": fields},
	))
}

func (t *summaryTools) internalError(tool string, err error) (*mcp.CallToolResult, any, error) {
	if t.logger != nil {
		t.logger.Printf("%s: %v", tool, err)
	}
	return toolError(contract.NewInternalErrorEnvelope())
}
