package server

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/jordanp2002/local-finance-mcp/internal/rollover"
	"github.com/jordanp2002/local-finance-mcp/internal/sinkingfund"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type createBudgetRolloverInput struct {
	SourceMonth         string  `json:"source_month"`
	Category            string  `json:"category"`
	Amount              string  `json:"amount"`
	SourceTransactionID *int64  `json:"source_transaction_id,omitempty"`
	Note                *string `json:"note,omitempty"`
}

type createBudgetRolloverOutput struct {
	OK       bool                    `json:"ok"`
	Rollover contract.BudgetRollover `json:"rollover"`
}

type listBudgetRolloversInput struct {
	SourceMonth *string `json:"source_month,omitempty"`
	TargetMonth *string `json:"target_month,omitempty"`
	Category    *string `json:"category,omitempty"`
	Limit       *int64  `json:"limit,omitempty"`
	Offset      *int64  `json:"offset,omitempty"`
}

type listBudgetRolloversOutput struct {
	OK        bool                      `json:"ok"`
	Rollovers []contract.BudgetRollover `json:"rollovers"`
	Page      contract.Page             `json:"page"`
}

type removeBudgetRolloverInput struct {
	ID int64 `json:"id"`
}

type removeBudgetRolloverOutput struct {
	OK              bool                    `json:"ok"`
	RemovedRollover contract.BudgetRollover `json:"removed_rollover"`
}

type rolloverTools struct {
	store  *rollover.Store
	logger *log.Logger
}

func registerRolloverTools(srv *mcp.Server, store *rollover.Store, logger *log.Logger) {
	tools := &rolloverTools{store: store, logger: logger}

	mcp.AddTool[createBudgetRolloverInput, any](srv, &mcp.Tool{
		Name:        "create_budget_rollover",
		Description: "Create an explicitly authorized one-month budget rollover from a category's uncovered overspending. The target month is derived as the immediate next month. Use after showing a transaction rollover offer or when the user explicitly requests the rollover; this tool never runs automatically.",
		Annotations: writableToolAnnotations(true, false),
	}, tools.createBudgetRollover)

	mcp.AddTool[listBudgetRolloversInput, any](srv, &mcp.Tool{
		Name:        "list_budget_rollovers",
		Description: "List explicit one-month budget rollovers with optional source month, target month, category, and pagination filters. Use this audit path to explain why a target month's available category budget is lower.",
		Annotations: readOnlyToolAnnotations(),
	}, tools.listBudgetRollovers)

	mcp.AddTool[removeBudgetRolloverInput, any](srv, &mcp.Tool{
		Name:        "remove_budget_rollover",
		Description: "Remove one explicit budget rollover by ID and return the removed record. Dependent later rollovers must be removed first; removal is intentionally not idempotent.",
		Annotations: writableToolAnnotations(true, false),
	}, tools.removeBudgetRollover)
}

func (t *rolloverTools) createBudgetRollover(ctx context.Context, _ *mcp.CallToolRequest, in createBudgetRolloverInput) (*mcp.CallToolResult, any, error) {
	result, fields, err := t.store.Create(ctx, rollover.CreateInput{
		SourceMonth:         in.SourceMonth,
		Category:            in.Category,
		Amount:              in.Amount,
		SourceTransactionID: in.SourceTransactionID,
		Note:                in.Note,
	})
	if len(fields) != 0 {
		return toolError(invalidRolloverInputEnvelope(fields))
	}
	if err != nil {
		return t.mapRolloverError("create_budget_rollover", err)
	}
	return toolOK(createBudgetRolloverOutput{OK: true, Rollover: result.Rollover})
}

func (t *rolloverTools) listBudgetRollovers(ctx context.Context, _ *mcp.CallToolRequest, in listBudgetRolloversInput) (*mcp.CallToolResult, any, error) {
	result, fields, err := t.store.List(ctx, rollover.ListInput{
		SourceMonth: in.SourceMonth,
		TargetMonth: in.TargetMonth,
		Category:    in.Category,
		Limit:       in.Limit,
		Offset:      in.Offset,
	})
	if len(fields) != 0 {
		return toolError(invalidRolloverInputEnvelope(fields))
	}
	if err != nil {
		return t.mapRolloverError("list_budget_rollovers", err)
	}
	return toolOK(listBudgetRolloversOutput{
		OK:        true,
		Rollovers: nonNilRollovers(result.Rollovers),
		Page:      result.Page,
	})
}

