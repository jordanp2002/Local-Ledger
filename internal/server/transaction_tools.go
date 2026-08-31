package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/jordanp2002/local-finance-mcp/internal/rollover"
	"github.com/jordanp2002/local-finance-mcp/internal/transaction"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const addTransactionsDescription = "Atomically record a confirmed batch of structured expenses using exact merchant-default rules. Submit only user-confirmed expense rows — not images, files, credits, payments, pending transactions, or unreadable lines. Resolve every uncategorized merchant with the user before calling. Each row requires amount, merchant, and a YYYY-MM-DD date; dates are never defaulted to today. The first occurrence of a new merchant in the array must include category unless an exact mapping already exists. `idempotency_key` is required. Reuse the exact same key and payload if retrying this confirmed batch; do not mint a new key for a retry. The server does not detect duplicate purchases. The call is all-or-nothing: any invalid or uncategorized row writes nothing. If `rollover_offers` is returned, show the non-mutating offer and ask whether the user wants to create the explicit one-month rollover."

type addTransactionInput struct {
	Amount         string  `json:"amount"`
	Merchant       string  `json:"merchant"`
	Category       *string `json:"category,omitempty"`
	Date           *string `json:"date,omitempty"`
	Note           *string `json:"note,omitempty"`
	IdempotencyKey *string `json:"idempotency_key,omitempty"`
}

type addTransactionOutput struct {
	OK                    bool                     `json:"ok"`
	Transaction           contract.Transaction     `json:"transaction"`
	CategorySource        string                   `json:"category_source"`
	MerchantMappingAction string                   `json:"merchant_mapping_action"`
	IdempotentReplay      bool                     `json:"idempotent_replay"`
	RolloverOffers        []contract.RolloverOffer `json:"rollover_offers"`
}

type addSplitTransactionInput struct {
	Merchant       string                    `json:"merchant"`
	Date           *string                   `json:"date,omitempty"`
	Note           *string                   `json:"note,omitempty"`
	Allocations    []addSplitAllocationInput `json:"allocations"`
	IdempotencyKey *string                   `json:"idempotency_key,omitempty"`
}

type addSplitAllocationInput struct {
	Category string `json:"category"`
	Amount   string `json:"amount"`
}

type addTransactionsInput struct {
	IdempotencyKey string                    `json:"idempotency_key"`
	Transactions   []addTransactionsRowInput `json:"transactions"`
}

type addTransactionsRowInput struct {
	Amount   string  `json:"amount"`
	Merchant string  `json:"merchant"`
	Category *string `json:"category,omitempty"`
	Date     string  `json:"date"`
	Note     *string `json:"note,omitempty"`
}

type addTransactionsRowOutput struct {
	Transaction           contract.Transaction `json:"transaction"`
	CategorySource        string               `json:"category_source"`
	MerchantMappingAction string               `json:"merchant_mapping_action"`
}

type addTransactionsOutput struct {
	OK               bool                       `json:"ok"`
	IdempotencyKey   string                     `json:"idempotency_key"`
	IdempotentReplay bool                       `json:"idempotent_replay"`
	Count            int                        `json:"count"`
	TotalAmount      string                     `json:"total_amount"`
	Transactions     []addTransactionsRowOutput `json:"transactions"`
	RolloverOffers   []contract.RolloverOffer   `json:"rollover_offers"`
}

type updateTransactionInput struct {
	ID          int64                    `json:"id"`
	Amount      *string                  `json:"amount,omitempty"`
	Merchant    *string                  `json:"merchant,omitempty"`
	Category    *string                  `json:"category,omitempty"`
	Date        *string                  `json:"date,omitempty"`
	Note        *string                  `json:"note,omitempty"`
	Allocations *[]updateAllocationInput `json:"allocations,omitempty"`
}

type updateAllocationInput struct {
	Category string `json:"category"`
	Amount   string `json:"amount"`
}

