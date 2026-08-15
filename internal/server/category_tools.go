package server

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/jordanp2002/local-finance-mcp/internal/category"
	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type categoryNameInput struct {
	Name string `json:"name"`
}

type listCategoriesInput struct{}

type createCategoryOutput struct {
	OK          bool              `json:"ok"`
	Category    contract.Category `json:"category"`
	Created     bool              `json:"created"`
	Reactivated bool              `json:"reactivated"`
}

type listCategoriesOutput struct {
	OK         bool                `json:"ok"`
	Categories []contract.Category `json:"categories"`
}

type disableCategoryOutput struct {
	OK            bool              `json:"ok"`
	Category      contract.Category `json:"category"`
	Changed       bool              `json:"changed"`
	RemovedBudget *contract.Budget  `json:"removed_budget"`
}

type categoryTools struct {
	store  *category.Store
	logger *log.Logger
}

func registerCategoryTools(srv *mcp.Server, store *category.Store, logger *log.Logger) {
	tools := &categoryTools{store: store, logger: logger}

	mcp.AddTool[categoryNameInput, any](srv, &mcp.Tool{
		Name:        "create_category",
		Description: "Create an active spending category, or re-enable an inactive category with the same name.",
	}, tools.createCategory)

	mcp.AddTool[listCategoriesInput, any](srv, &mcp.Tool{
		Name:        "list_categories",
		Description: "List active spending categories ordered by name.",
	}, tools.listCategories)

	mcp.AddTool[categoryNameInput, any](srv, &mcp.Tool{
		Name:        "disable_category",
		Description: "Disable a category and remove only its current-month budget.",
	}, tools.disableCategory)
}

func (t *categoryTools) createCategory(ctx context.Context, _ *mcp.CallToolRequest, in categoryNameInput) (*mcp.CallToolResult, any, error) {
	if category.NormalizeName(in.Name) == "" {
		return toolError(invalidNameEnvelope("must not be empty"))
	}

	cat, created, reactivated, err := t.store.Create(ctx, in.Name)
	if err != nil {
		switch {
		case errors.Is(err, category.ErrAlreadyExists):
			return toolError(contract.NewErrorEnvelope(contract.NewError(
				contract.ErrorCodeCategoryAlreadyExists,
				fmt.Sprintf("Category '%s' already exists.", cat.Name),
				false,
				map[string]any{"category": cat},
			)))
		case errors.Is(err, category.ErrNameContainsNUL):
			return toolError(invalidNameEnvelope("must not contain NUL characters"))
		case errors.Is(err, category.ErrInvalidName):
			return toolError(invalidNameEnvelope("must not be empty"))
		default:
			return t.internalError("create_category", err)
		}
	}

	return toolOK(createCategoryOutput{
		OK:          true,
		Category:    cat,
		Created:     created,
		Reactivated: reactivated,
	})
}

func (t *categoryTools) listCategories(ctx context.Context, _ *mcp.CallToolRequest, _ listCategoriesInput) (*mcp.CallToolResult, any, error) {
	cats, err := t.store.List(ctx)
	if err != nil {
		return t.internalError("list_categories", err)
	}

	return toolOK(listCategoriesOutput{
		OK:         true,
		Categories: activeCategories(cats),
	})
}

func (t *categoryTools) disableCategory(ctx context.Context, _ *mcp.CallToolRequest, in categoryNameInput) (*mcp.CallToolResult, any, error) {
	if category.NormalizeName(in.Name) == "" {
		return toolError(invalidNameEnvelope("must not be empty"))
	}

	cat, changed, removed, err := t.store.Disable(ctx, in.Name)
	if err != nil {
		switch {
		case errors.Is(err, category.ErrNotFound):
			return t.categoryNotFound(ctx, in.Name)
		case errors.Is(err, category.ErrNameContainsNUL):
			return toolError(invalidNameEnvelope("must not contain NUL characters"))
		case errors.Is(err, category.ErrInvalidName):
			return toolError(invalidNameEnvelope("must not be empty"))
		default:
			return t.internalError("disable_category", err)
		}
	}

	return toolOK(disableCategoryOutput{
		OK:            true,
		Category:      cat,
		Changed:       changed,
		RemovedBudget: removed,
	})
}

func (t *categoryTools) categoryNotFound(ctx context.Context, name string) (*mcp.CallToolResult, any, error) {
	requested := category.NormalizeName(name)
	cats, err := t.store.List(ctx)
	if err != nil {
		return t.internalError("disable_category", err)
	}

	return toolError(contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeCategoryNotFound,
		fmt.Sprintf("Category '%s' does not exist.", requested),
		false,
		map[string]any{
			"requested_category": requested,
			"categories":         activeCategories(cats),
		},
	)))
}

func (t *categoryTools) internalError(tool string, err error) (*mcp.CallToolResult, any, error) {
	t.logger.Printf("%s: %v", tool, err)
	return toolError(contract.NewInternalErrorEnvelope(err))
}

func toolOK(output any) (*mcp.CallToolResult, any, error) {
	return nil, output, nil
}

func toolError(envelope contract.ErrorEnvelope) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{IsError: true}, envelope, nil
}

func invalidNameEnvelope(reason string) contract.ErrorEnvelope {
	return contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{
			"fields": []map[string]string{
				{"field": "name", "reason": reason},
			},
		},
	))
}

func activeCategories(cats []contract.Category) []contract.Category {
	if cats == nil {
		return []contract.Category{}
	}
	return cats
}
