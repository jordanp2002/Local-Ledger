package server

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/jordanp2002/local-finance-mcp/internal/budget"
	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type createMonthlyBudgetInput struct {
	Month   string                          `json:"month"`
	Budgets []createMonthlyBudgetAllocation `json:"budgets"`
}

type createMonthlyBudgetAllocation struct {
	Category string `json:"category"`
	Amount   string `json:"amount"`
}

type createMonthlyBudgetOutput struct {
	OK           bool              `json:"ok"`
	Month        string            `json:"month"`
	CreationMode string            `json:"creation_mode"`
	SourceMonth  *string           `json:"source_month"`
	TotalBudget  string            `json:"total_budget"`
	Budgets      []contract.Budget `json:"budgets"`
}

type budgetTools struct {
	store  *budget.Store
	logger *log.Logger
}

func registerBudgetTools(srv *mcp.Server, store *budget.Store, logger *log.Logger) {
	tools := &budgetTools{store: store, logger: logger}

	mcp.AddTool[createMonthlyBudgetInput, any](srv, &mcp.Tool{
		Name:        "create_monthly_budget",
		Description: "Create the explicit budget snapshot for the server's current local month.",
	}, tools.createMonthlyBudget)
}

func (t *budgetTools) createMonthlyBudget(ctx context.Context, _ *mcp.CallToolRequest, in createMonthlyBudgetInput) (*mcp.CallToolResult, any, error) {
	allocations := make([]budget.Allocation, len(in.Budgets))
	for i, allocation := range in.Budgets {
		allocations[i] = budget.Allocation{
			Category: allocation.Category,
			Amount:   allocation.Amount,
		}
	}

	result, fields, err := t.store.CreateExplicit(ctx, in.Month, allocations)
	if len(fields) != 0 {
		return toolError(invalidBudgetInputEnvelope(fields))
	}
	if err != nil {
		var alreadyExists *budget.AlreadyExistsError
		if errors.As(err, &alreadyExists) {
			return toolError(contract.NewErrorEnvelope(contract.NewError(
				contract.ErrorCodeMonthlyBudgetAlreadyExists,
				fmt.Sprintf("A monthly budget already exists for %s.", alreadyExists.Month),
				false,
				map[string]any{"month": alreadyExists.Month},
			)))
		}

		var notFound *budget.CategoryNotFoundError
		if errors.As(err, &notFound) {
			return toolError(contract.NewErrorEnvelope(contract.NewError(
				contract.ErrorCodeCategoryNotFound,
				fmt.Sprintf("Category '%s' does not exist.", notFound.Requested),
				false,
				map[string]any{
					"requested_category": notFound.Requested,
					"categories":         activeCategories(notFound.ActiveCategories),
				},
			)))
		}

		var inactive *budget.CategoryInactiveError
		if errors.As(err, &inactive) {
			return toolError(contract.NewErrorEnvelope(contract.NewError(
				contract.ErrorCodeCategoryInactive,
				fmt.Sprintf("Category '%s' is inactive.", inactive.Category.Name),
				false,
				map[string]any{
					"category":          inactive.Category,
					"active_categories": activeCategories(inactive.ActiveCategories),
				},
			)))
		}

		return t.internalError("create_monthly_budget", err)
	}

	return toolOK(createMonthlyBudgetOutput{
		OK:           true,
		Month:        result.Month,
		CreationMode: "explicit",
		SourceMonth:  nil,
		TotalBudget:  result.TotalBudget,
		Budgets:      activeBudgets(result.Budgets),
	})
}

func invalidBudgetInputEnvelope(fields []contract.FieldIssue) contract.ErrorEnvelope {
	return contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{"fields": fields},
	))
}

func activeBudgets(budgets []contract.Budget) []contract.Budget {
	if budgets == nil {
		return []contract.Budget{}
	}
	return budgets
}

func (t *budgetTools) internalError(tool string, err error) (*mcp.CallToolResult, any, error) {
	if t.logger != nil {
		t.logger.Printf("%s: %v", tool, err)
	}
	return toolError(contract.NewInternalErrorEnvelope())
}