type updateTransactionOutput struct {
	OK             bool                     `json:"ok"`
	Transaction    contract.Transaction     `json:"transaction"`
	RolloverOffers []contract.RolloverOffer `json:"rollover_offers"`
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
	Merchant  *string `json:"merchant,omitempty"`
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
		Description: "Record one expense and apply exact merchant-default mapping rules atomically. An optional idempotency_key makes a successful retry return the original transaction instead of creating a duplicate. If rollover_offers is returned, show the offer and ask whether to create the explicit one-month rollover.",
		Annotations: writableToolAnnotations(true, false),
	}, tools.addTransaction)

	mcp.AddTool[addSplitTransactionInput, any](srv, &mcp.Tool{
		Name:        "add_split_transaction",
		Description: "Record one confirmed purchase across two or more active categories as one transaction. Allocation amounts must be positive and their checked sum is the transaction amount. An optional idempotency_key makes an exact retry return the original purchase; split purchases never create or replace a merchant default. If rollover_offers is returned, show the offer and ask whether to create the explicit one-month rollover.",
		Annotations: writableToolAnnotations(true, false),
	}, tools.addSplitTransaction)

	mcp.AddTool[addTransactionsInput, any](srv, &mcp.Tool{
		Name:        "add_transactions",
		Description: addTransactionsDescription,
		Annotations: writableToolAnnotations(true, true),
	}, tools.addTransactions)

	mcp.AddTool[updateTransactionInput, any](srv, &mcp.Tool{
		Name:        "update_transaction",
		Description: "Patch an existing expense without changing merchant defaults or budgets. If rollover_offers is returned, show the offer and ask whether to create the explicit one-month rollover.",
		Annotations: writableToolAnnotations(true, false),
	}, tools.updateTransaction)

	mcp.AddTool[removeTransactionInput, any](srv, &mcp.Tool{
		Name:        "remove_transaction",
		Description: "Permanently remove one expense by ID and return the deleted record.",
		Annotations: writableToolAnnotations(true, true),
	}, tools.removeTransaction)

	mcp.AddTool[listTransactionsInput, any](srv, &mcp.Tool{
		Name:        "list_transactions",
		Description: "List purchases with optional inclusive date bounds, category filter, exact merchant filter, and pagination.",
		Annotations: readOnlyToolAnnotations(),
	}, tools.listTransactions)
}

func (t *transactionTools) addTransaction(ctx context.Context, _ *mcp.CallToolRequest, in addTransactionInput) (*mcp.CallToolResult, any, error) {
	result, fields, err := t.store.Add(ctx, transaction.AddInput{
		Amount:         in.Amount,
		Merchant:       in.Merchant,
		Category:       in.Category,
		Date:           in.Date,
		Note:           in.Note,
		IdempotencyKey: in.IdempotencyKey,
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
		IdempotentReplay:      result.IdempotentReplay,
		RolloverOffers:        nonNilRolloverOffers(result.RolloverOffers),
	})
}

func (t *transactionTools) addSplitTransaction(ctx context.Context, _ *mcp.CallToolRequest, in addSplitTransactionInput) (*mcp.CallToolResult, any, error) {
	allocations := make([]transaction.AllocationInput, len(in.Allocations))
	for i, allocation := range in.Allocations {
		allocations[i] = transaction.AllocationInput{Category: allocation.Category, Amount: allocation.Amount}
	}
	result, fields, err := t.store.AddSplit(ctx, transaction.AddSplitInput{
		Merchant:       in.Merchant,
		Date:           in.Date,
		Note:           in.Note,
		Allocations:    allocations,
		IdempotencyKey: in.IdempotencyKey,
	})
	if len(fields) != 0 {
		return toolError(invalidTransactionInputEnvelope(fields))
	}
	if err != nil {
		return t.mapTransactionError("add_split_transaction", err)
	}
	return toolOK(addTransactionOutput{
		OK:                    true,
		Transaction:           result.Transaction,
		CategorySource:        result.CategorySource,
		MerchantMappingAction: result.MerchantMappingAction,
		IdempotentReplay:      result.IdempotentReplay,
		RolloverOffers:        nonNilRolloverOffers(result.RolloverOffers),
	})
}

