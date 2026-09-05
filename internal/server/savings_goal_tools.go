package server

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/savingsgoal"
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

type goalAmountInput struct {
	GoalID         int64   `json:"goal_id"`
	Amount         string  `json:"amount"`
	Date           string  `json:"date"`
	Note           *string `json:"note,omitempty"`
	IdempotencyKey string  `json:"idempotency_key"`
}

type fundSavingsGoalInput struct {
	GoalID          int64   `json:"goal_id"`
	SourceAccountID int64   `json:"source_account_id"`
	Amount          string  `json:"amount"`
	Date            string  `json:"date"`
	Note            *string `json:"note,omitempty"`
	IdempotencyKey  string  `json:"idempotency_key"`
}

type goalIDInput struct {
	GoalID int64 `json:"goal_id"`
}
type reverseFundingInput struct {
	EntryID        int64   `json:"entry_id"`
	Note           *string `json:"note,omitempty"`
	IdempotencyKey string  `json:"idempotency_key"`
}
type savingsOverviewInput struct {
	IncludeInactiveAccounts bool `json:"include_inactive_accounts,omitempty"`
	IncludeClosedGoals      bool `json:"include_closed_goals,omitempty"`
}

type savingsGoalStore interface {
	Create(ctx context.Context, in savingsgoal.CreateInput) (contract.SavingsGoal, []contract.FieldIssue, error)
	Update(ctx context.Context, in savingsgoal.UpdateInput) (savingsgoal.UpdateResult, []contract.FieldIssue, error)
	List(ctx context.Context, in savingsgoal.ListInput) ([]contract.SavingsGoal, []contract.FieldIssue, error)
	Allocate(context.Context, savingsgoal.AllocationInput) (savingsgoal.GoalMutationResult, []contract.FieldIssue, error)
	Release(context.Context, savingsgoal.AllocationInput) (savingsgoal.GoalMutationResult, []contract.FieldIssue, error)
	Fund(context.Context, savingsgoal.FundingInput) (savingsgoal.FundingResult, []contract.FieldIssue, error)
	ReverseFunding(context.Context, savingsgoal.ReverseFundingInput) (savingsgoal.FundingResult, []contract.FieldIssue, error)
	Complete(context.Context, int64) (savingsgoal.LifecycleResult, []contract.FieldIssue, error)
	Cancel(context.Context, int64) (savingsgoal.LifecycleResult, []contract.FieldIssue, error)
	Overview(context.Context, bool, bool) (contract.SavingsOverview, error)
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
	mcp.AddTool[goalAmountInput, any](srv, &mcp.Tool{Name: "allocate_to_savings_goal", Description: "Reserve money already present in a savings goal's holding account.", Annotations: writableToolAnnotations(true, true)}, tools.allocate)
	mcp.AddTool[goalAmountInput, any](srv, &mcp.Tool{Name: "release_savings_goal_funds", Description: "Release reserved savings goal funds without changing the account balance.", Annotations: writableToolAnnotations(true, true)}, tools.release)
	mcp.AddTool[fundSavingsGoalInput, any](srv, &mcp.Tool{Name: "fund_savings_goal", Description: "Atomically transfer money into a savings goal's holding account and reserve it.", Annotations: writableToolAnnotations(true, true)}, tools.fund)
	mcp.AddTool[reverseFundingInput, any](srv, &mcp.Tool{Name: "reverse_savings_goal_funding", Description: "Atomically reverse a transfer-funded savings goal entry and its account transfer.", Annotations: writableToolAnnotations(true, true)}, tools.reverseFunding)
	mcp.AddTool[goalIDInput, any](srv, &mcp.Tool{Name: "complete_savings_goal", Description: "Mark a savings goal complete after its target is reached.", Annotations: writableToolAnnotations(false, true)}, tools.complete)
	mcp.AddTool[goalIDInput, any](srv, &mcp.Tool{Name: "cancel_savings_goal", Description: "Cancel an active savings goal and release its allocation.", Annotations: writableToolAnnotations(false, true)}, tools.cancel)
	mcp.AddTool[savingsOverviewInput, any](srv, &mcp.Tool{Name: "get_savings_overview", Description: "Summarize account balances, savings allocations, and allocation shortfalls.", Annotations: readOnlyToolAnnotations()}, tools.overview)
}

