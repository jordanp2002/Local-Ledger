package category

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidName   = errors.New("invalid category name")
	ErrAlreadyExists = errors.New("category already exists")
	ErrNotFound      = errors.New("category not found")
)

const asciiWhitespace = " \t\n\r\v\f"

// NormalizeName removes surrounding ASCII whitespace from a category name.
func NormalizeName(name string) string {
	return strings.Trim(name, asciiWhitespace)
}

// LocalMonth returns YYYY-MM in t's location.
func LocalMonth(t time.Time) string {
	return t.Format("2006-01")
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