func (t *transactionTools) addTransactions(ctx context.Context, _ *mcp.CallToolRequest, in addTransactionsInput) (*mcp.CallToolResult, any, error) {
	rows := make([]transaction.BatchRow, len(in.Transactions))
	for i, row := range in.Transactions {
		rows[i] = transaction.BatchRow{
			Amount:   row.Amount,
			Merchant: row.Merchant,
			Category: row.Category,
			Date:     row.Date,
			Note:     row.Note,
		}
	}

	result, fields, err := t.store.AddBatch(ctx, transaction.AddBatchInput{
		IdempotencyKey: in.IdempotencyKey,
		Transactions:   rows,
	})
	if len(fields) != 0 {
		return toolError(invalidTransactionInputEnvelope(fields))
	}
	if err != nil {
		return t.mapTransactionError("add_transactions", err)
	}

	totalAmount, err := contract.FormatAmount(result.TotalHundredths)
	if err != nil {
		return t.internalError("add_transactions", err)
	}

	items := make([]addTransactionsRowOutput, len(result.Transactions))
	for i, item := range result.Transactions {
		items[i] = addTransactionsRowOutput{
			Transaction:           item.Transaction,
			CategorySource:        item.CategorySource,
			MerchantMappingAction: item.MerchantMappingAction,
		}
	}
	return toolOK(addTransactionsOutput{
		OK:               true,
		IdempotencyKey:   result.IdempotencyKey,
		IdempotentReplay: result.IdempotentReplay,
		Count:            len(items),
		TotalAmount:      totalAmount,
		Transactions:     items,
		RolloverOffers:   nonNilRolloverOffers(result.RolloverOffers),
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
		OK:             true,
		Transaction:    result.Transaction,
		RolloverOffers: nonNilRolloverOffers(result.RolloverOffers),
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
		Merchant:  in.Merchant,
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
	var rowErr *transaction.BatchRowError
	if errors.As(err, &rowErr) {
		res, payload, callErr := t.mapTransactionError(tool, rowErr.Err)
		envelope, ok := payload.(contract.ErrorEnvelope)
		if !ok || envelope.Error.Code == contract.ErrorCodeInternalError {
			return res, payload, callErr
		}
		return toolError(withDetailIndex(envelope, rowErr.Index))
	}

	var conflict *transaction.IdempotencyConflictError
	if errors.As(err, &conflict) {
		return toolError(idempotencyConflictEnvelope(tool, conflict))
	}

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

	var split *transaction.SplitTransactionRequiresAllocationsError
	if errors.As(err, &split) {
		return toolError(contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeSplitTransactionRequiresAllocations,
			"This split transaction must be updated by supplying its complete allocations.",
			false,
			map[string]any{"id": split.ID},
		)))
	}

	var dependency *rollover.DependencyConflictError
	if errors.As(err, &dependency) {
		return toolError(dependencyConflictEnvelope(dependency))
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

func idempotencyConflictEnvelope(tool string, conflict *transaction.IdempotencyConflictError) contract.ErrorEnvelope {
	details := map[string]any{
		"idempotency_key": conflict.IdempotencyKey,
		"reason":          conflict.Reason,
	}
	message := ""
	switch {
	case tool == "add_transactions" && conflict.Reason == transaction.IdempotencyReasonPayloadMismatch:
		message = "The idempotency key has already been used for a different transaction import."
	case tool == "add_transactions" && conflict.Reason == transaction.IdempotencyReasonTransactionRemoved:
		message = "An imported transaction was removed; this idempotency key cannot be reused and the batch must not be resubmitted."
		indexes := conflict.RemovedIndexes
		if indexes == nil {
			indexes = []int{}
		}
		details["removed_indexes"] = indexes
	case conflict.Reason == transaction.IdempotencyReasonPayloadMismatch:
		message = "The idempotency key has already been used for a different transaction request."
	case conflict.Reason == transaction.IdempotencyReasonTransactionRemoved:
		message = "The original transaction was removed; this idempotency key cannot be reused."
	}
	return contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeIdempotencyConflict,
		message,
		false,
		details,
	))
}

func withDetailIndex(envelope contract.ErrorEnvelope, index int) contract.ErrorEnvelope {
	details := make(map[string]any, len(envelope.Error.Details)+1)
	for key, value := range envelope.Error.Details {
		details[key] = value
	}
	details["index"] = index
	envelope.Error.Details = details
	return envelope
}

func (t *transactionTools) internalError(tool string, err error) (*mcp.CallToolResult, any, error) {
	if t.logger != nil {
		t.logger.Printf("%s: %v", tool, err)
	}
	return toolError(contract.NewInternalErrorEnvelope())
}

func nonNilRolloverOffers(offers []contract.RolloverOffer) []contract.RolloverOffer {
	if offers == nil {
		return []contract.RolloverOffer{}
	}
	return offers
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
	if raw, ok := args["allocations"]; ok {
		if isJSONNull(raw) {
			out.AllocationsNull = true
		} else {
			allocations := make([]transaction.AllocationInput, len(*in.Allocations))
			for i, allocation := range *in.Allocations {
				allocations[i] = transaction.AllocationInput{Category: allocation.Category, Amount: allocation.Amount}
			}
			out.Allocations = &allocations
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
