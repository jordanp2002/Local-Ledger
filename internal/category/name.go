package category

import (
	"errors"
	"strings"
)

var (
	ErrInvalidName   = errors.New("invalid category name")
	ErrAlreadyExists = errors.New("category already exists")
	ErrNotFound      = errors.New("category not found")
)

const asciiWhitespace = " \t\n\r\v\f"

// NormalizeName trims only ASCII whitespace. Unicode spaces remain part of the name.
func NormalizeName(name string) string {
	return strings.Trim(name, asciiWhitespace)
}
