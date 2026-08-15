package contract_test

import (
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

func TestParseDateAcceptsCanonicalRealDates(t *testing.T) {
	tests := []string{
		"0000-01-01",
		"2024-02-29",
		"2000-02-29",
		"2026-08-14",
		"9999-12-31",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			got, err := contract.ParseDate(input)
			if err != nil {
				t.Fatalf("ParseDate(%q) error = %v", input, err)
			}
			if got != input {
				t.Fatalf("ParseDate(%q) = %q, want unchanged canonical value", input, got)
			}
		})
	}
}

func TestParseDateRejectsNonCanonicalAndImpossibleDates(t *testing.T) {
	tests := []string{
		"",
		"2026-8-14",
		"2026-08-4",
		"2026/08/14",
		"2026-02-29",
		"1900-02-29",
		"2026-04-31",
		"2026-00-14",
		"2026-13-14",
		"2026-08-00",
		"2026-08-32",
		" 2026-08-14",
		"2026-08-14 ",
		"+2026-08-14",
		"2026-08-14T00:00:00Z",
		"20a6-08-14",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			got, err := contract.ParseDate(input)
			if err == nil {
				t.Fatalf("ParseDate(%q) = %q, want error", input, got)
			}
			if got != "" {
				t.Fatalf("ParseDate(%q) value on error = %q, want empty string", input, got)
			}
		})
	}
}

func TestParseMonthAcceptsCanonicalRealMonths(t *testing.T) {
	tests := []string{
		"0000-01",
		"2026-01",
		"2026-02",
		"2026-12",
		"9999-12",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			got, err := contract.ParseMonth(input)
			if err != nil {
				t.Fatalf("ParseMonth(%q) error = %v", input, err)
			}
			if got != input {
				t.Fatalf("ParseMonth(%q) = %q, want unchanged canonical value", input, got)
			}
		})
	}
}

func TestParseMonthRejectsNonCanonicalAndInvalidMonths(t *testing.T) {
	tests := []string{
		"",
		"2026-1",
		"2026-00",
		"2026-13",
		"2026/08",
		"2026-08-01",
		" 2026-08",
		"2026-08 ",
		"+2026-08",
		"20a6-08",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			got, err := contract.ParseMonth(input)
			if err == nil {
				t.Fatalf("ParseMonth(%q) = %q, want error", input, got)
			}
			if got != "" {
				t.Fatalf("ParseMonth(%q) value on error = %q, want empty string", input, got)
			}
		})
	}
}

func TestCalendarParsingDoesNotApplyCurrentTimeRules(t *testing.T) {
	if got, err := contract.ParseDate("9999-12-31"); err != nil || got != "9999-12-31" {
		t.Fatalf("ParseDate(future date) = (%q, %v), want canonical future date accepted", got, err)
	}
	if got, err := contract.ParseMonth("9999-12"); err != nil || got != "9999-12" {
		t.Fatalf("ParseMonth(future month) = (%q, %v), want canonical future month accepted", got, err)
	}
}
