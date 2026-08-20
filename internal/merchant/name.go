// Package merchant implements known-merchant mappings.
package merchant

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

const (
	DefaultLimit int64 = 50
	MaxLimit     int64 = 200
)

var (
	ErrInvalidMerchant     = errors.New("invalid merchant name")
	ErrMerchantContainsNUL = fmt.Errorf("%w: contains NUL", ErrInvalidMerchant)
	ErrInvalidCategory     = errors.New("invalid category name")
	ErrCategoryContainsNUL = fmt.Errorf("%w: contains NUL", ErrInvalidCategory)
	ErrCategoryNotFound    = errors.New("category not found")
	ErrCategoryInactive    = errors.New("category inactive")
	ErrNotFound            = errors.New("known merchant not found")
	ErrAlreadyExists       = errors.New("known merchant already exists")
)

// ValidationError contains format issues for a merchant operation.
type ValidationError struct {
	Fields []contract.FieldIssue
	causes []error
}

func (e *ValidationError) Error() string {
	if e == nil {
		return "invalid merchant input"
	}
	return "invalid merchant input"
}

// Is matches the underlying field-specific validation errors.
func (e *ValidationError) Is(target error) bool {
	if e == nil {
		return false
	}
	for _, cause := range e.causes {
		if errors.Is(cause, target) {
			return true
		}
	}
	return false
}

// CategoryNotFoundError identifies a missing category.
type CategoryNotFoundError struct {
	Requested string
}

func (e *CategoryNotFoundError) Error() string {
	if e == nil {
		return ErrCategoryNotFound.Error()
	}
	return fmt.Sprintf("category %q not found", e.Requested)
}

func (e *CategoryNotFoundError) Is(target error) bool {
	return target == ErrCategoryNotFound
}

// CategoryInactiveError identifies an inactive category.
type CategoryInactiveError struct {
	Category contract.Category
}

func (e *CategoryInactiveError) Error() string {
	if e == nil {
		return ErrCategoryInactive.Error()
	}
	return fmt.Sprintf("category %q is inactive", e.Category.Name)
}

func (e *CategoryInactiveError) Is(target error) bool {
	return target == ErrCategoryInactive
}

type NotFoundError struct {
	Requested string
}

func (e *NotFoundError) Error() string {
	if e == nil {
		return ErrNotFound.Error()
	}
	return fmt.Sprintf("known merchant %q not found", e.Requested)
}

func (e *NotFoundError) Is(target error) bool {
	return target == ErrNotFound
}

type AlreadyExistsError struct {
	KnownMerchant contract.KnownMerchant
}

func (e *AlreadyExistsError) Error() string {
	if e == nil {
		return ErrAlreadyExists.Error()
	}
	return fmt.Sprintf("known merchant %q already exists", e.KnownMerchant.Merchant)
}

func (e *AlreadyExistsError) Is(target error) bool {
	return target == ErrAlreadyExists
}

func normalizeName(value string) string {
	return contract.TrimASCIIWhitespace(value)
}

func validateMerchantName(value string) (string, error) {
	merchantName := normalizeName(value)
	switch {
	case merchantName == "":
		return "", ErrInvalidMerchant
	case strings.ContainsRune(merchantName, '\x00'):
		return "", ErrMerchantContainsNUL
	default:
		return merchantName, nil
	}
}

func merchantFieldIssue(field string, err error) contract.FieldIssue {
	if errors.Is(err, ErrMerchantContainsNUL) {
		return contract.FieldIssue{Field: field, Reason: "must not contain NUL characters"}
	}
	return contract.FieldIssue{Field: field, Reason: "must not be empty"}
}

func validateRenameInputs(merchantName, newMerchantName string) (string, string, *ValidationError) {
	normalizedMerchant, merchantErr := validateMerchantName(merchantName)
	normalizedNewMerchant, newMerchantErr := validateMerchantName(newMerchantName)

	issues := make([]contract.FieldIssue, 0, 2)
	causes := make([]error, 0, 2)
	if merchantErr != nil {
		issues = append(issues, merchantFieldIssue("merchant", merchantErr))
		causes = append(causes, merchantErr)
	}
	if newMerchantErr != nil {
		issues = append(issues, merchantFieldIssue("new_merchant", newMerchantErr))
		causes = append(causes, newMerchantErr)
	}
	if len(issues) == 0 {
		return normalizedMerchant, normalizedNewMerchant, nil
	}
	return normalizedMerchant, normalizedNewMerchant, &ValidationError{Fields: issues, causes: causes}
}

func validateRemoveInput(merchantName string) (string, *ValidationError) {
	normalized, err := validateMerchantName(merchantName)
	if err == nil {
		return normalized, nil
	}
	return normalized, &ValidationError{
		Fields: []contract.FieldIssue{merchantFieldIssue("merchant", err)},
		causes: []error{err},
	}
}

func validateSetInputs(merchantName, categoryName string) (string, string, *ValidationError) {
	merchantName = normalizeName(merchantName)
	categoryName = normalizeName(categoryName)

	issues := make([]contract.FieldIssue, 0, 2)
	causes := make([]error, 0, 2)
	if merchantName == "" {
		issues = append(issues, contract.FieldIssue{Field: "merchant", Reason: "must not be empty"})
		causes = append(causes, ErrInvalidMerchant)
	} else if strings.ContainsRune(merchantName, '\x00') {
		issues = append(issues, contract.FieldIssue{Field: "merchant", Reason: "must not contain NUL characters"})
		causes = append(causes, ErrMerchantContainsNUL)
	}
	if categoryName == "" {
		issues = append(issues, contract.FieldIssue{Field: "category", Reason: "must not be empty"})
		causes = append(causes, ErrInvalidCategory)
	} else if strings.ContainsRune(categoryName, '\x00') {
		issues = append(issues, contract.FieldIssue{Field: "category", Reason: "must not contain NUL characters"})
		causes = append(causes, ErrCategoryContainsNUL)
	}
	if len(issues) == 0 {
		return merchantName, categoryName, nil
	}
	return merchantName, categoryName, &ValidationError{Fields: issues, causes: causes}
}

type listOptions struct {
	query  string
	limit  int64
	offset int64
}

func validateListOptions(options ListOptions) (listOptions, []contract.FieldIssue) {
	query := normalizeName(options.Query)
	issues := make([]contract.FieldIssue, 0, 3)
	if strings.ContainsRune(query, '\x00') {
		issues = append(issues, contract.FieldIssue{Field: "query", Reason: "must not contain NUL characters"})
	}

	limit := DefaultLimit
	if options.Limit != nil {
		limit = *options.Limit
		if limit < 1 || limit > MaxLimit {
			issues = append(issues, contract.FieldIssue{Field: "limit", Reason: "must be between 1 and 200"})
		}
	}

	offset := int64(0)
	if options.Offset != nil {
		offset = *options.Offset
		if offset < 0 {
			issues = append(issues, contract.FieldIssue{Field: "offset", Reason: "must be zero or greater"})
		}
	}

	return listOptions{query: query, limit: limit, offset: offset}, issues
}

func escapeLikeLiteral(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}
