package category

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidName     = errors.New("invalid category name")
	ErrNameContainsNUL = fmt.Errorf("%w: contains NUL", ErrInvalidName)
	ErrAlreadyExists   = errors.New("category already exists")
	ErrNotFound        = errors.New("category not found")
)

const asciiWhitespace = " \t\n\r\v\f"

// NormalizeName trims only ASCII whitespace. Unicode spaces remain part of the name.
func NormalizeName(name string) string {
	return strings.Trim(name, asciiWhitespace)
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
