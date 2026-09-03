package contract_test

import (
	"math"
	"strconv"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

func TestParseAmountAcceptsDecimalPrecisionAndNormalizesToHundredths(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int64
	}{
		{name: "whole number", input: "20", want: 2000},
		{name: "one fractional digit", input: "20.5", want: 2050},
		{name: "two fractional digits", input: "20.50", want: 2050},
		{name: "zero", input: "0.00", want: 0},
		{name: "zero with one fractional digit", input: "0.0", want: 0},
		{name: "leading zeroes", input: "00020.5", want: 2050},
		{name: "one hundredth", input: "0.01", want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := contract.ParseAmount(tt.input)
			if err != nil {
				t.Fatalf("ParseAmount(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ParseAmount(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseAmountRejectsInvalidSyntax(t *testing.T) {
	tests := []string{
		"",
		" ",
		"20 ",
		" 20",
		"20.5 ",
		"+20",
		"-20",
		"-0.01",
		".5",
		"20.",
		"20.500",
		"20..50",
		"20.5.0",
		"1,000",
		"1_000",
		"1e2",
		"NaN",
		"１２",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			if got, err := contract.ParseAmount(input); err == nil {
				t.Fatalf("ParseAmount(%q) = %d, nil error; want rejection", input, got)
			}
		})
	}
}

func TestParseAmountRejectsValuesThatOverflowInt64Hundredths(t *testing.T) {
	tests := []string{
		"92233720368547758.08",
		"92233720368547759",
		"9223372036854775807",
		"99999999999999999999999999999999999999999999999999.99",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			if got, err := contract.ParseAmount(input); err == nil {
				t.Fatalf("ParseAmount(%q) = %d, nil error; want overflow rejection", input, got)
			}
		})
	}
}

func TestParseAmountAcceptsMaximumInt64Hundredths(t *testing.T) {
	const input = "92233720368547758.07"

	got, err := contract.ParseAmount(input)
	if err != nil {
		t.Fatalf("ParseAmount(%q) error = %v", input, err)
	}
	if got != math.MaxInt64 {
		t.Fatalf("ParseAmount(%q) = %d, want %d", input, got, int64(math.MaxInt64))
	}
}

func TestFormatAmountAlwaysUsesTwoFractionalDigits(t *testing.T) {
	tests := []struct {
		name       string
		hundredths int64
		want       string
	}{
		{name: "zero", hundredths: 0, want: "0.00"},
		{name: "one hundredth", hundredths: 1, want: "0.01"},
		{name: "five cents", hundredths: 5, want: "0.05"},
		{name: "one dollar", hundredths: 100, want: "1.00"},
		{name: "one fractional digit", hundredths: 2050, want: "20.50"},
		{name: "maximum", hundredths: math.MaxInt64, want: "92233720368547758.07"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := contract.FormatAmount(tt.hundredths)
			if err != nil {
				t.Fatalf("FormatAmount(%d) error = %v", tt.hundredths, err)
			}
			if got != tt.want {
				t.Fatalf("FormatAmount(%d) = %q, want %q", tt.hundredths, got, tt.want)
			}
		})
	}
}

func TestFormatAmountRejectsNegativeHundredths(t *testing.T) {
	for _, hundredths := range []int64{-1, math.MinInt64} {
		t.Run(strconv.FormatInt(hundredths, 10), func(t *testing.T) {
			got, err := contract.FormatAmount(hundredths)
			if err == nil {
				t.Fatalf("FormatAmount(%d) = %q, nil error; want rejection", hundredths, got)
			}
			if got != "" {
				t.Fatalf("FormatAmount(%d) = %q on error, want empty string", hundredths, got)
			}
		})
	}
}

