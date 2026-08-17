package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/jordanp2002/local-finance-mcp/internal/transaction"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type addTransactionInput struct {
	Amount   string  `json:"amount"`
	Merchant string  `json:"merchant"`
	Category *string `json:"category,omitempty"`
	Date     *string `json:"date,omitempty"`
	Note     *string `json:"note,omitempty"`
}

type addTransactionOutput struct {
	OK                    bool                 `json:"ok"`
	Transaction           contract.Transaction `json:"transaction"`
	CategorySource        string               `json:"category_source"`
	MerchantMappingAction string               `json:"merchant_mapping_action"`
}

type updateTransactionInput struct {
	ID       int64   `json:"id"`
	Amount   *string `json:"amount,omitempty"`
	Merchant *string `json:"merchant,omitempty"`
	Category *string `json:"category,omitempty"`
	Date     *string `json:"date,omitempty"`
	Note     *string `json:"note,omitempty"`
}

type updateTransactionOutput struct {
	OK          bool                 `json:"ok"`
	Transaction contract.Transaction `json:"transaction"`
}

type removeTransactionInput struct {
	ID int64 `json:"id"`
}

type removeTransactionOutput struct {
	OK                 bool                 `json:"ok"`
	RemovedTransaction contract.Transaction `json:"removed_transaction"`
}

type listTransactionsInput struct {
	StartDate *string `json:"start_date,omitempty"`
	EndDate   *string `json:"end_date,omitempty"`
	Category  *string `json:"category,omitempty"`
	Limit     *int64  `json:"limit,omitempty"`
	Offset    *int64  `json:"offset,omitempty"`
}

type listTransactionsOutput struct {
	OK           bool                   `json:"ok"`
	Transactions []contract.Transaction `json:"transactions"`
	Page         contract.Page          `json:"page"`
}

type transactionTools struct {
	store  *transaction.Store
	logger *log.Logger
}

func registerTransactionTools(srv *mcp.Server, store *transaction.Store, logger *log.Logger) {
	tools := &transactionTools{store: store, logger: logger}

	mcp.AddTool[addTransactionInput, any](srv, &mcp.Tool{
		Name:        "add_transaction",
		Description: "Record one expense and apply exact merchant-default mapping rules atomically.",
		Annotations: writableToolAnnotations(true, false),
	}, tools.addTransaction)

	mcp.AddTool[updateTransactionInput, any](srv, &mcp.Tool{
		Name:        "update_transaction",
		Description: "Patch an existing expense without changing merchant defaults or budgets.",
		Annotations: writableToolAnnotations(true, false),
	}, tools.updateTransaction)

	mcp.AddTool[removeTransactionInput, any](srv, &mcp.Tool{
		Name:        "remove_transaction",
		Description: "Permanently remove one expense by ID and return the deleted record.",
		Annotations: writableToolAnnotations(true, true),
	}, tools.removeTransaction)

	mcp.AddTool[listTransactionsInput, any](srv, &mcp.Tool{
		Name:        "list_transactions",
		Description: "List purchases with optional inclusive date bounds, category filter, and pagination.",
		Annotations: readOnlyToolAnnotations(),
	}, tools.listTransactions)
}

func (t *transactionTools) addTransaction(ctx context.Context, _ *mcp.CallToolRequest, in addTransactionInput) (*mcp.CallToolResult, any, error) {
	result, fields, err := t.store.Add(ctx, transaction.AddInput{
		Amount:   in.Amount,
		Merchant: in.Merchant,
		Category: in.Category,
		Date:     in.Date,
		Note:     in.Note,
	})
	if len(fields) != 0 {
		return toolError(invalidTransactionInputEnvelope(fields))
	}
	if err != nil {
		return t.mapTransactionError("add_transaction", err)
	}

	return toolOK(addTransactionOutput{
		OK:                    true,
		Transaction:           result.Transaction,
		CategorySource:        result.CategorySource,
		MerchantMappingAction: result.MerchantMappingAction,
	})
}

func (t *transactionTools) updateTransaction(ctx context.Context, req *mcp.CallToolRequest, in updateTransactionInput) (*mcp.CallToolResult, any, error) {
	updateIn, err := updateInputFromRequest(req, in)
	if err != nil {
		return t.internalError("update_transaction", err)
	}

	result, fields, err := t.store.Update(ctx, updateIn)
	if len(fields) != 0 {
		return toolError(invalidTransactionInputEnvelope(fields))
	}
	if err != nil {
		return t.mapTransactionError("update_transaction", err)
	}

	return toolOK(updateTransactionOutput{
		OK:          true,
		Transaction: result.Transaction,
	})
}

