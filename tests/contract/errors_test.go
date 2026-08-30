package contract_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

func TestErrorCodesMatchApprovedContract(t *testing.T) {
	tests := []struct {
		name string
		code contract.ErrorCode
		want string
	}{
		{name: "invalid input", code: contract.ErrorCodeInvalidInput, want: "invalid_input"},
		{name: "category not found", code: contract.ErrorCodeCategoryNotFound, want: "category_not_found"},
		{name: "category already exists", code: contract.ErrorCodeCategoryAlreadyExists, want: "category_already_exists"},
		{name: "category inactive", code: contract.ErrorCodeCategoryInactive, want: "category_inactive"},
		{name: "known merchant not found", code: contract.ErrorCodeKnownMerchantNotFound, want: "known_merchant_not_found"},
		{name: "known merchant already exists", code: contract.ErrorCodeKnownMerchantAlreadyExists, want: "known_merchant_already_exists"},
		{name: "merchant category required", code: contract.ErrorCodeMerchantCategoryRequired, want: "merchant_category_required"},
		{name: "merchant category inactive", code: contract.ErrorCodeMerchantCategoryInactive, want: "merchant_category_inactive"},
		{name: "transaction not found", code: contract.ErrorCodeTransactionNotFound, want: "transaction_not_found"},
		{name: "monthly budget already exists", code: contract.ErrorCodeMonthlyBudgetAlreadyExists, want: "monthly_budget_already_exists"},
		{name: "monthly budget not found", code: contract.ErrorCodeMonthlyBudgetNotFound, want: "monthly_budget_not_found"},
		{name: "budget source not found", code: contract.ErrorCodeBudgetSourceNotFound, want: "budget_source_not_found"},
		{name: "budget source empty", code: contract.ErrorCodeBudgetSourceEmpty, want: "budget_source_empty"},
		{name: "idempotency conflict", code: contract.ErrorCodeIdempotencyConflict, want: "idempotency_conflict"},
		{name: "recurring transaction not found", code: contract.ErrorCodeRecurringTransactionNotFound, want: "recurring_transaction_not_found"},
		{name: "recurring category inactive", code: contract.ErrorCodeRecurringCategoryInactive, want: "recurring_category_inactive"},
		{name: "internal error", code: contract.ErrorCodeInternalError, want: "internal_error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(tt.code); got != tt.want {
				t.Fatalf("error code = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestErrorEnvelopeUsesStableJSONShape(t *testing.T) {
	publicErr := contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{
			"field":  "amount",
			"issues": []any{"must have at most two fractional digits"},
		},
	)

	got, err := json.Marshal(contract.NewErrorEnvelope(publicErr))
	if err != nil {
		t.Fatalf("marshal error envelope: %v", err)
	}

	want := `{"ok":false,"error":{"code":"invalid_input","message":"One or more input fields are invalid.","retryable":false,"details":{"field":"amount","issues":["must have at most two fractional digits"]}}}`
	if string(got) != want {
		t.Fatalf("error envelope JSON = %s, want %s", got, want)
	}
}

func TestErrorEnvelopeEncodesNilDetailsAsObject(t *testing.T) {
	envelope := contract.ErrorEnvelope{
		Error: contract.Error{
			Code:      contract.ErrorCodeCategoryNotFound,
			Retryable: false,
		},
	}

	got, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal error envelope: %v", err)
	}
	want := `{"ok":false,"error":{"code":"category_not_found","message":"The requested category was not found.","retryable":false,"details":{}}}`
	if string(got) != want {
		t.Fatalf("error envelope JSON = %s, want %s", got, want)
	}
}

func TestInternalErrorDoesNotExposeCauseOrDetails(t *testing.T) {
	secret := "sqlite: /private/ledger/finance.db: malformed query"
	internal := contract.Error{
		Code:      contract.ErrorCodeInternalError,
		Message:   secret,
		Retryable: true,
		Details:   map[string]any{"cause": errors.New(secret)},
	}

	got, err := json.Marshal(contract.ErrorEnvelope{Error: internal})
	if err != nil {
		t.Fatalf("marshal internal error envelope: %v", err)
	}
	if strings.Contains(string(got), secret) {
		t.Fatalf("internal error JSON leaked implementation detail: %s", got)
	}
	want := `{"ok":false,"error":{"code":"internal_error","message":"The operation could not be completed.","retryable":true,"details":{}}}`
	if string(got) != want {
		t.Fatalf("internal error JSON = %s, want %s", got, want)
	}
}

func TestNewInternalErrorDiscardsCause(t *testing.T) {
	secret := errors.New("driver failure: /private/ledger/finance.db")
	got, err := json.Marshal(contract.NewInternalErrorEnvelope(secret))
	if err != nil {
		t.Fatalf("marshal internal error envelope: %v", err)
	}
	if strings.Contains(string(got), secret.Error()) {
		t.Fatalf("internal error JSON leaked cause: %s", got)
	}
	want := `{"ok":false,"error":{"code":"internal_error","message":"The operation could not be completed.","retryable":true,"details":{}}}`
	if string(got) != want {
		t.Fatalf("internal error JSON = %s, want %s", got, want)
	}
}
