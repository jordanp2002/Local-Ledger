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
