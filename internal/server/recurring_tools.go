package server

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/recurring"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type createRecurringTransactionInput struct {
	Merchant   string  `json:"merchant"`
	Amount     string  `json:"amount"`
	Category   string  `json:"category"`
	DayOfMonth int64   `json:"day_of_month"`
	Note       *string `json:"note,omitempty"`
}

type createRecurringTransactionOutput struct {
	OK                   bool                          `json:"ok"`
	RecurringTransaction contract.RecurringTransaction `json:"recurring_transaction"`
}

type listRecurringTransactionsInput struct{}

type listRecurringTransactionsOutput struct {
	OK                    bool                            `json:"ok"`
	RecurringTransactions []contract.RecurringTransaction `json:"recurring_transactions"`
}

type disableRecurringTransactionInput struct {
	ID int64 `json:"id"`
}

type disableRecurringTransactionOutput struct {
	OK                   bool                          `json:"ok"`
	RecurringTransaction contract.RecurringTransaction `json:"recurring_transaction"`
	Changed              bool                          `json:"changed"`
}

type updateRecurringTransactionInput struct {
	ID         int64   `json:"id"`
	Merchant   *string `json:"merchant,omitempty"`
	Amount     *string `json:"amount,omitempty"`
	Category   *string `json:"category,omitempty"`
	DayOfMonth *int64  `json:"day_of_month,omitempty"`
	Note       *string `json:"note,omitempty"`
}

type updateRecurringTransactionOutput struct {
	OK                   bool                          `json:"ok"`
	RecurringTransaction contract.RecurringTransaction `json:"recurring_transaction"`
	Changed              bool                          `json:"changed"`
}

type enableRecurringTransactionInput struct {
	ID int64 `json:"id"`
}

type enableRecurringTransactionOutput struct {
	OK                   bool                          `json:"ok"`
	RecurringTransaction contract.RecurringTransaction `json:"recurring_transaction"`
	Changed              bool                          `json:"changed"`
}

type previewDueTransactionsInput struct{}

type previewDueTransactionsOutput struct {
	OK              bool                             `json:"ok"`
	AsOfDate        string                           `json:"as_of_date"`
	Month           string                           `json:"month"`
	TotalAmount     string                           `json:"total_amount"`
	DueTransactions []contract.DueTransaction        `json:"due_transactions"`
	Blocked         []contract.BlockedDueTransaction `json:"blocked"`
}

type materializeDueTransactionsInput struct{}

type materializeDueTransactionsOutput struct {
	OK             bool                     `json:"ok"`
	AsOfDate       string                   `json:"as_of_date"`
	Month          string                   `json:"month"`
	Created        int64                    `json:"created"`
	TotalAmount    string                   `json:"total_amount"`
	Transactions   []contract.Transaction   `json:"transactions"`
	RolloverOffers []contract.RolloverOffer `json:"rollover_offers"`
}

type previewUpcomingTransactionsInput struct{}

type previewUpcomingTransactionsOutput struct {
	OK                   bool                             `json:"ok"`
	AsOfDate             string                           `json:"as_of_date"`
	Month                string                           `json:"month"`
	TotalAmount          string                           `json:"total_amount"`
	UpcomingTransactions []contract.UpcomingTransaction   `json:"upcoming_transactions"`
	Blocked              []contract.BlockedDueTransaction `json:"blocked"`
}

type recurringTools struct {
	store  *recurring.Store
	logger *log.Logger
}

func registerRecurringTools(srv *mcp.Server, store *recurring.Store, logger *log.Logger) {
	tools := &recurringTools{store: store, logger: logger}

	mcp.AddTool[createRecurringTransactionInput, any](srv, &mcp.Tool{
		Name:        "create_recurring_transaction",
		Description: "Save a fixed monthly expense template without recording financial activity.",
		Annotations: writableToolAnnotations(true, false),
	}, tools.createRecurringTransaction)

	mcp.AddTool[listRecurringTransactionsInput, any](srv, &mcp.Tool{
		Name:        "list_recurring_transactions",
		Description: "List all recurring expense templates ordered by status, day of month, merchant, and ID.",
		Annotations: readOnlyToolAnnotations(),
	}, tools.listRecurringTransactions)

	mcp.AddTool[disableRecurringTransactionInput, any](srv, &mcp.Tool{
		Name:        "disable_recurring_transaction",
		Description: "Disable a recurring expense template without affecting past transactions.",
		Annotations: writableToolAnnotations(true, true),
	}, tools.disableRecurringTransaction)

	mcp.AddTool[updateRecurringTransactionInput, any](srv, &mcp.Tool{
		Name:        "update_recurring_transaction",
		Description: "Patch a recurring expense template without changing generated transactions or run history.",
		Annotations: writableToolAnnotations(true, true),
	}, tools.updateRecurringTransaction)

	mcp.AddTool[enableRecurringTransactionInput, any](srv, &mcp.Tool{
		Name:        "enable_recurring_transaction",
		Description: "Re-enable a disabled recurring expense template without affecting past transactions.",
		Annotations: writableToolAnnotations(true, true),
	}, tools.enableRecurringTransaction)

	mcp.AddTool[previewDueTransactionsInput, any](srv, &mcp.Tool{
		Name:        "preview_due_transactions",
		Description: "Preview recurring expenses due this month without writing data; show this result to the user and ask for confirmation before materializing.",
		Annotations: readOnlyToolAnnotations(),
	}, tools.previewDueTransactions)

	mcp.AddTool[previewUpcomingTransactionsInput, any](srv, &mcp.Tool{
		Name:        "preview_upcoming_transactions",
		Description: "Preview every active recurring expense not yet run in the current month without writing data.",
		Annotations: readOnlyToolAnnotations(),
	}, tools.previewUpcomingTransactions)

	mcp.AddTool[materializeDueTransactionsInput, any](srv, &mcp.Tool{
		Name:        "materialize_due_transactions",
		Description: "After user confirmation, atomically create ordinary expense transactions for the current due set; retries are safe and create no duplicates. If rollover_offers is returned, show the offer and ask whether to create the explicit one-month rollover.",
		Annotations: writableToolAnnotations(true, true),
	}, tools.materializeDueTransactions)
}

