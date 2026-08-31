package server

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/jordanp2002/local-finance-mcp/internal/sinkingfund"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type sinkingFundInput struct {
	Category string `json:"category"`
}
type listSinkingFundsInput struct {
	Category       *string `json:"category,omitempty"`
	IncludeHistory bool    `json:"include_history,omitempty"`
}
type sinkingFundOutput struct {
	OK               bool                       `json:"ok"`
	Changed          bool                       `json:"changed"`
	Period           contract.SinkingFundPeriod `json:"period"`
	CurrentMonth     string                     `json:"current_month"`
	BaseContribution string                     `json:"base_contribution"`
	OpeningBalance   string                     `json:"opening_balance"`
	AvailableBalance string                     `json:"available_balance"`
	Spending         string                     `json:"spending"`
	ClosingBalance   string                     `json:"closing_balance"`
	ReleasedBalance  *string                    `json:"released_balance,omitempty"`
	EffectiveMonth   string                     `json:"effective_month,omitempty"`
}
type sinkingFundListItem struct {
	Period           contract.SinkingFundPeriod `json:"period"`
	CurrentMonth     string                     `json:"current_month"`
	BaseContribution string                     `json:"base_contribution"`
	OpeningBalance   string                     `json:"opening_balance"`
	AvailableBalance string                     `json:"available_balance"`
	Spending         string                     `json:"spending"`
	ClosingBalance   string                     `json:"closing_balance"`
	ReleasedBalance  *string                    `json:"released_balance,omitempty"`
}
type sinkingFundListOutput struct {
	OK    bool                  `json:"ok"`
	Funds []sinkingFundListItem `json:"funds"`
}

func registerSinkingFundTools(srv *mcp.Server, store *sinkingfund.Store, logger *log.Logger) {
	mcp.AddTool[sinkingFundInput, any](srv, &mcp.Tool{Name: "enable_sinking_fund", Description: "Enable a category sinking fund for the current month.", Annotations: writableToolAnnotations(true, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, in sinkingFundInput) (*mcp.CallToolResult, any, error) {
		r, f, e := store.Enable(ctx, sinkingfund.EnableInput{Category: in.Category})
		if len(f) > 0 {
			return toolError(invalidSinkingFundInput(f))
		}
		if e != nil {
			return mapSinkingFundError("enable_sinking_fund", e, logger)
		}
		var released *string
		if r.ReleasedBalance != nil {
			value := amount(*r.ReleasedBalance)
			released = &value
		}
		return toolOK(sinkingFundOutput{OK: true, Changed: r.Changed, Period: periodContract(r.Period), CurrentMonth: r.Balance.CurrentMonth, BaseContribution: amount(r.Balance.BaseContribution), OpeningBalance: amount(r.Balance.OpeningBalance), AvailableBalance: amount(r.Balance.AvailableBalance), Spending: amount(r.Balance.Spending), ClosingBalance: amount(r.Balance.ClosingBalance), ReleasedBalance: released})
	})
	mcp.AddTool[sinkingFundInput, any](srv, &mcp.Tool{Name: "disable_sinking_fund", Description: "Disable a category sinking fund after the current month and release its closing balance.", Annotations: writableToolAnnotations(true, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, in sinkingFundInput) (*mcp.CallToolResult, any, error) {
		r, f, e := store.Disable(ctx, sinkingfund.DisableInput{Category: in.Category})
		if len(f) > 0 {
			return toolError(invalidSinkingFundInput(f))
		}
		if e != nil {
			return mapSinkingFundError("disable_sinking_fund", e, logger)
		}
		released := amount(r.ReleasedBalance)
		return toolOK(sinkingFundOutput{OK: true, Changed: r.Changed, Period: periodContract(r.Period), CurrentMonth: endMonthValue(r.Period), ClosingBalance: released, ReleasedBalance: &released, EffectiveMonth: r.EffectiveMonth})
	})
	mcp.AddTool[listSinkingFundsInput, any](srv, &mcp.Tool{Name: "list_sinking_funds", Description: "List current sinking-fund balances, optionally including completed history.", Annotations: readOnlyToolAnnotations()}, func(ctx context.Context, _ *mcp.CallToolRequest, in listSinkingFundsInput) (*mcp.CallToolResult, any, error) {
		r, f, e := store.List(ctx, sinkingfund.ListInput{Category: in.Category, IncludeHistory: in.IncludeHistory})
		if len(f) > 0 {
			return toolError(invalidSinkingFundInput(f))
		}
		if e != nil {
			return mapSinkingFundError("list_sinking_funds", e, logger)
		}
		items := make([]sinkingFundListItem, 0, len(r.Funds))
		for _, b := range r.Funds {
			released := (*string)(nil)
			if b.Period.EndMonth != nil {
				x := amount(b.ClosingBalance)
				released = &x
			}
			items = append(items, sinkingFundListItem{Period: periodContract(b.Period), CurrentMonth: b.CurrentMonth, BaseContribution: amount(b.BaseContribution), OpeningBalance: amount(b.OpeningBalance), AvailableBalance: amount(b.AvailableBalance), Spending: amount(b.Spending), ClosingBalance: amount(b.ClosingBalance), ReleasedBalance: released})
		}
		return toolOK(sinkingFundListOutput{OK: true, Funds: items})
	})
}
func periodContract(p sinkingfund.Period) contract.SinkingFundPeriod {
	return contract.SinkingFundPeriod{ID: p.ID, CategoryID: p.CategoryID, Category: p.Category, CategoryActive: p.CategoryActive, StartMonth: p.StartMonth, EndMonth: p.EndMonth, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}
func amount(v int64) string {
	x, e := contract.FormatSignedAmount(v)
	if e != nil {
		return "0.00"
	}
	return x
}
func endMonthValue(p sinkingfund.Period) string {
	if p.EndMonth != nil {
		return *p.EndMonth
	}
	return p.StartMonth
}
func invalidSinkingFundInput(f []contract.FieldIssue) contract.ErrorEnvelope {
	return contract.NewErrorEnvelope(contract.NewError(contract.ErrorCodeInvalidInput, "", false, map[string]any{"fields": f}))
}
func mapSinkingFundError(tool string, e error, l *log.Logger) (*mcp.CallToolResult, any, error) {
	code := contract.ErrorCodeInternalError
	details := map[string]any{}
	switch {
	case errors.Is(e, sinkingfund.ErrNotActive):
		code = contract.ErrorCodeSinkingFundNotActive
	case errors.Is(e, sinkingfund.ErrActive):
		code = contract.ErrorCodeSinkingFundActive
	case errors.Is(e, sinkingfund.ErrRolloverConflict):
		code = contract.ErrorCodeSinkingFundRolloverConflict
	case errors.Is(e, sinkingfund.ErrMissingSnapshot):
		code = contract.ErrorCodeInvalidInput
		details["reason"] = "current month must have a budget snapshot"
	case errors.Is(e, sinkingfund.ErrMissingBudget):
		code = contract.ErrorCodeInvalidInput
		details["reason"] = "current month must have a budget row for the category"
	case errors.Is(e, sinkingfund.ErrCategoryNotFound):
		code = contract.ErrorCodeCategoryNotFound
	case errors.Is(e, sinkingfund.ErrCategoryInactive):
		code = contract.ErrorCodeCategoryInactive
	default:
		if l != nil {
			l.Printf("%s: %v", tool, e)
		}
		return toolError(contract.NewInternalErrorEnvelope())
	}
	return toolError(contract.NewErrorEnvelope(contract.NewError(code, fmt.Sprint(e), false, details)))
}
