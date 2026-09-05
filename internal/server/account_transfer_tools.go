package server

import (
	"context"
	"errors"
	"log"

	"github.com/jordanp2002/Local-Ledger/internal/account"
	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type transferBetweenAccountsInput struct {
	SourceAccountID      int64   `json:"source_account_id"`
	DestinationAccountID int64   `json:"destination_account_id"`
	Amount               string  `json:"amount"`
	Date                 string  `json:"date"`
	Note                 *string `json:"note,omitempty"`
	IdempotencyKey       string  `json:"idempotency_key"`
}

type transferBetweenAccountsOutput struct {
	OK                 bool                     `json:"ok"`
	Transfer           contract.AccountTransfer `json:"transfer"`
	SourceBalance      string                   `json:"source_balance"`
	DestinationBalance string                   `json:"destination_balance"`
	IdempotentReplay   bool                     `json:"idempotent_replay"`
	ExecutedExternally bool                     `json:"executed_externally"`
}

type listAccountTransfersInput struct {
	AccountID            *int64  `json:"account_id,omitempty"`
	SourceAccountID      *int64  `json:"source_account_id,omitempty"`
	DestinationAccountID *int64  `json:"destination_account_id,omitempty"`
	StartDate            *string `json:"start_date,omitempty"`
	EndDate              *string `json:"end_date,omitempty"`
	Status               *string `json:"status,omitempty"`
	Limit                *int64  `json:"limit,omitempty"`
	Offset               *int64  `json:"offset,omitempty"`
}

type listAccountTransfersOutput struct {
	OK        bool                       `json:"ok"`
	Transfers []contract.AccountTransfer `json:"transfers"`
	Page      contract.Page              `json:"page"`
}

type reverseAccountTransferInput struct {
	ID             int64   `json:"id"`
	Note           *string `json:"note,omitempty"`
	IdempotencyKey string  `json:"idempotency_key"`
}

type reverseAccountTransferOutput struct {
	OK                 bool                     `json:"ok"`
	Transfer           contract.AccountTransfer `json:"transfer"`
	SourceBalance      string                   `json:"source_balance"`
	DestinationBalance string                   `json:"destination_balance"`
	Changed            bool                     `json:"changed"`
	IdempotentReplay   bool                     `json:"idempotent_replay"`
	ExecutedExternally bool                     `json:"executed_externally"`
}

func registerAccountTransferTools(srv *mcp.Server, store accountStore, logger *log.Logger) {
	tools := &accountTools{store: store, logger: logger}
	mcp.AddTool[transferBetweenAccountsInput, any](srv, &mcp.Tool{
		Name:        "transfer_between_accounts",
		Description: "Record a completed local transfer between two asset accounts. Local only; never contacts a bank or executes an external transfer.",
		Annotations: writableToolAnnotations(true, true),
	}, tools.transferBetweenAccounts)
	mcp.AddTool[listAccountTransfersInput, any](srv, &mcp.Tool{
		Name:        "list_account_transfers",
		Description: "List completed local account transfers and their canonical account identities.",
		Annotations: readOnlyToolAnnotations(),
	}, tools.listAccountTransfers)
	mcp.AddTool[reverseAccountTransferInput, any](srv, &mcp.Tool{
		Name:        "reverse_account_transfer",
		Description: "Reverse one local account transfer with an inverse transfer. Local only; never contacts a bank or executes an external transfer.",
		Annotations: writableToolAnnotations(true, true),
	}, tools.reverseAccountTransfer)
}

func (t *accountTools) transferBetweenAccounts(ctx context.Context, req *mcp.CallToolRequest, in transferBetweenAccountsInput) (*mcp.CallToolResult, any, error) {
	notePresent := false
	if req != nil {
		if args, err := rawToolArguments(req); err == nil {
			_, notePresent = args["note"]
		}
	}
	result, fields, err := t.store.TransferBetweenAccounts(ctx, account.TransferInput{
		SourceAccountID: in.SourceAccountID, DestinationAccountID: in.DestinationAccountID,
		Amount: in.Amount, Date: in.Date, Note: in.Note, NotePresent: notePresent,
		IdempotencyKey: in.IdempotencyKey,
	})
	if len(fields) > 0 {
		return toolError(invalidInputEnvelope(fields))
	}
	if err != nil {
		return t.mapAccountError("transfer_between_accounts", err)
	}
	return toolOK(transferBetweenAccountsOutput{
		OK: true, Transfer: result.Transfer, SourceBalance: result.SourceBalance,
		DestinationBalance: result.DestinationBalance, IdempotentReplay: result.IdempotentReplay,
		ExecutedExternally: false,
	})
}

func (t *accountTools) listAccountTransfers(ctx context.Context, _ *mcp.CallToolRequest, in listAccountTransfersInput) (*mcp.CallToolResult, any, error) {
	result, fields, err := t.store.ListTransfers(ctx, account.ListTransfersInput{
		AccountID: in.AccountID, SourceAccountID: in.SourceAccountID,
		DestinationAccountID: in.DestinationAccountID, StartDate: in.StartDate,
		EndDate: in.EndDate, Status: in.Status, Limit: in.Limit, Offset: in.Offset,
	})
	if len(fields) > 0 {
		return toolError(invalidInputEnvelope(fields))
	}
	if err != nil {
		return t.mapAccountError("list_account_transfers", err)
	}
	transfers := result.Transfers
	if transfers == nil {
		transfers = []contract.AccountTransfer{}
	}
	return toolOK(listAccountTransfersOutput{OK: true, Transfers: transfers, Page: result.Page})
}

func (t *accountTools) reverseAccountTransfer(ctx context.Context, req *mcp.CallToolRequest, in reverseAccountTransferInput) (*mcp.CallToolResult, any, error) {
	notePresent := false
	if req != nil {
		if args, err := rawToolArguments(req); err == nil {
			_, notePresent = args["note"]
		}
	}
	result, fields, err := t.store.ReverseAccountTransfer(ctx, account.ReverseTransferInput{
		TransferID: in.ID, Note: in.Note, NotePresent: notePresent, IdempotencyKey: in.IdempotencyKey,
	})
	if len(fields) > 0 {
		return toolError(invalidInputEnvelope(fields))
	}
	if err != nil {
		return t.mapAccountError("reverse_account_transfer", err)
	}
	return toolOK(reverseAccountTransferOutput{
		OK: true, Transfer: result.Transfer, SourceBalance: result.SourceBalance,
		DestinationBalance: result.DestinationBalance, Changed: result.Changed,
		IdempotentReplay: result.IdempotentReplay, ExecutedExternally: false,
	})
}

func mapAccountTransferError(err error) (contract.ErrorCode, string, map[string]any, bool) {
	var notFound *account.TransferNotFoundError
	if errors.As(err, &notFound) {
		return contract.ErrorCodeAccountTransferNotFound, "", map[string]any{"id": notFound.ID}, true
	}
	var alreadyReversed *account.TransferAlreadyReversedError
	if errors.As(err, &alreadyReversed) {
		return contract.ErrorCodeAccountTransferAlreadyReversed, "", map[string]any{"id": alreadyReversed.ID}, true
	}
	var dependency *account.TransferDependencyConflictError
	if errors.As(err, &dependency) {
		return contract.ErrorCodeTransferDependencyConflict,
			"This transfer has a goal funding dependency; use the goal-funding reversal path.",
			map[string]any{"id": dependency.ID}, true
	}
	return "", "", nil, false
}
