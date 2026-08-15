package category

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

var (
	ErrInvalidName     = errors.New("invalid category name")
	ErrNameContainsNUL = fmt.Errorf("%w: contains NUL", ErrInvalidName)
	ErrAlreadyExists   = errors.New("category already exists")
	ErrNotFound        = errors.New("category not found")
)

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
