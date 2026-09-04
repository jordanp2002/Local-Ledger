package server

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/jordanp2002/local-finance-mcp/internal/account"
	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type createAccountInput struct {
	Name           string  `json:"name"`
	Type           string  `json:"type"`
	OpeningBalance string  `json:"opening_balance"`
	Note           *string `json:"note,omitempty"`
}

type createAccountOutput struct {
	OK          bool             `json:"ok"`
	Account     contract.Account `json:"account"`
	Created     bool             `json:"created"`
	Reactivated bool             `json:"reactivated"`
}

type updateAccountInput struct {
	ID   int64   `json:"id"`
	Name *string `json:"name,omitempty"`
	Note *string `json:"note,omitempty"`
}

type updateAccountOutput struct {
	OK      bool             `json:"ok"`
	Account contract.Account `json:"account"`
	Changed bool             `json:"changed"`
}

type listAccountsInput struct {
	Name            *string `json:"name,omitempty"`
	Type            *string `json:"type,omitempty"`
	IncludeInactive bool    `json:"include_inactive,omitempty"`
}

type listAccountsOutput struct {
	OK       bool               `json:"ok"`
	Accounts []contract.Account `json:"accounts"`
}

type disableAccountInput struct {
	ID int64 `json:"id"`
}

type disableAccountOutput struct {
	OK      bool             `json:"ok"`
	Account contract.Account `json:"account"`
	Changed bool             `json:"changed"`
}

type accountStore interface {
	Create(ctx context.Context, in account.CreateInput) (account.CreateResult, []contract.FieldIssue, error)
	Update(ctx context.Context, in account.UpdateInput) (account.UpdateResult, []contract.FieldIssue, error)
	List(ctx context.Context, in account.ListInput) ([]contract.Account, []contract.FieldIssue, error)
	Disable(ctx context.Context, id int64) (account.DisableResult, []contract.FieldIssue, error)
	RecordActivity(ctx context.Context, in account.RecordInput) (account.RecordResult, []contract.FieldIssue, error)
	ReconcileBalance(ctx context.Context, in account.ReconcileInput) (account.ReconcileResult, []contract.FieldIssue, error)
	ListActivity(ctx context.Context, in account.ListActivityInput) (account.ListActivityResult, []contract.FieldIssue, error)
	ReverseActivity(ctx context.Context, in account.ReverseInput) (account.ReverseResult, []contract.FieldIssue, error)
}

type accountTools struct {
	store  accountStore
	logger *log.Logger
}

func registerAccountTools(srv *mcp.Server, store accountStore, logger *log.Logger) {
	tools := &accountTools{store: store, logger: logger}

	mcp.AddTool[createAccountInput, any](srv, &mcp.Tool{
		Name:        "create_account",
		Description: "Create a local asset account with a reported opening balance, or reactivate an inactive account with the same name. Local only; never contacts a bank.",
		Annotations: writableToolAnnotations(true, true),
	}, tools.createAccount)

	mcp.AddTool[updateAccountInput, any](srv, &mcp.Tool{
		Name:        "update_account",
		Description: "Rename a local asset account or set its note. Type and balances never change here.",
		Annotations: writableToolAnnotations(true, true),
	}, tools.updateAccount)

	mcp.AddTool[listAccountsInput, any](srv, &mcp.Tool{
		Name:        "list_accounts",
		Description: "List local asset accounts with reported balances.",
		Annotations: readOnlyToolAnnotations(),
	}, tools.listAccounts)

	mcp.AddTool[disableAccountInput, any](srv, &mcp.Tool{
		Name:        "disable_account",
		Description: "Retire a local asset account with exactly zero balance. History is preserved, never deleted.",
		Annotations: writableToolAnnotations(true, true),
	}, tools.disableAccount)

	registerAccountActivityTools(srv, store, tools.logger)
}