func (t *recurringTools) createRecurringTransaction(ctx context.Context, _ *mcp.CallToolRequest, in createRecurringTransactionInput) (*mcp.CallToolResult, any, error) {
	res, fields, err := t.store.Create(ctx, recurring.CreateInput{
		Merchant:   in.Merchant,
		Amount:     in.Amount,
		Category:   in.Category,
		DayOfMonth: in.DayOfMonth,
		Note:       in.Note,
	})
	if len(fields) != 0 {
		return toolError(invalidInputEnvelope(fields))
	}
	if err != nil {
		return t.mapRecurringError("create_recurring_transaction", err)
	}

	return toolOK(createRecurringTransactionOutput{
		OK:                   true,
		RecurringTransaction: res.RecurringTransaction,
	})
}

func (t *recurringTools) listRecurringTransactions(ctx context.Context, _ *mcp.CallToolRequest, _ listRecurringTransactionsInput) (*mcp.CallToolResult, any, error) {
	items, err := t.store.List(ctx)
	if err != nil {
		return internalToolError(t.logger, "list_recurring_transactions", err)
	}
	if items == nil {
		items = []contract.RecurringTransaction{}
	}

	return toolOK(listRecurringTransactionsOutput{
		OK:                    true,
		RecurringTransactions: items,
	})
}

func (t *recurringTools) disableRecurringTransaction(ctx context.Context, _ *mcp.CallToolRequest, in disableRecurringTransactionInput) (*mcp.CallToolResult, any, error) {
	res, fields, err := t.store.Disable(ctx, in.ID)
	if len(fields) != 0 {
		return toolError(invalidInputEnvelope(fields))
	}
	if err != nil {
		return t.mapRecurringError("disable_recurring_transaction", err)
	}

	return toolOK(disableRecurringTransactionOutput{
		OK:                   true,
		RecurringTransaction: res.RecurringTransaction,
		Changed:              res.Changed,
	})
}

func (t *recurringTools) updateRecurringTransaction(ctx context.Context, req *mcp.CallToolRequest, in updateRecurringTransactionInput) (*mcp.CallToolResult, any, error) {
	updateIn, err := updateRecurringInputFromRequest(req, in)
	if err != nil {
		return internalToolError(t.logger, "update_recurring_transaction", err)
	}
	res, fields, err := t.store.Update(ctx, updateIn)
	if len(fields) != 0 {
		return toolError(invalidInputEnvelope(fields))
	}
	if err != nil {
		return t.mapRecurringError("update_recurring_transaction", err)
	}
	return toolOK(updateRecurringTransactionOutput{
		OK:                   true,
		RecurringTransaction: res.RecurringTransaction,
		Changed:              res.Changed,
	})
}

func (t *recurringTools) enableRecurringTransaction(ctx context.Context, _ *mcp.CallToolRequest, in enableRecurringTransactionInput) (*mcp.CallToolResult, any, error) {
	res, fields, err := t.store.Enable(ctx, in.ID)
	if len(fields) != 0 {
		return toolError(invalidInputEnvelope(fields))
	}
	if err != nil {
		return t.mapRecurringError("enable_recurring_transaction", err)
	}
	return toolOK(enableRecurringTransactionOutput{
		OK:                   true,
		RecurringTransaction: res.RecurringTransaction,
		Changed:              res.Changed,
	})
}

