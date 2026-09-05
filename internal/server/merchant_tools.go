package server

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/jordanp2002/Local-Ledger/internal/category"
	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/merchant"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type setKnownMerchantInput struct {
	Merchant string `json:"merchant"`
	Category string `json:"category"`
}

type renameKnownMerchantInput struct {
	Merchant    string `json:"merchant"`
	NewMerchant string `json:"new_merchant"`
}

type removeKnownMerchantInput struct {
	Merchant string `json:"merchant"`
}

type listKnownMerchantsInput struct {
	Query  string `json:"query,omitempty"`
	Limit  *int64 `json:"limit,omitempty"`
	Offset *int64 `json:"offset,omitempty"`
}

type setKnownMerchantOutput struct {
	OK               bool                   `json:"ok"`
	KnownMerchant    contract.KnownMerchant `json:"known_merchant"`
	Created          bool                   `json:"created"`
	PreviousCategory *string                `json:"previous_category"`
}

type listKnownMerchantsOutput struct {
	OK             bool                     `json:"ok"`
	KnownMerchants []contract.KnownMerchant `json:"known_merchants"`
	Page           contract.Page            `json:"page"`
}

type renameKnownMerchantOutput struct {
	OK               bool                   `json:"ok"`
	KnownMerchant    contract.KnownMerchant `json:"known_merchant"`
	PreviousMerchant string                 `json:"previous_merchant"`
	Changed          bool                   `json:"changed"`
}

type removeKnownMerchantOutput struct {
	OK                   bool                   `json:"ok"`
	RemovedKnownMerchant contract.KnownMerchant `json:"removed_known_merchant"`
}

type merchantTools struct {
	store      *merchant.Store
	categories *category.Store
	logger     *log.Logger
}

func registerMerchantTools(srv *mcp.Server, store *merchant.Store, categories *category.Store, logger *log.Logger) {
	tools := &merchantTools{store: store, categories: categories, logger: logger}

	mcp.AddTool[setKnownMerchantInput, any](srv, &mcp.Tool{
		Name:        "set_known_merchant",
		Description: "Create or replace an exact merchant-to-category default.",
		Annotations: writableToolAnnotations(true, true),
	}, tools.setKnownMerchant)

	mcp.AddTool[listKnownMerchantsInput, any](srv, &mcp.Tool{
		Name:        "list_known_merchants",
		Description: "List exact merchant-to-category defaults with optional search and pagination.",
		Annotations: readOnlyToolAnnotations(),
	}, tools.listKnownMerchants)

	mcp.AddTool[renameKnownMerchantInput, any](srv, &mcp.Tool{
		Name:        "rename_known_merchant",
		Description: "Rename a known merchant default while preserving its mapping and transaction history.",
		Annotations: writableToolAnnotations(true, true),
	}, tools.renameKnownMerchant)

	mcp.AddTool[removeKnownMerchantInput, any](srv, &mcp.Tool{
		Name:        "remove_known_merchant",
		Description: "Remove a known merchant default without changing transaction history.",
		Annotations: writableToolAnnotations(true, true),
	}, tools.removeKnownMerchant)
}

func (t *merchantTools) setKnownMerchant(ctx context.Context, _ *mcp.CallToolRequest, in setKnownMerchantInput) (*mcp.CallToolResult, any, error) {
	result, err := t.store.Set(ctx, in.Merchant, in.Category)
	if err != nil {
		switch {
		case errors.Is(err, merchant.ErrCategoryNotFound):
			requested := contract.TrimASCIIWhitespace(in.Category)
			var notFound *merchant.CategoryNotFoundError
			if errors.As(err, &notFound) {
				requested = notFound.Requested
			}
			return t.categoryNotFound(ctx, requested)
		case errors.Is(err, merchant.ErrCategoryInactive):
			inactiveCategory := result.TargetCategory
			if inactiveCategory.Name == "" {
				var inactive *merchant.CategoryInactiveError
				if !errors.As(err, &inactive) {
					return t.internalError("set_known_merchant", err)
				}
				inactiveCategory = inactive.Category
			}
			if inactiveCategory.Name == "" {
				return t.internalError("set_known_merchant", err)
			}
			return t.categoryInactive(ctx, inactiveCategory)
		case errors.Is(err, merchant.ErrInvalidMerchant), errors.Is(err, merchant.ErrInvalidCategory):
			var validation *merchant.ValidationError
			if errors.As(err, &validation) {
				return toolError(invalidMerchantInputEnvelope(validation.Fields))
			}
			return t.internalError("set_known_merchant", err)
		default:
			return t.internalError("set_known_merchant", err)
		}
	}

	return toolOK(setKnownMerchantOutput{
		OK:               true,
		KnownMerchant:    result.KnownMerchant,
		Created:          result.Created,
		PreviousCategory: result.PreviousCategory,
	})
}

