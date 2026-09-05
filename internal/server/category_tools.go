package server

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/jordanp2002/Local-Ledger/internal/category"
	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/rollover"
	"github.com/jordanp2002/Local-Ledger/internal/sinkingfund"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type categoryNameInput struct {
	Name string `json:"name"`
}

type renameCategoryInput struct {
	Category string `json:"category"`
	NewName  string `json:"new_name"`
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

type renameCategoryOutput struct {
	OK           bool              `json:"ok"`
	Category     contract.Category `json:"category"`
	PreviousName string            `json:"previous_name"`
	Changed      bool              `json:"changed"`
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
		Annotations: writableToolAnnotations(true, true),
	}, tools.createCategory)

	mcp.AddTool[listCategoriesInput, any](srv, &mcp.Tool{
		Name:        "list_categories",
		Description: "List active spending categories ordered by name.",
		Annotations: readOnlyToolAnnotations(),
	}, tools.listCategories)

	mcp.AddTool[categoryNameInput, any](srv, &mcp.Tool{
		Name:        "disable_category",
		Description: "Disable a category and remove only its current-month budget.",
		Annotations: writableToolAnnotations(true, true),
	}, tools.disableCategory)

	mcp.AddTool[renameCategoryInput, any](srv, &mcp.Tool{
		Name:        "rename_category",
		Description: "Rename a category while preserving its identity and financial history.",
		Annotations: writableToolAnnotations(true, true),
	}, tools.renameCategory)
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
			return internalToolError(t.logger, "create_category", err)
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
		return internalToolError(t.logger, "list_categories", err)
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
		case errors.Is(err, rollover.ErrDependencyConflict):
			var dependency *rollover.DependencyConflictError
			if !errors.As(err, &dependency) {
				return internalToolError(t.logger, "disable_category", err)
			}
			return toolError(dependencyConflictEnvelope(dependency))
		case errors.Is(err, sinkingfund.ErrActive):
			return toolError(contract.NewErrorEnvelope(contract.NewError(contract.ErrorCodeSinkingFundActive, "Disable the sinking fund before disabling the category.", false, nil)))
		default:
			return internalToolError(t.logger, "disable_category", err)
		}
	}

	return toolOK(disableCategoryOutput{
		OK:            true,
		Category:      cat,
		Changed:       changed,
		RemovedBudget: removed,
	})
}

func (t *categoryTools) renameCategory(ctx context.Context, _ *mcp.CallToolRequest, in renameCategoryInput) (*mcp.CallToolResult, any, error) {
	cat, previousName, changed, err := t.store.Rename(ctx, in.Category, in.NewName)
	if err != nil {
		var validation *category.ValidationError
		var collision *category.AlreadyExistsError
		switch {
		case errors.As(err, &validation):
			return toolError(invalidInputEnvelope(validation.Fields))
		case errors.Is(err, category.ErrNotFound):
			return t.categoryNotFoundFor(ctx, in.Category, "rename_category")
		case errors.As(err, &collision):
			return toolError(contract.NewErrorEnvelope(contract.NewError(
				contract.ErrorCodeCategoryAlreadyExists,
				fmt.Sprintf("Category '%s' already exists.", collision.Category.Name),
				false,
				map[string]any{"category": collision.Category},
			)))
		default:
			return internalToolError(t.logger, "rename_category", err)
		}
	}

	return toolOK(renameCategoryOutput{
		OK:           true,
		Category:     cat,
		PreviousName: previousName,
		Changed:      changed,
	})
}

func (t *categoryTools) categoryNotFound(ctx context.Context, name string) (*mcp.CallToolResult, any, error) {
	return t.categoryNotFoundFor(ctx, name, "disable_category")
}

func (t *categoryTools) categoryNotFoundFor(ctx context.Context, name, tool string) (*mcp.CallToolResult, any, error) {
	requested := category.NormalizeName(name)
	cats, err := t.store.List(ctx)
	if err != nil {
		return internalToolError(t.logger, tool, err)
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
