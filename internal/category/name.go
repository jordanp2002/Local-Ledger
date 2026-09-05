package category

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
)

var (
	ErrInvalidName     = errors.New("invalid category name")
	ErrNameContainsNUL = fmt.Errorf("%w: contains NUL", ErrInvalidName)
	ErrAlreadyExists   = errors.New("category already exists")
	ErrNotFound        = errors.New("category not found")
)

type ValidationError struct {
	Fields []contract.FieldIssue
	causes []error
}

func (e *ValidationError) Error() string {
	return "invalid category input"
}

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

type AlreadyExistsError struct {
	Category contract.Category
}

func (e *AlreadyExistsError) Error() string {
	if e == nil {
		return ErrAlreadyExists.Error()
	}
	return fmt.Sprintf("category %q already exists", e.Category.Name)
}

func (e *AlreadyExistsError) Is(target error) bool {
	return target == ErrAlreadyExists
}

// NormalizeName trims only ASCII whitespace. Unicode spaces remain part of the name.
func NormalizeName(name string) string {
	return contract.TrimASCIIWhitespace(name)
}

func validateName(name string) (string, error) {
	normalized := NormalizeName(name)
	if normalized == "" {
		return "", ErrInvalidName
	}
	if strings.ContainsRune(normalized, '\x00') {
		return "", ErrNameContainsNUL
	}
	return normalized, nil
}

func validateRenameInputs(name, newName string) (string, string, *ValidationError) {
	normalizedName, nameIssue, nameCause := validateRenameName("category", name)
	normalizedNewName, newNameIssue, newNameCause := validateRenameName("new_name", newName)

	issues := make([]contract.FieldIssue, 0, 2)
	causes := make([]error, 0, 2)
	if nameIssue != nil {
		issues = append(issues, *nameIssue)
		causes = append(causes, nameCause)
	}
	if newNameIssue != nil {
		issues = append(issues, *newNameIssue)
		causes = append(causes, newNameCause)
	}
	if len(issues) == 0 {
		return normalizedName, normalizedNewName, nil
	}
	return normalizedName, normalizedNewName, &ValidationError{Fields: issues, causes: causes}
}

func validateRenameName(field, name string) (string, *contract.FieldIssue, error) {
	normalized := NormalizeName(name)
	switch {
	case normalized == "":
		return "", &contract.FieldIssue{Field: field, Reason: "must not be empty"}, ErrInvalidName
	case strings.ContainsRune(normalized, '\x00'):
		return "", &contract.FieldIssue{Field: field, Reason: "must not contain NUL characters"}, ErrNameContainsNUL
	default:
		return normalized, nil, nil
	}
}
