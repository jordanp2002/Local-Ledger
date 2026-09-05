package server

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/jordanp2002/local-finance-mcp/internal/savingsgoal"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type createSavingsGoalInput struct {
	Name         string  `json:"name"`
	AccountID    int64   `json:"account_id"`
	TargetAmount string  `json:"target_amount"`
	TargetDate   *string `json:"target_date,omitempty"`
	Note         *string `json:"note,omitempty"`
}

type createSavingsGoalOutput struct {
	OK   bool                 `json:"ok"`
	Goal contract.SavingsGoal `json:"goal"`
}

type updateSavingsGoalInput struct {
	ID           int64   `json:"id"`
	Name         *string `json:"name,omitempty"`
	TargetAmount *string `json:"target_amount,omitempty"`
	TargetDate   *string `json:"target_date,omitempty"`
	Note         *string `json:"note,omitempty"`
	AccountID    *int64  `json:"account_id,omitempty"`
}

type updateSavingsGoalOutput struct {
	OK      bool                 `json:"ok"`
	Goal    contract.SavingsGoal `json:"goal"`
	Changed bool                 `json:"changed"`
}

type listSavingsGoalsInput struct {
	Name          *string `json:"name,omitempty"`
	AccountID     *int64  `json:"account_id,omitempty"`
	Status        *string `json:"status,omitempty"`
	IncludeClosed bool    `json:"include_closed,omitempty"`
}

type listSavingsGoalsOutput struct {
	OK    bool                   `json:"ok"`
	Goals []contract.SavingsGoal `json:"goals"`
}

type savingsGoalStore interface {
	Create(ctx context.Context, in savingsgoal.CreateInput) (contract.SavingsGoal, []contract.FieldIssue, error)
	Update(ctx context.Context, in savingsgoal.UpdateInput) (savingsgoal.UpdateResult, []contract.FieldIssue, error)
	List(ctx context.Context, in savingsgoal.ListInput) ([]contract.SavingsGoal, []contract.FieldIssue, error)
}

type savingsGoalTools struct {
	store  savingsGoalStore
	logger *log.Logger
}

func registerSavingsGoalTools(srv *mcp.Server, store savingsGoalStore, logger *log.Logger) {
	tools := &savingsGoalTools{store: store, logger: logger}

	mcp.AddTool[createSavingsGoalInput, any](srv, &mcp.Tool{
		Name:        "create_savings_goal",
		Description: "Create an independent savings goal held in an asset account.",
		Annotations: writableToolAnnotations(true, true),
	}, tools.createSavingsGoal)

	mcp.AddTool[updateSavingsGoalInput, any](srv, &mcp.Tool{
		Name:        "update_savings_goal",
		Description: "Update mutable fields on an active savings goal.",
		Annotations: writableToolAnnotations(true, true),
	}, tools.updateSavingsGoal)

	mcp.AddTool[listSavingsGoalsInput, any](srv, &mcp.Tool{
		Name:        "list_savings_goals",
		Description: "List and filter savings goals with their derived progress.",
		Annotations: readOnlyToolAnnotations(),
	}, tools.listSavingsGoals)
}

func (t *savingsGoalTools) createSavingsGoal(ctx context.Context, _ *mcp.CallToolRequest, in createSavingsGoalInput) (*mcp.CallToolResult, any, error) {
	goal, fields, err := t.store.Create(ctx, savingsgoal.CreateInput{
		Name:         in.Name,
		AccountID:    in.AccountID,
		TargetAmount: in.TargetAmount,
		TargetDate:   in.TargetDate,
		Note:         in.Note,
	})
	if len(fields) > 0 {
		return toolError(invalidSavingsGoalInputEnvelope(fields))
	}
	if err != nil {
		return t.mapSavingsGoalError("create_savings_goal", err)
	}
	return toolOK(createSavingsGoalOutput{OK: true, Goal: goal})
}