func (t *recurringTools) previewDueTransactions(ctx context.Context, _ *mcp.CallToolRequest, _ previewDueTransactionsInput) (*mcp.CallToolResult, any, error) {
	res, err := t.store.PreviewDue(ctx)
	if err != nil {
		return internalToolError(t.logger, "preview_due_transactions", err)
	}
	if res.DueTransactions == nil {
		res.DueTransactions = []contract.DueTransaction{}
	}
	if res.Blocked == nil {
		res.Blocked = []contract.BlockedDueTransaction{}
	}

	return toolOK(previewDueTransactionsOutput{
		OK:              true,
		AsOfDate:        res.AsOfDate,
		Month:           res.Month,
		TotalAmount:     res.TotalAmount,
		DueTransactions: res.DueTransactions,
		Blocked:         res.Blocked,
	})
}

func (t *recurringTools) previewUpcomingTransactions(ctx context.Context, _ *mcp.CallToolRequest, _ previewUpcomingTransactionsInput) (*mcp.CallToolResult, any, error) {
	res, err := t.store.PreviewUpcoming(ctx)
	if err != nil {
		return internalToolError(t.logger, "preview_upcoming_transactions", err)
	}
	if res.UpcomingTransactions == nil {
		res.UpcomingTransactions = []contract.UpcomingTransaction{}
	}
	if res.Blocked == nil {
		res.Blocked = []contract.BlockedDueTransaction{}
	}
	return toolOK(previewUpcomingTransactionsOutput{
		OK:                   true,
		AsOfDate:             res.AsOfDate,
		Month:                res.Month,
		TotalAmount:          res.TotalAmount,
		UpcomingTransactions: res.UpcomingTransactions,
		Blocked:              res.Blocked,
	})
}

func (t *recurringTools) materializeDueTransactions(ctx context.Context, _ *mcp.CallToolRequest, _ materializeDueTransactionsInput) (*mcp.CallToolResult, any, error) {
	res, err := t.store.MaterializeDue(ctx)
	if err != nil {
		return t.mapRecurringError("materialize_due_transactions", err)
	}
	if res.Transactions == nil {
		res.Transactions = []contract.Transaction{}
	}

	return toolOK(materializeDueTransactionsOutput{
		OK:             true,
		AsOfDate:       res.AsOfDate,
		Month:          res.Month,
		Created:        res.Created,
		TotalAmount:    res.TotalAmount,
		Transactions:   res.Transactions,
		RolloverOffers: nonNilRolloverOffers(res.RolloverOffers),
	})
}

func (t *recurringTools) mapRecurringError(tool string, err error) (*mcp.CallToolResult, any, error) {
	var notFound *recurring.NotFoundError
	if errors.As(err, &notFound) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeRecurringTransactionNotFound,
			fmt.Sprintf("Recurring transaction %d was not found.", notFound.ID),
			false,
			map[string]any{"id": notFound.ID},
		)))
	}

	var categoryNotFound *recurring.CategoryNotFoundError
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

	var categoryInactive *recurring.CategoryInactiveError
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

	var recurringCategoryInactive *recurring.RecurringCategoryInactiveError
	if errors.As(err, &recurringCategoryInactive) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeRecurringCategoryInactive,
			fmt.Sprintf("Recurring transaction %d for '%s' references inactive category '%s'.", recurringCategoryInactive.RecurringTransactionID, recurringCategoryInactive.Merchant, recurringCategoryInactive.Category),
			false,
			map[string]any{
				"recurring_transaction_id": recurringCategoryInactive.RecurringTransactionID,
				"merchant":                 recurringCategoryInactive.Merchant,
				"category":                 recurringCategoryInactive.Category,
				"due_date":                 recurringCategoryInactive.DueDate,
			},
		)))
	}

	return internalToolError(t.logger, tool, err)
}

func updateRecurringInputFromRequest(req *mcp.CallToolRequest, in updateRecurringTransactionInput) (recurring.UpdateInput, error) {
	args, err := rawToolArguments(req)
	if err != nil {
		return recurring.UpdateInput{}, err
	}
	out := recurring.UpdateInput{ID: in.ID}
	if raw, ok := args["merchant"]; ok {
		if isJSONNull(raw) {
			out.MerchantNull = true
		} else {
			out.Merchant = in.Merchant
		}
	}
	if raw, ok := args["amount"]; ok {
		if isJSONNull(raw) {
			out.AmountNull = true
		} else {
			out.Amount = in.Amount
		}
	}
	if raw, ok := args["category"]; ok {
		if isJSONNull(raw) {
			out.CategoryNull = true
		} else {
			out.Category = in.Category
		}
	}
	if raw, ok := args["day_of_month"]; ok {
		if isJSONNull(raw) {
			out.DayOfMonthNull = true
		} else {
			out.DayOfMonth = in.DayOfMonth
		}
	}
	if raw, ok := args["note"]; ok {
		out.Note.Present = true
		if !isJSONNull(raw) {
			out.Note.Value = in.Note
		}
	}
	return out, nil
}
