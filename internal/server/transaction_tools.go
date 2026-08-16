package server

import (
	"context"
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

type transactionTools struct {
	store  *transaction.Store
	logger *log.Logger
}

func registerTransactionTools(srv *mcp.Server, store *transaction.Store, logger *log.Logger) {
	tools := &transactionTools{store: store, logger: logger}

	mcp.AddTool[addTransactionInput, any](srv, &mcp.Tool{
		Name:        "add_transaction",
		Description: "Record one expense and apply exact merchant-default mapping rules atomically.",
	}, tools.addTransaction)
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

func (t *transactionTools) mapTransactionError(tool string, err error) (*mcp.CallToolResult, any, error) {
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
