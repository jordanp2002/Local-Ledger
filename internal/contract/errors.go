// Package contract defines stable finance-tool values.
package contract

import "encoding/json"

type ErrorCode string

const (
	ErrorCodeInvalidInput                        ErrorCode = "invalid_input"
	ErrorCodeCategoryNotFound                    ErrorCode = "category_not_found"
	ErrorCodeCategoryAlreadyExists               ErrorCode = "category_already_exists"
	ErrorCodeCategoryInactive                    ErrorCode = "category_inactive"
	ErrorCodeKnownMerchantNotFound               ErrorCode = "known_merchant_not_found"
	ErrorCodeKnownMerchantAlreadyExists          ErrorCode = "known_merchant_already_exists"
	ErrorCodeMerchantCategoryRequired            ErrorCode = "merchant_category_required"
	ErrorCodeMerchantCategoryInactive            ErrorCode = "merchant_category_inactive"
	ErrorCodeTransactionNotFound                 ErrorCode = "transaction_not_found"
	ErrorCodeMonthlyBudgetAlreadyExists          ErrorCode = "monthly_budget_already_exists"
	ErrorCodeMonthlyBudgetNotFound               ErrorCode = "monthly_budget_not_found"
	ErrorCodeBudgetSourceNotFound                ErrorCode = "budget_source_not_found"
	ErrorCodeBudgetSourceEmpty                   ErrorCode = "budget_source_empty"
	ErrorCodeIdempotencyConflict                 ErrorCode = "idempotency_conflict"
	ErrorCodeSplitTransactionRequiresAllocations ErrorCode = "split_transaction_requires_allocations"
	ErrorCodeBudgetRolloverNotEligible           ErrorCode = "budget_rollover_not_eligible"
	ErrorCodeBudgetRolloverNotFound              ErrorCode = "budget_rollover_not_found"
	ErrorCodeBudgetRolloverDependencyConflict    ErrorCode = "budget_rollover_dependency_conflict"
	ErrorCodeSinkingFundNotActive                ErrorCode = "sinking_fund_not_active"
	ErrorCodeSinkingFundActive                   ErrorCode = "sinking_fund_active"
	ErrorCodeSinkingFundRolloverConflict         ErrorCode = "sinking_fund_rollover_conflict"
	ErrorCodeRecurringTransactionNotFound        ErrorCode = "recurring_transaction_not_found"
	ErrorCodeRecurringCategoryInactive           ErrorCode = "recurring_category_inactive"
	ErrorCodeInternalError                       ErrorCode = "internal_error"
)

type Error struct {
	Code      ErrorCode      `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details"`
}

func (e Error) Error() string {
	if e.Code == ErrorCodeInternalError {
		return defaultErrorMessage(e.Code)
	}
	if e.Message != "" {
		return e.Message
	}
	return defaultErrorMessage(e.Code)
}

// MarshalJSON keeps the response shape stable and redacts internal errors.
func (e Error) MarshalJSON() ([]byte, error) {
	details := e.Details
	message := e.Message
	retryable := e.Retryable
	if e.Code == ErrorCodeInternalError {
		details = nil
		message = defaultErrorMessage(e.Code)
		retryable = true
	} else if message == "" {
		message = defaultErrorMessage(e.Code)
	}
	if details == nil {
		details = map[string]any{}
	}

	type wireError struct {
		Code      ErrorCode      `json:"code"`
		Message   string         `json:"message"`
		Retryable bool           `json:"retryable"`
		Details   map[string]any `json:"details"`
	}
	return json.Marshal(wireError{
		Code:      e.Code,
		Message:   message,
		Retryable: retryable,
		Details:   details,
	})
}

// ErrorEnvelope is the public shape for a failed tool call.
type ErrorEnvelope struct {
	OK    bool  `json:"ok"`
	Error Error `json:"error"`
}

// NewError constructs a public error with a default message when needed.
func NewError(code ErrorCode, message string, retryable bool, details map[string]any) Error {
	if code == ErrorCodeInternalError {
		message = defaultErrorMessage(code)
		retryable = true
		details = nil
	} else if message == "" {
		message = defaultErrorMessage(code)
	}
	return Error{
		Code:      code,
		Message:   message,
		Retryable: retryable,
		Details:   cloneDetails(details),
	}
}

// NewErrorEnvelope wraps an error in the failed-response shape.
func NewErrorEnvelope(err Error) ErrorEnvelope {
	return ErrorEnvelope{Error: err}
}

// NewInternalError creates a safe public error for an unexpected failure.
func NewInternalError(_ ...error) Error {
	return NewError(ErrorCodeInternalError, "", true, nil)
}

// NewInternalErrorEnvelope creates a failed response for an unexpected error.
func NewInternalErrorEnvelope(cause ...error) ErrorEnvelope {
	return NewErrorEnvelope(NewInternalError(cause...))
}

func cloneDetails(details map[string]any) map[string]any {
	if details == nil {
		return nil
	}
	cloned := make(map[string]any, len(details))
	for key, value := range details {
		cloned[key] = value
	}
	return cloned
}

func defaultErrorMessage(code ErrorCode) string {
	switch code {
	case ErrorCodeInvalidInput:
		return "One or more input fields are invalid."
	case ErrorCodeCategoryNotFound:
		return "The requested category was not found."
	case ErrorCodeCategoryAlreadyExists:
		return "The category already exists."
	case ErrorCodeCategoryInactive:
		return "The requested category is inactive."
	case ErrorCodeKnownMerchantNotFound:
		return "The known merchant was not found."
	case ErrorCodeKnownMerchantAlreadyExists:
		return "The known merchant already exists."
	case ErrorCodeMerchantCategoryRequired:
		return "An active category is required for this merchant."
	case ErrorCodeMerchantCategoryInactive:
		return "The merchant's category is inactive."
	case ErrorCodeTransactionNotFound:
		return "The requested transaction was not found."
	case ErrorCodeMonthlyBudgetAlreadyExists:
		return "A monthly budget already exists for this month."
	case ErrorCodeMonthlyBudgetNotFound:
		return "The monthly budget was not found."
	case ErrorCodeBudgetSourceNotFound:
		return "No earlier monthly budget was found to carry forward."
	case ErrorCodeBudgetSourceEmpty:
		return "The earlier monthly budget has no active categories to carry forward."
	case ErrorCodeIdempotencyConflict:
		return "The idempotency key conflicts with an existing request."
	case ErrorCodeSplitTransactionRequiresAllocations:
		return "This split transaction must be updated by supplying its complete allocations."
	case ErrorCodeBudgetRolloverNotEligible:
		return "The requested budget rollover is not eligible."
	case ErrorCodeBudgetRolloverNotFound:
		return "The requested budget rollover was not found."
	case ErrorCodeBudgetRolloverDependencyConflict:
		return "The budget rollover conflicts with a dependent adjustment."
	case ErrorCodeSinkingFundNotActive:
		return "The sinking fund is not active."
	case ErrorCodeSinkingFundActive:
		return "The sinking fund is already active."
	case ErrorCodeSinkingFundRolloverConflict:
		return "The sinking fund conflicts with an explicit budget rollover."
	case ErrorCodeRecurringTransactionNotFound:
		return "The requested recurring transaction was not found."
	case ErrorCodeRecurringCategoryInactive:
		return "The recurring transaction references an inactive category."
	case ErrorCodeInternalError:
		return "The operation could not be completed."
	default:
		return "The operation could not be completed."
	}
}
