package server

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/jordanp2002/Local-Ledger/internal/budget"
	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/rollover"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type createMonthlyBudgetInput struct {
	Month        string                          `json:"month"`
	Budgets      []createMonthlyBudgetAllocation `json:"budgets,omitempty"`
	CarryForward *bool                           `json:"carry_forward,omitempty"`
	Overrides    []createMonthlyBudgetAllocation `json:"overrides,omitempty"`
}

type setBudgetsInput struct {
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

type setBudgetsChange struct {
	Budget  contract.Budget `json:"budget"`
	Created bool            `json:"created"`
}

type setBudgetsOutput struct {
	OK      bool               `json:"ok"`
	Month   string             `json:"month"`
	Changes []setBudgetsChange `json:"changes"`
}

type budgetTools struct {
	store  *budget.Store
	logger *log.Logger
}

func registerBudgetTools(srv *mcp.Server, store *budget.Store, logger *log.Logger) {
	tools := &budgetTools{store: store, logger: logger}

	mcp.AddTool[createMonthlyBudgetInput, any](srv, &mcp.Tool{
		Name:        "create_monthly_budget",
		Description: "Create a budget snapshot for the current or a past local month using explicit allocations or by carrying forward the latest earlier month. Future months are rejected.",
		Annotations: writableToolAnnotations(false, true),
	}, tools.createMonthlyBudget)

	mcp.AddTool[setBudgetsInput, any](srv, &mcp.Tool{
		Name:        "set_budgets",
		Description: "Replace or add allocations on an existing current or past month budget snapshot. Future months are rejected.",
		Annotations: writableToolAnnotations(true, false),
	}, tools.setBudgets)
}

func (t *budgetTools) createMonthlyBudget(ctx context.Context, _ *mcp.CallToolRequest, in createMonthlyBudgetInput) (*mcp.CallToolResult, any, error) {
	result, fields, err := t.store.Create(ctx, budget.CreateInput{
		Month:        in.Month,
		Budgets:      budgetAllocations(in.Budgets),
		CarryForward: in.CarryForward,
		Overrides:    budgetAllocations(in.Overrides),
	})
	if len(fields) != 0 {
		return toolError(invalidBudgetInputEnvelope(fields))
	}
	if err != nil {
		return t.mapBudgetError("create_monthly_budget", err)
	}

	return toolOK(createMonthlyBudgetOutput{
		OK:           true,
		Month:        result.Month,
		CreationMode: result.CreationMode,
		SourceMonth:  result.SourceMonth,
		TotalBudget:  result.TotalBudget,
		Budgets:      activeBudgets(result.Budgets),
	})
}

func (t *budgetTools) setBudgets(ctx context.Context, _ *mcp.CallToolRequest, in setBudgetsInput) (*mcp.CallToolResult, any, error) {
	result, fields, err := t.store.Set(ctx, in.Month, budgetAllocations(in.Budgets))
	if len(fields) != 0 {
		return toolError(invalidBudgetInputEnvelope(fields))
	}
	if err != nil {
		return t.mapBudgetError("set_budgets", err)
	}

	return toolOK(setBudgetsOutput{
		OK:      true,
		Month:   result.Month,
		Changes: setBudgetChanges(result.Changes),
	})
}

func (t *budgetTools) mapBudgetError(tool string, err error) (*mcp.CallToolResult, any, error) {
	var alreadyExists *budget.AlreadyExistsError
	if errors.As(err, &alreadyExists) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeMonthlyBudgetAlreadyExists,
			fmt.Sprintf("A monthly budget already exists for %s.", alreadyExists.Month),
			false,
			map[string]any{"month": alreadyExists.Month},
		)))
	}

	var sourceNotFound *budget.SourceNotFoundError
	if errors.As(err, &sourceNotFound) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeBudgetSourceNotFound,
			fmt.Sprintf("There is no earlier monthly budget to carry forward into %s.", sourceNotFound.Month),
			false,
			map[string]any{"month": sourceNotFound.Month},
		)))
	}

	var sourceEmpty *budget.SourceEmptyError
	if errors.As(err, &sourceEmpty) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeBudgetSourceEmpty,
			"The earlier monthly budget has no active categories to carry forward.",
			false,
			map[string]any{
				"month":        sourceEmpty.Month,
				"source_month": sourceEmpty.SourceMonth,
			},
		)))
	}

	var monthNotFound *budget.NotFoundError
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

	var categoryNotFound *budget.CategoryNotFoundError
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

	var dependency *rollover.DependencyConflictError
	if errors.As(err, &dependency) {
		return toolError(dependencyConflictEnvelope(dependency))
	}

	return t.internalError(tool, err)
}

func budgetAllocations(in []createMonthlyBudgetAllocation) []budget.Allocation {
	if in == nil {
		return nil
	}
	allocations := make([]budget.Allocation, len(in))
	for i, allocation := range in {
		allocations[i] = budget.Allocation{
			Category: allocation.Category,
			Amount:   allocation.Amount,
		}
	}
	return allocations
}

func setBudgetChanges(changes []budget.SetChange) []setBudgetsChange {
	out := make([]setBudgetsChange, 0, len(changes))
	for _, change := range changes {
		out = append(out, setBudgetsChange{
			Budget:  change.Budget,
			Created: change.Created,
		})
	}
	return out
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