func TestFormatSignedAmountFormatsZeroPositiveAndNegative(t *testing.T) {
	tests := []struct {
		name       string
		hundredths int64
		want       string
	}{
		{name: "zero", hundredths: 0, want: "0.00"},
		{name: "one hundredth", hundredths: 1, want: "0.01"},
		{name: "ten fifty", hundredths: 1050, want: "10.50"},
		{name: "negative one hundredth", hundredths: -1, want: "-0.01"},
		{name: "negative ten fifty", hundredths: -1050, want: "-10.50"},
		{name: "maximum", hundredths: math.MaxInt64, want: "92233720368547758.07"},
		{name: "negative maximum", hundredths: -math.MaxInt64, want: "-92233720368547758.07"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := contract.FormatSignedAmount(tt.hundredths)
			if err != nil {
				t.Fatalf("FormatSignedAmount(%d) error = %v", tt.hundredths, err)
			}
			if got != tt.want {
				t.Fatalf("FormatSignedAmount(%d) = %q, want %q", tt.hundredths, got, tt.want)
			}
		})
	}
}

func TestFormatSignedAmountRejectsMinInt64(t *testing.T) {
	got, err := contract.FormatSignedAmount(math.MinInt64)
	if err == nil {
		t.Fatalf("FormatSignedAmount(MinInt64) = %q, nil error; want rejection", got)
	}
	if got != "" {
		t.Fatalf("FormatSignedAmount(MinInt64) = %q on error, want empty string", got)
	}
}

func TestFormatAmountStillRejectsNegativesAfterSignedFormatter(t *testing.T) {
	got, err := contract.FormatAmount(-1)
	if err == nil {
		t.Fatalf("FormatAmount(-1) = %q, nil error; want rejection", got)
	}
	if got != "" {
		t.Fatalf("FormatAmount(-1) = %q on error, want empty string", got)
	}
}

func TestParseAmountStillRejectsSignedStrings(t *testing.T) {
	for _, input := range []string{"-20", "-0.01", "+20"} {
		if got, err := contract.ParseAmount(input); err == nil {
			t.Fatalf("ParseAmount(%q) = %d, nil error; want rejection", input, got)
		}
	}
}

func TestParseSignedAmountAcceptsAndNormalizesSignedDecimals(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{input: "20.5", want: 2050},
		{input: "0.00", want: 0},
		{input: "-0.00", want: 0},
		{input: "-20.5", want: -2050},
		{input: "92233720368547758.07", want: math.MaxInt64},
		{input: "-92233720368547758.07", want: -math.MaxInt64},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := contract.ParseSignedAmount(tt.input)
			if err != nil {
				t.Fatalf("ParseSignedAmount(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ParseSignedAmount(%q) = %d, want %d", tt.input, got, tt.want)
			}
			formatted, err := contract.FormatSignedAmount(got)
			if err != nil {
				t.Fatalf("FormatSignedAmount(%d) error = %v", got, err)
			}
			roundTripped, err := contract.ParseSignedAmount(formatted)
			if err != nil || roundTripped != got {
				t.Fatalf("signed round trip = %d, %v; want %d", roundTripped, err, got)
			}
		})
	}
}

func TestParseSignedAmountRejectsInvalidAndOverflowingDecimals(t *testing.T) {
	for _, input := range []string{
		"",
		"+20.00",
		"-",
		"-20.000",
		"-92233720368547758.08",
		"92233720368547758.08",
	} {
		t.Run(input, func(t *testing.T) {
			if got, err := contract.ParseSignedAmount(input); err == nil {
				t.Fatalf("ParseSignedAmount(%q) = %d, nil error; want rejection", input, got)
			}
		})
	}
}

func TestFormatAmountRoundTripsThroughParseAmount(t *testing.T) {
	tests := []int64{0, 1, 7, 50, 99, 100, 2050, 123456789, math.MaxInt64}

	for _, want := range tests {
		t.Run(strconv.FormatInt(want, 10), func(t *testing.T) {
			formatted, err := contract.FormatAmount(want)
			if err != nil {
				t.Fatalf("FormatAmount(%d) error = %v", want, err)
			}
			got, err := contract.ParseAmount(formatted)
			if err != nil {
				t.Fatalf("ParseAmount(FormatAmount(%d)) error = %v", want, err)
			}
			if got != want {
				t.Fatalf("ParseAmount(FormatAmount(%d)) = %d, want %d", want, got, want)
			}
		})
	}
}