func (t *accountTools) createAccount(ctx context.Context, req *mcp.CallToolRequest, in createAccountInput) (*mcp.CallToolResult, any, error) {
	notePresent := false
	if req != nil {
		if args, err := rawToolArguments(req); err == nil {
			_, notePresent = args["note"]
		}
	}
	result, fields, err := t.store.Create(ctx, account.CreateInput{
		Name:           in.Name,
		Type:           in.Type,
		OpeningBalance: in.OpeningBalance,
		Note:           in.Note,
		NotePresent:    notePresent,
	})
	if len(fields) > 0 {
		return toolError(invalidAccountInputEnvelope(fields))
	}
	if err != nil {
		return t.mapAccountError("create_account", err)
	}
	return toolOK(createAccountOutput{OK: true, Account: result.Account, Created: result.Created, Reactivated: result.Reactivated})
}

func (t *accountTools) updateAccount(ctx context.Context, req *mcp.CallToolRequest, in updateAccountInput) (*mcp.CallToolResult, any, error) {
	nameNull, notePresent := false, false
	if req != nil {
		if args, err := rawToolArguments(req); err == nil {
			if raw, ok := args["name"]; ok && isJSONNull(raw) {
				nameNull = true
			}
			_, notePresent = args["note"]
		}
	}
	// encoding/json collapses omitted and explicit null for *string; explicit
	// null on note means clear, detected via raw args above.
	result, fields, err := t.store.Update(ctx, account.UpdateInput{
		ID:          in.ID,
		Name:        in.Name,
		NameNull:    nameNull,
		Note:        in.Note,
		NotePresent: notePresent,
	})
	if len(fields) > 0 {
		return toolError(invalidAccountInputEnvelope(fields))
	}
	if err != nil {
		return t.mapAccountError("update_account", err)
	}
	return toolOK(updateAccountOutput{OK: true, Account: result.Account, Changed: result.Changed})
}

func (t *accountTools) listAccounts(ctx context.Context, _ *mcp.CallToolRequest, in listAccountsInput) (*mcp.CallToolResult, any, error) {
	accounts, fields, err := t.store.List(ctx, account.ListInput{
		Name:            in.Name,
		Type:            in.Type,
		IncludeInactive: in.IncludeInactive,
	})
	if len(fields) > 0 {
		return toolError(invalidAccountInputEnvelope(fields))
	}
	if err != nil {
		return t.mapAccountError("list_accounts", err)
	}
	if accounts == nil {
		accounts = []contract.Account{}
	}
	return toolOK(listAccountsOutput{OK: true, Accounts: accounts})
}

func (t *accountTools) disableAccount(ctx context.Context, _ *mcp.CallToolRequest, in disableAccountInput) (*mcp.CallToolResult, any, error) {
	result, fields, err := t.store.Disable(ctx, in.ID)
	if len(fields) > 0 {
		return toolError(invalidAccountInputEnvelope(fields))
	}
	if err != nil {
		return t.mapAccountError("disable_account", err)
	}
	return toolOK(disableAccountOutput{OK: true, Account: result.Account, Changed: result.Changed})
}

func (t *accountTools) mapAccountError(tool string, err error) (*mcp.CallToolResult, any, error) {
	if code, message, details, ok := mapAccountActivityError(err); ok {
		return toolError(contract.NewErrorEnvelope(contract.NewError(code, message, false, details)))
	}
	var exists *account.AlreadyExistsError
	if errors.As(err, &exists) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeAccountAlreadyExists,
			fmt.Sprintf("Account '%s' already exists.", exists.Account.Name),
			false,
			map[string]any{"account": exists.Account},
		)))
	}
	var notFound *account.NotFoundError
	if errors.As(err, &notFound) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeAccountNotFound,
			fmt.Sprintf("Account %d was not found.", notFound.ID),
			false,
			map[string]any{"id": notFound.ID},
		)))
	}
	var balance *account.BalanceNotZeroError
	if errors.As(err, &balance) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeAccountBalanceNotZero,
			fmt.Sprintf("Account '%s' balance must be zero.", balance.Account.Name),
			false,
			map[string]any{"account": balance.Account, "current_balance": balance.Account.CurrentBalance},
		)))
	}
	if errors.Is(err, account.ErrInactive) {
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

func invalidAccountInputEnvelope(fields []contract.FieldIssue) contract.ErrorEnvelope {
	return contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{"fields": fields},
	))
}
