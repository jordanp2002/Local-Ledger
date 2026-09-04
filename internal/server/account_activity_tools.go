package server

import (
	"context"
	"errors"
	"log"

	"github.com/jordanp2002/local-finance-mcp/internal/account"
	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type recordAccountActivityInput struct {
	AccountID      int64   `json:"account_id"`
	Type           string  `json:"type"`
	Amount         string  `json:"amount"`
	Date           string  `json:"date"`
	Note           *string `json:"note,omitempty"`
	IdempotencyKey string  `json:"idempotency_key"`
}

type recordAccountActivityOutput struct {
	OK               bool                  `json:"ok"`
	Entry            contract.AccountEntry `json:"entry"`
	Balance          string                `json:"balance"`
	IdempotentReplay bool                  `json:"idempotent_replay"`
}

type reconcileAccountBalanceInput struct {
	AccountID      int64   `json:"account_id"`
	Balance        string  `json:"balance"`
	Note           *string `json:"note,omitempty"`
	IdempotencyKey string  `json:"idempotency_key"`
}

type reconcileAccountBalanceOutput struct {
	OK               bool                   `json:"ok"`
	Entry            *contract.AccountEntry `json:"entry"`
	PreviousBalance  string                 `json:"previous_balance"`
	Adjustment       string                 `json:"adjustment"`
	Balance          string                 `json:"balance"`
	Changed          bool                   `json:"changed"`
	IdempotentReplay bool                   `json:"idempotent_replay"`
}

type listAccountActivityInput struct {
	AccountID int64   `json:"account_id"`
	StartDate *string `json:"start_date,omitempty"`
	EndDate   *string `json:"end_date,omitempty"`
	Kind      *string `json:"kind,omitempty"`
	Limit     *int64  `json:"limit,omitempty"`
	Offset    *int64  `json:"offset,omitempty"`
}

type listAccountActivityOutput struct {
	OK      bool                    `json:"ok"`
	Entries []contract.AccountEntry `json:"entries"`
	Page    contract.Page           `json:"page"`
}

type reverseAccountActivityInput struct {
	ID             int64   `json:"id"`
	Note           *string `json:"note,omitempty"`
	IdempotencyKey string  `json:"idempotency_key"`
}

type reverseAccountActivityOutput struct {
	OK               bool                  `json:"ok"`
	Entry            contract.AccountEntry `json:"entry"`
	Balance          string                `json:"balance"`
	Changed          bool                  `json:"changed"`
	IdempotentReplay bool                  `json:"idempotent_replay"`
}

func registerAccountActivityTools(srv *mcp.Server, store accountStore, logger *log.Logger) {
	tools := &accountTools{store: store, logger: logger}
	mcp.AddTool[recordAccountActivityInput, any](srv, &mcp.Tool{
		Name:        "record_account_activity",
		Description: "Record a local deposit or withdrawal for one asset account. Local only; never contacts a bank and never affects budgets.",
		Annotations: writableToolAnnotations(true, true),
	}, tools.recordAccountActivity)
	mcp.AddTool[reconcileAccountBalanceInput, any](srv, &mcp.Tool{
		Name:        "reconcile_account_balance",
		Description: "Reconcile one asset account to a reported local balance by recording the auditable delta. Local only; never contacts a bank.",
		Annotations: writableToolAnnotations(true, true),
	}, tools.reconcileAccountBalance)
	mcp.AddTool[listAccountActivityInput, any](srv, &mcp.Tool{
		Name:        "list_account_activity",
		Description: "List local account activity with running balances in stable date order.",
		Annotations: readOnlyToolAnnotations(),
	}, tools.listAccountActivity)
	mcp.AddTool[reverseAccountActivityInput, any](srv, &mcp.Tool{
		Name:        "reverse_account_activity",
		Description: "Reverse one deposit, withdrawal, or reconciliation entry with an offsetting local entry. Originals are never edited or deleted.",
		Annotations: writableToolAnnotations(true, true),
	}, tools.reverseAccountActivity)
}

func (t *accountTools) recordAccountActivity(ctx context.Context, req *mcp.CallToolRequest, in recordAccountActivityInput) (*mcp.CallToolResult, any, error) {
	notePresent := false
	if req != nil {
		if args, err := rawToolArguments(req); err == nil {
			_, notePresent = args["note"]
		}
	}
	result, fields, err := t.store.RecordActivity(ctx, account.RecordInput{
		AccountID: in.AccountID, Type: in.Type, Amount: in.Amount, Date: in.Date,
		Note: in.Note, NotePresent: notePresent, IdempotencyKey: in.IdempotencyKey,
	})
	if len(fields) > 0 {
		return toolError(invalidAccountInputEnvelope(fields))
	}
	if err != nil {
		return t.mapAccountError("record_account_activity", err)
	}
	return toolOK(recordAccountActivityOutput{OK: true, Entry: result.Entry, Balance: result.Balance, IdempotentReplay: result.IdempotentReplay})
}

func (t *accountTools) reconcileAccountBalance(ctx context.Context, req *mcp.CallToolRequest, in reconcileAccountBalanceInput) (*mcp.CallToolResult, any, error) {
	notePresent := false
	if req != nil {
		if args, err := rawToolArguments(req); err == nil {
			_, notePresent = args["note"]
		}
	}
	result, fields, err := t.store.ReconcileBalance(ctx, account.ReconcileInput{
		AccountID: in.AccountID, Balance: in.Balance,
		Note: in.Note, NotePresent: notePresent, IdempotencyKey: in.IdempotencyKey,
	})
	if len(fields) > 0 {
		return toolError(invalidAccountInputEnvelope(fields))
	}
	if err != nil {
		return t.mapAccountError("reconcile_account_balance", err)
	}
	return toolOK(reconcileAccountBalanceOutput{
		OK: true, Entry: result.Entry, PreviousBalance: result.PreviousBalance,
		Adjustment: result.Adjustment, Balance: result.Balance,
		Changed: result.Changed, IdempotentReplay: result.IdempotentReplay,
	})
}

func (t *accountTools) listAccountActivity(ctx context.Context, _ *mcp.CallToolRequest, in listAccountActivityInput) (*mcp.CallToolResult, any, error) {
	result, fields, err := t.store.ListActivity(ctx, account.ListActivityInput{
		AccountID: in.AccountID, StartDate: in.StartDate, EndDate: in.EndDate,
		Kind: in.Kind, Limit: in.Limit, Offset: in.Offset,
	})
	if len(fields) > 0 {
		return toolError(invalidAccountInputEnvelope(fields))
	}
	if err != nil {
		return t.mapAccountError("list_account_activity", err)
	}
	entries := result.Entries
	if entries == nil {
		entries = []contract.AccountEntry{}
	}
	return toolOK(listAccountActivityOutput{OK: true, Entries: entries, Page: result.Page})
}

func (t *accountTools) reverseAccountActivity(ctx context.Context, req *mcp.CallToolRequest, in reverseAccountActivityInput) (*mcp.CallToolResult, any, error) {
	notePresent := false
	if req != nil {
		if args, err := rawToolArguments(req); err == nil {
			_, notePresent = args["note"]
		}
	}
	result, fields, err := t.store.ReverseActivity(ctx, account.ReverseInput{
		EntryID: in.ID, Note: in.Note, NotePresent: notePresent, IdempotencyKey: in.IdempotencyKey,
	})
	if len(fields) > 0 {
		return toolError(invalidAccountInputEnvelope(fields))
	}
	if err != nil {
		return t.mapAccountError("reverse_account_activity", err)
	}
	return toolOK(reverseAccountActivityOutput{OK: true, Entry: result.Entry, Balance: result.Balance, Changed: result.Changed, IdempotentReplay: result.IdempotentReplay})
}

func mapAccountActivityError(err error) (contract.ErrorCode, string, map[string]any, bool) {
	var entryNotFound *account.EntryNotFoundError
	if errors.As(err, &entryNotFound) {
		return contract.ErrorCodeAccountEntryNotFound, "", map[string]any{"id": entryNotFound.ID}, true
	}
	var notReversible *account.EntryNotReversibleError
	if errors.As(err, &notReversible) {
		return contract.ErrorCodeAccountEntryNotReversible, "", map[string]any{"id": notReversible.ID}, true
	}
	var conflict *account.IdempotencyConflictError
	if errors.As(err, &conflict) {
		return contract.ErrorCodeIdempotencyConflict, "The idempotency key has already been used for a different account request.", map[string]any{"idempotency_key": conflict.IdempotencyKey, "reason": conflict.Reason}, true
	}
	return "", "", nil, false
}
