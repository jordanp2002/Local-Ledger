// Package contract defines stable finance-tool values.
package contract

import (
	"fmt"
	"time"
)

const (
	dateLayout  = "2006-01-02"
	monthLayout = "2006-01"
)

// ParseDate validates a YYYY-MM-DD date and returns it unchanged.
// Current-date policies are enforced by callers.
func ParseDate(value string) (string, error) {
	parsed, err := time.Parse(dateLayout, value)
	if err != nil {
		return "", fmt.Errorf("invalid date %q: %w", value, err)
	}
	if parsed.Format(dateLayout) != value {
		return "", fmt.Errorf("invalid date %q: non-canonical form", value)
	}
	return value, nil
}

// ParseMonth validates a YYYY-MM month and returns it unchanged.
// Current-month policies are enforced by callers.
func ParseMonth(value string) (string, error) {
	parsed, err := time.Parse(monthLayout, value)
	if err != nil {
		return "", fmt.Errorf("invalid month %q: %w", value, err)
	}
	if parsed.Format(monthLayout) != value {
		return "", fmt.Errorf("invalid month %q: non-canonical form", value)
	}
	return value, nil
}

// MonthDateRange returns the inclusive YYYY-MM-DD start and end dates for a
// canonical YYYY-MM month. It does not apply current-month policy.
func MonthDateRange(month string) (string, string, error) {
	parsed, err := ParseMonth(month)
	if err != nil {
		return "", "", err
	}
	start, err := time.Parse(monthLayout, parsed)
	if err != nil {
		return "", "", fmt.Errorf("invalid month %q: %w", month, err)
	}
	end := start.AddDate(0, 1, -1)
	return start.Format(dateLayout), end.Format(dateLayout), nil
}

// NextMonth returns the canonical calendar month immediately after month.
func NextMonth(month string) (string, error) {
	parsed, err := ParseMonth(month)
	if err != nil {
		return "", err
	}
	value, err := time.Parse(monthLayout, parsed)
	if err != nil {
		return "", fmt.Errorf("invalid month %q: %w", month, err)
	}
	next := value.AddDate(0, 1, 0).Format(monthLayout)
	if _, err := ParseMonth(next); err != nil {
		return "", fmt.Errorf("month %q has no representable next month: %w", month, err)
	}
	return next, nil
}