func (t *merchantTools) listKnownMerchants(ctx context.Context, _ *mcp.CallToolRequest, in listKnownMerchantsInput) (*mcp.CallToolResult, any, error) {
	result, fields, err := t.store.List(ctx, merchant.ListOptions{
		Query:  in.Query,
		Limit:  in.Limit,
		Offset: in.Offset,
	})
	if len(fields) != 0 {
		return toolError(invalidMerchantInputEnvelope(fields))
	}
	if err != nil {
		return t.internalError("list_known_merchants", err)
	}

	knownMerchants := result.KnownMerchants
	if knownMerchants == nil {
		knownMerchants = []contract.KnownMerchant{}
	}
	return toolOK(listKnownMerchantsOutput{
		OK:             true,
		KnownMerchants: knownMerchants,
		Page:           result.Page,
	})
}

func (t *merchantTools) renameKnownMerchant(ctx context.Context, _ *mcp.CallToolRequest, in renameKnownMerchantInput) (*mcp.CallToolResult, any, error) {
	knownMerchant, previousMerchant, changed, err := t.store.Rename(ctx, in.Merchant, in.NewMerchant)
	if err != nil {
		var validation *merchant.ValidationError
		var notFound *merchant.NotFoundError
		var collision *merchant.AlreadyExistsError
		switch {
		case errors.As(err, &validation):
			return toolError(invalidMerchantInputEnvelope(validation.Fields))
		case errors.As(err, &notFound):
			return knownMerchantNotFoundEnvelope(notFound.Requested)
		case errors.As(err, &collision):
			return knownMerchantAlreadyExistsEnvelope(collision.KnownMerchant)
		default:
			return t.internalError("rename_known_merchant", err)
		}
	}

	return toolOK(renameKnownMerchantOutput{
		OK:               true,
		KnownMerchant:    knownMerchant,
		PreviousMerchant: previousMerchant,
		Changed:          changed,
	})
}

func (t *merchantTools) removeKnownMerchant(ctx context.Context, _ *mcp.CallToolRequest, in removeKnownMerchantInput) (*mcp.CallToolResult, any, error) {
	knownMerchant, err := t.store.Remove(ctx, in.Merchant)
	if err != nil {
		var validation *merchant.ValidationError
		var notFound *merchant.NotFoundError
		switch {
		case errors.As(err, &validation):
			return toolError(invalidMerchantInputEnvelope(validation.Fields))
		case errors.As(err, &notFound):
			return knownMerchantNotFoundEnvelope(notFound.Requested)
		default:
			return t.internalError("remove_known_merchant", err)
		}
	}

	return toolOK(removeKnownMerchantOutput{
		OK:                   true,
		RemovedKnownMerchant: knownMerchant,
	})
}

func knownMerchantNotFoundEnvelope(requested string) (*mcp.CallToolResult, any, error) {
	return toolError(contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeKnownMerchantNotFound,
		fmt.Sprintf("Known merchant '%s' does not exist.", requested),
		false,
		map[string]any{"requested_merchant": requested},
	)))
}

func knownMerchantAlreadyExistsEnvelope(conflict contract.KnownMerchant) (*mcp.CallToolResult, any, error) {
	return toolError(contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeKnownMerchantAlreadyExists,
		fmt.Sprintf("Known merchant '%s' already exists.", conflict.Merchant),
		false,
		map[string]any{"known_merchant": conflict},
	)))
}

func invalidMerchantInputEnvelope(fields []contract.FieldIssue) contract.ErrorEnvelope {
	return contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{"fields": fields},
	))
}

func (t *merchantTools) categoryNotFound(ctx context.Context, requested string) (*mcp.CallToolResult, any, error) {
	categories, err := t.categories.List(ctx)
	if err != nil {
		return t.internalError("set_known_merchant", err)
	}

	return toolError(contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeCategoryNotFound,
		fmt.Sprintf("Category '%s' does not exist.", requested),
		false,
		map[string]any{
			"requested_category": requested,
			"categories":         activeCategories(categories),
		},
	)))
}

func (t *merchantTools) categoryInactive(ctx context.Context, inactive contract.Category) (*mcp.CallToolResult, any, error) {
	categories, err := t.categories.List(ctx)
	if err != nil {
		return t.internalError("set_known_merchant", err)
	}

	return toolError(contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeCategoryInactive,
		fmt.Sprintf("Category '%s' is inactive.", inactive.Name),
		false,
		map[string]any{
			"category":          inactive,
			"active_categories": activeCategories(categories),
		},
	)))
}

func (t *merchantTools) internalError(tool string, err error) (*mcp.CallToolResult, any, error) {
	if t.logger != nil {
		t.logger.Printf("%s: %v", tool, err)
	}
	return toolError(contract.NewInternalErrorEnvelope())
}
