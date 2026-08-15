// Package contract defines stable finance-tool values.
package contract

import (
	"errors"
	"math"
	"strconv"
	"strings"
)

// ErrInvalidAmount reports an invalid non-negative amount.
var ErrInvalidAmount = errors.New("invalid amount")

const (
	maxAmountWhole      = int64(math.MaxInt64 / 100)
	maxAmountFractional = int64(math.MaxInt64 % 100)
)

// ParseAmount converts a non-negative decimal with up to two fractional digits
// into integer hundredths.
func ParseAmount(value string) (int64, error) {
	if value == "" {
		return 0, ErrInvalidAmount
	}

	integerPart := value
	fractionalPart := ""
	if separator := strings.IndexByte(value, '.'); separator >= 0 {
		// A second decimal point is rejected by the digit check below, but
		// keeping the split explicit makes the accepted grammar clear.
		integerPart = value[:separator]
		fractionalPart = value[separator+1:]
		if len(fractionalPart) == 0 || len(fractionalPart) > 2 {
			return 0, ErrInvalidAmount
		}
	}

	if integerPart == "" {
		return 0, ErrInvalidAmount
	}

	whole, ok := parseWholeAmount(integerPart)
	if !ok {
		return 0, ErrInvalidAmount
	}

	fraction, ok := parseFractionalAmount(fractionalPart)
	if !ok {
		return 0, ErrInvalidAmount
	}
	if whole > maxAmountWhole || (whole == maxAmountWhole && fraction > maxAmountFractional) {
		return 0, ErrInvalidAmount
	}

	return whole*100 + fraction, nil
}

// FormatAmount formats non-negative integer hundredths with two fractional
// digits. Callers enforce additional rules such as whether zero is allowed.
func FormatAmount(hundredths int64) (string, error) {
	if hundredths < 0 {
		return "", ErrInvalidAmount
	}

	whole := hundredths / 100
	fraction := hundredths % 100
	return strconv.FormatInt(whole, 10) + "." + twoDigits(fraction), nil
}

func parseWholeAmount(value string) (int64, bool) {
	var whole int64
	for i := 0; i < len(value); i++ {
		digit, ok := decimalDigit(value[i])
		if !ok {
			return 0, false
		}
		if whole > maxAmountWhole/10 || (whole == maxAmountWhole/10 && int64(digit) > maxAmountWhole%10) {
			return 0, false
		}
		whole = whole*10 + int64(digit)
	}
	return whole, true
}

func parseFractionalAmount(value string) (int64, bool) {
	if value == "" {
		return 0, true
	}

	first, ok := decimalDigit(value[0])
	if !ok {
		return 0, false
	}
	if len(value) == 1 {
		return int64(first) * 10, true
	}

	second, ok := decimalDigit(value[1])
	if !ok {
		return 0, false
	}
	return int64(first)*10 + int64(second), true
}

func decimalDigit(value byte) (byte, bool) {
	if value < '0' || value > '9' {
		return 0, false
	}
	return value - '0', true
}

func twoDigits(value int64) string {
	if value < 10 {
		return "0" + strconv.FormatInt(value, 10)
	}
	return strconv.FormatInt(value, 10)
}