func (t *savingsGoalTools) updateSavingsGoal(ctx context.Context, req *mcp.CallToolRequest, in updateSavingsGoalInput) (*mcp.CallToolResult, any, error) {
	var nameNull, targetAmountNull, accountIDNull bool
	var targetDatePresent, notePresent bool
	if req != nil {
		if args, err := rawToolArguments(req); err == nil {
			if raw, ok := args["name"]; ok && isJSONNull(raw) {
				nameNull = true
			}
			if raw, ok := args["target_amount"]; ok && isJSONNull(raw) {
				targetAmountNull = true
			}
			if raw, ok := args["account_id"]; ok && isJSONNull(raw) {
				accountIDNull = true
			}
			_, targetDatePresent = args["target_date"]
			_, notePresent = args["note"]
		}
	}

	result, fields, err := t.store.Update(ctx, savingsgoal.UpdateInput{
		ID:                in.ID,
		Name:              in.Name,
		NameNull:          nameNull,
		TargetAmount:      in.TargetAmount,
		TargetAmountNull:  targetAmountNull,
		TargetDate:        in.TargetDate,
		TargetDatePresent: targetDatePresent,
		Note:              in.Note,
		NotePresent:       notePresent,
		AccountID:         in.AccountID,
		AccountIDNull:     accountIDNull,
	})
	if len(fields) > 0 {
		return toolError(invalidSavingsGoalInputEnvelope(fields))
	}
	if err != nil {
		return t.mapSavingsGoalError("update_savings_goal", err)
	}
	return toolOK(updateSavingsGoalOutput{OK: true, Goal: result.Goal, Changed: result.Changed})
}

func (t *savingsGoalTools) listSavingsGoals(ctx context.Context, _ *mcp.CallToolRequest, in listSavingsGoalsInput) (*mcp.CallToolResult, any, error) {
	goals, fields, err := t.store.List(ctx, savingsgoal.ListInput{
		Name:          in.Name,
		AccountID:     in.AccountID,
		Status:        in.Status,
		IncludeClosed: in.IncludeClosed,
	})
	if len(fields) > 0 {
		return toolError(invalidSavingsGoalInputEnvelope(fields))
	}
	if err != nil {
		return t.mapSavingsGoalError("list_savings_goals", err)
	}
	if goals == nil {
		goals = []contract.SavingsGoal{}
	}
	return toolOK(listSavingsGoalsOutput{OK: true, Goals: goals})
}

func (t *savingsGoalTools) mapSavingsGoalError(tool string, err error) (*mcp.CallToolResult, any, error) {
	var notFound *savingsgoal.NotFoundError
	if errors.As(err, &notFound) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeSavingsGoalNotFound,
			fmt.Sprintf("Savings goal %d was not found.", notFound.ID),
			false,
			map[string]any{"id": notFound.ID},
		)))
	}
	var alreadyExists *savingsgoal.AlreadyExistsError
	if errors.As(err, &alreadyExists) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeSavingsGoalAlreadyExists,
			fmt.Sprintf("Savings goal %q already exists.", alreadyExists.Name),
			false,
			map[string]any{"name": alreadyExists.Name},
		)))
	}
	var closed *savingsgoal.ClosedError
	if errors.As(err, &closed) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeSavingsGoalClosed,
			fmt.Sprintf("Savings goal %d is %s and cannot be modified.", closed.ID, closed.Status),
			false,
			map[string]any{"id": closed.ID, "status": closed.Status},
		)))
	}
	var hasAllocations *savingsgoal.HasAllocationsError
	if errors.As(err, &hasAllocations) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeSavingsGoalHasAllocations,
			fmt.Sprintf("Savings goal %d has allocated funds and cannot change holding accounts.", hasAllocations.ID),
			false,
			map[string]any{"id": hasAllocations.ID, "current_amount": hasAllocations.CurrentAmount},
		)))
	}
	var acctNotFound *savingsgoal.AccountNotFoundError
	if errors.As(err, &acctNotFound) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeAccountNotFound,
			fmt.Sprintf("Account %d was not found.", acctNotFound.ID),
			false,
			map[string]any{"id": acctNotFound.ID},
		)))
	}
	if errors.Is(err, savingsgoal.ErrAccountInactive) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeAccountInactive,
			"The requested account is inactive.",
			false,
			map[string]any{},
		)))
	}
	if t.logger != nil {
		t.logger.Printf("%s: %v", tool, err)
	}
	return toolError(contract.NewInternalErrorEnvelope())
}

func invalidSavingsGoalInputEnvelope(fields []contract.FieldIssue) contract.ErrorEnvelope {
	return contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{"fields": fields},
	))
}
