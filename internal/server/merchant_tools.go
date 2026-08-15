package server

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/jordanp2002/local-finance-mcp/internal/category"
	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/jordanp2002/local-finance-mcp/internal/merchant"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type setKnownMerchantInput struct {
	Merchant string `json:"merchant"`
	Category string `json:"category"`
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
	}, tools.setKnownMerchant)

	mcp.AddTool[listKnownMerchantsInput, any](srv, &mcp.Tool{
		Name:        "list_known_merchants",
		Description: "List exact merchant-to-category defaults with optional search and pagination.",
	}, tools.listKnownMerchants)
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