func (t *transactionTools) removeTransaction(ctx context.Context, _ *mcp.CallToolRequest, in removeTransactionInput) (*mcp.CallToolResult, any, error) {
	removed, fields, err := t.store.Remove(ctx, in.ID)
	if len(fields) != 0 {
		return toolError(invalidTransactionInputEnvelope(fields))
	}
	if err != nil {
		return t.mapTransactionError("remove_transaction", err)
	}

	return toolOK(removeTransactionOutput{
		OK:                 true,
		RemovedTransaction: removed,
	})
}

func (t *transactionTools) listTransactions(ctx context.Context, _ *mcp.CallToolRequest, in listTransactionsInput) (*mcp.CallToolResult, any, error) {
	result, fields, err := t.store.List(ctx, transaction.ListInput{
		StartDate: in.StartDate,
		EndDate:   in.EndDate,
		Category:  in.Category,
		Limit:     in.Limit,
		Offset:    in.Offset,
	})
	if len(fields) != 0 {
		return toolError(invalidTransactionInputEnvelope(fields))
	}
	if err != nil {
		return t.mapTransactionError("list_transactions", err)
	}

	transactions := result.Transactions
	if transactions == nil {
		transactions = []contract.Transaction{}
	}
	return toolOK(listTransactionsOutput{
		OK:           true,
		Transactions: transactions,
		Page:         result.Page,
	})
}

func (t *transactionTools) mapTransactionError(tool string, err error) (*mcp.CallToolResult, any, error) {
	var notFound *transaction.TransactionNotFoundError
	if errors.As(err, &notFound) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeTransactionNotFound,
			fmt.Sprintf("Transaction %d was not found.", notFound.ID),
			false,
			map[string]any{"id": notFound.ID},
		)))
	}

	var categoryNotFound *transaction.CategoryNotFoundError
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

	var inactive *transaction.CategoryInactiveError
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

	var required *transaction.MerchantCategoryRequiredError
	if errors.As(err, &required) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeMerchantCategoryRequired,
			fmt.Sprintf("Merchant '%s' has no exact category mapping.", required.Merchant),
			false,
			map[string]any{"merchant": required.Merchant},
		)))
	}

	var mappingInactive *transaction.MerchantCategoryInactiveError
	if errors.As(err, &mappingInactive) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeMerchantCategoryInactive,
			fmt.Sprintf("Merchant '%s' maps to inactive category '%s'.", mappingInactive.KnownMerchant.Merchant, mappingInactive.KnownMerchant.Category),
			false,
			map[string]any{
				"known_merchant":    mappingInactive.KnownMerchant,
				"active_categories": activeCategories(mappingInactive.ActiveCategories),
			},
		)))
	}

	return t.internalError(tool, err)
}

func invalidTransactionInputEnvelope(fields []contract.FieldIssue) contract.ErrorEnvelope {
	return contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{"fields": fields},
	))
}

func (t *transactionTools) internalError(tool string, err error) (*mcp.CallToolResult, any, error) {
	if t.logger != nil {
		t.logger.Printf("%s: %v", tool, err)
	}
	return toolError(contract.NewInternalErrorEnvelope())
}

// updateInputFromRequest maps typed MCP input plus raw argument presence.
// encoding/json collapses omitted and JSON null into a nil *string, so
// explicit null on amount/merchant/category/date is passed through as a
// *Null flag for store validation.
func updateInputFromRequest(req *mcp.CallToolRequest, in updateTransactionInput) (transaction.UpdateInput, error) {
	args, err := rawToolArguments(req)
	if err != nil {
		return transaction.UpdateInput{}, err
	}

	out := transaction.UpdateInput{ID: in.ID}
	if raw, ok := args["amount"]; ok {
		if isJSONNull(raw) {
			out.AmountNull = true
		} else {
			out.Amount = in.Amount
		}
	}
	if raw, ok := args["merchant"]; ok {
		if isJSONNull(raw) {
			out.MerchantNull = true
		} else {
			out.Merchant = in.Merchant
		}
	}
	if raw, ok := args["category"]; ok {
		if isJSONNull(raw) {
			out.CategoryNull = true
		} else {
			out.Category = in.Category
		}
	}
	if raw, ok := args["date"]; ok {
		if isJSONNull(raw) {
			out.DateNull = true
		} else {
			out.Date = in.Date
		}
	}
	if raw, ok := args["note"]; ok {
		if isJSONNull(raw) {
			out.Note = transaction.NotePatch{Present: true}
		} else {
			out.Note = transaction.NotePatch{Present: true, Value: in.Note}
		}
	}
	return out, nil
}

func rawToolArguments(req *mcp.CallToolRequest) (map[string]json.RawMessage, error) {
	if req == nil || req.Params == nil || len(req.Params.Arguments) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
		return nil, err
	}
	if args == nil {
		return map[string]json.RawMessage{}, nil
	}
	return args, nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