func (t *savingsGoalTools) allocate(ctx context.Context, _ *mcp.CallToolRequest, in goalAmountInput) (*mcp.CallToolResult, any, error) {
	r, f, e := t.store.Allocate(ctx, savingsgoal.AllocationInput{GoalID: in.GoalID, Amount: in.Amount, Date: in.Date, Note: in.Note, IdempotencyKey: in.IdempotencyKey})
	return t.goalMutation("allocate_to_savings_goal", r, f, e)
}
func (t *savingsGoalTools) release(ctx context.Context, _ *mcp.CallToolRequest, in goalAmountInput) (*mcp.CallToolResult, any, error) {
	r, f, e := t.store.Release(ctx, savingsgoal.AllocationInput{GoalID: in.GoalID, Amount: in.Amount, Date: in.Date, Note: in.Note, IdempotencyKey: in.IdempotencyKey})
	return t.goalMutation("release_savings_goal_funds", r, f, e)
}
func (t *savingsGoalTools) goalMutation(tool string, r savingsgoal.GoalMutationResult, f []contract.FieldIssue, e error) (*mcp.CallToolResult, any, error) {
	if len(f) > 0 {
		return toolError(invalidInputEnvelope(f))
	}
	if e != nil {
		return t.mapSavingsGoalError(tool, e)
	}
	return toolOK(struct {
		OK               bool                              `json:"ok"`
		Goal             contract.SavingsGoal              `json:"goal"`
		Account          contract.SavingsAccountAllocation `json:"account"`
		Changed          bool                              `json:"changed"`
		IdempotentReplay bool                              `json:"idempotent_replay"`
	}{true, r.Goal, r.Account, r.Changed, r.IdempotentReplay})
}
func (t *savingsGoalTools) fund(ctx context.Context, _ *mcp.CallToolRequest, in fundSavingsGoalInput) (*mcp.CallToolResult, any, error) {
	r, f, e := t.store.Fund(ctx, savingsgoal.FundingInput{GoalID: in.GoalID, SourceAccountID: in.SourceAccountID, Amount: in.Amount, Date: in.Date, Note: in.Note, IdempotencyKey: in.IdempotencyKey})
	if len(f) > 0 {
		return toolError(invalidInputEnvelope(f))
	}
	if e != nil {
		return t.mapSavingsGoalError("fund_savings_goal", e)
	}
	return toolOK(struct {
		OK bool `json:"ok"`
		savingsgoal.FundingResult
	}{true, r})
}
func (t *savingsGoalTools) reverseFunding(ctx context.Context, _ *mcp.CallToolRequest, in reverseFundingInput) (*mcp.CallToolResult, any, error) {
	r, f, e := t.store.ReverseFunding(ctx, savingsgoal.ReverseFundingInput{EntryID: in.EntryID, Note: in.Note, IdempotencyKey: in.IdempotencyKey})
	if len(f) > 0 {
		return toolError(invalidInputEnvelope(f))
	}
	if e != nil {
		return t.mapSavingsGoalError("reverse_savings_goal_funding", e)
	}
	return toolOK(struct {
		OK bool `json:"ok"`
		savingsgoal.FundingResult
	}{true, r})
}
func (t *savingsGoalTools) complete(ctx context.Context, _ *mcp.CallToolRequest, in goalIDInput) (*mcp.CallToolResult, any, error) {
	return t.lifecycle(ctx, in.GoalID, true)
}
func (t *savingsGoalTools) cancel(ctx context.Context, _ *mcp.CallToolRequest, in goalIDInput) (*mcp.CallToolResult, any, error) {
	return t.lifecycle(ctx, in.GoalID, false)
}
func (t *savingsGoalTools) lifecycle(ctx context.Context, id int64, complete bool) (*mcp.CallToolResult, any, error) {
	var r savingsgoal.LifecycleResult
	var f []contract.FieldIssue
	var e error
	tool := "cancel_savings_goal"
	if complete {
		tool = "complete_savings_goal"
		r, f, e = t.store.Complete(ctx, id)
	} else {
		r, f, e = t.store.Cancel(ctx, id)
	}
	if len(f) > 0 {
		return toolError(invalidInputEnvelope(f))
	}
	if e != nil {
		return t.mapSavingsGoalError(tool, e)
	}
	return toolOK(struct {
		OK bool `json:"ok"`
		savingsgoal.LifecycleResult
	}{true, r})
}
func (t *savingsGoalTools) overview(ctx context.Context, _ *mcp.CallToolRequest, in savingsOverviewInput) (*mcp.CallToolResult, any, error) {
	r, e := t.store.Overview(ctx, in.IncludeInactiveAccounts, in.IncludeClosedGoals)
	if e != nil {
		return t.mapSavingsGoalError("get_savings_overview", e)
	}
	return toolOK(struct {
		OK       bool                     `json:"ok"`
		Overview contract.SavingsOverview `json:"overview"`
	}{true, r})
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
		return toolError(invalidInputEnvelope(fields))
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
		return toolError(invalidInputEnvelope(fields))
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
		return toolError(invalidInputEnvelope(fields))
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
	var insufficient *savingsgoal.InsufficientBalanceError
	if errors.As(err, &insufficient) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(contract.ErrorCodeGoalAllocationInsufficientBalance, "", false, map[string]any{"account_balance": insufficient.Balance, "allocated_total": insufficient.Allocated, "unallocated_balance": insufficient.Unallocated, "requested_amount": insufficient.Requested})))
	}
	var exceeds *savingsgoal.ExceedsCurrentError
	if errors.As(err, &exceeds) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(contract.ErrorCodeGoalAllocationExceedsCurrent, "", false, map[string]any{"current_amount": exceeds.Current, "requested_amount": exceeds.Requested})))
	}
	var target *savingsgoal.TargetNotReachedError
	if errors.As(err, &target) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(contract.ErrorCodeSavingsGoalTargetNotReached, "", false, map[string]any{"target_amount": target.Target, "current_amount": target.Current, "remaining_amount": target.Remaining})))
	}
	var idem *savingsgoal.IdempotencyConflictError
	if errors.As(err, &idem) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(contract.ErrorCodeIdempotencyConflict, "", false, map[string]any{"idempotency_key": idem.Key})))
	}
	var fundingMissing *savingsgoal.FundingNotFoundError
	if errors.As(err, &fundingMissing) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(contract.ErrorCodeSavingsGoalFundingNotFound, "", false, map[string]any{"entry_id": fundingMissing.EntryID})))
	}
	var fundingConflict *savingsgoal.FundingDependencyConflictError
	if errors.As(err, &fundingConflict) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(contract.ErrorCodeSavingsGoalFundingDependencyConflict, "", false, map[string]any{"entry_id": fundingConflict.EntryID})))
	}
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
	return internalToolError(t.logger, tool, err)
}