func (t *rolloverTools) removeBudgetRollover(ctx context.Context, _ *mcp.CallToolRequest, in removeBudgetRolloverInput) (*mcp.CallToolResult, any, error) {
	removed, fields, err := t.store.Remove(ctx, in.ID)
	if len(fields) != 0 {
		return toolError(invalidRolloverInputEnvelope(fields))
	}
	if err != nil {
		return t.mapRolloverError("remove_budget_rollover", err)
	}
	return toolOK(removeBudgetRolloverOutput{OK: true, RemovedRollover: removed})
}

func (t *rolloverTools) mapRolloverError(tool string, err error) (*mcp.CallToolResult, any, error) {
	var notEligible *rollover.NotEligibleError
	if errors.As(err, &notEligible) {
		details := map[string]any{
			"source_month":        notEligible.SourceMonth,
			"target_month":        notEligible.TargetMonth,
			"category":            notEligible.Category,
			"requested_amount":    formatRolloverDetail(notEligible.RequestedAmount),
			"source_overspending": formatRolloverDetail(notEligible.SourceOverspending),
			"already_rolled":      formatRolloverDetail(notEligible.AlreadyRolled),
			"eligible_rollover":   formatRolloverDetail(notEligible.EligibleRollover),
		}
		if notEligible.Reason != "" {
			details["reason"] = notEligible.Reason
		}
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeBudgetRolloverNotEligible,
			"The requested budget rollover is not eligible.",
			false,
			details,
		)))
	}

	var notFound *rollover.NotFoundError
	if errors.As(err, &notFound) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeBudgetRolloverNotFound,
			fmt.Sprintf("Budget rollover %d was not found.", notFound.ID),
			false,
			map[string]any{"id": notFound.ID},
		)))
	}

	var dependency *rollover.DependencyConflictError
	if errors.As(err, &dependency) {
		return toolError(dependencyConflictEnvelope(dependency))
	}

	var categoryNotFound *rollover.CategoryNotFoundError
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

	var categoryInactive *rollover.CategoryInactiveError
	if errors.As(err, &categoryInactive) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeCategoryInactive,
			fmt.Sprintf("Category '%s' is inactive.", categoryInactive.Category.Name),
			false,
			map[string]any{
				"category":          categoryInactive.Category,
				"active_categories": activeCategories(categoryInactive.ActiveCategories),
			},
		)))
	}

	var sourceTransaction *rollover.SourceTransactionNotFoundError
	if errors.As(err, &sourceTransaction) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeTransactionNotFound,
			fmt.Sprintf("Transaction %d was not found.", sourceTransaction.ID),
			false,
			map[string]any{"id": sourceTransaction.ID},
		)))
	}

	var fundConflict *sinkingfund.RolloverConflictError
	if errors.As(err, &fundConflict) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeSinkingFundRolloverConflict,
			"The rollover overlaps a sinking-fund period.",
			false,
			map[string]any{"category_id": fundConflict.CategoryID, "category": fundConflict.Category, "months": fundConflict.Months},
		)))
	}

	return t.internalError(tool, err)
}

func dependencyConflictEnvelope(dependency *rollover.DependencyConflictError) contract.ErrorEnvelope {
	return contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeBudgetRolloverDependencyConflict,
		"The budget rollover conflicts with a dependent adjustment.",
		false,
		map[string]any{
			"rollover_ids": dependency.RolloverIDs,
			"conflicts":    rolloverConflicts(dependency.Conflicts),
		},
	))
}

func invalidRolloverInputEnvelope(fields []contract.FieldIssue) contract.ErrorEnvelope {
	return contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{"fields": fields},
	))
}

func nonNilRollovers(rollovers []contract.BudgetRollover) []contract.BudgetRollover {
	if rollovers == nil {
		return []contract.BudgetRollover{}
	}
	return rollovers
}

func formatRolloverDetail(value int64) string {
	formatted, err := contract.FormatSignedAmount(value)
	if err != nil {
		return ""
	}
	return formatted
}

func rolloverConflicts(conflicts []rollover.Conflict) []map[string]any {
	result := make([]map[string]any, 0, len(conflicts))
	for _, conflict := range conflicts {
		result = append(result, map[string]any{
			"source_month":        conflict.SourceMonth,
			"category_id":         conflict.CategoryID,
			"rollover_ids":        conflict.RolloverIDs,
			"source_overspending": formatRolloverDetail(conflict.SourceOverspending),
			"outgoing_rollover":   formatRolloverDetail(conflict.OutgoingRollover),
			"eligible_rollover":   formatRolloverDetail(conflict.EligibleRollover),
		})
	}
	return result
}

func (t *rolloverTools) internalError(tool string, err error) (*mcp.CallToolResult, any, error) {
	if t.logger != nil {
		t.logger.Printf("%s: %v", tool, err)
	}
	return toolError(contract.NewInternalErrorEnvelope())
}
