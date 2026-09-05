package summary_test

import (
	"math"
	"reflect"
	"testing"

	"github.com/jordanp2002/Local-Ledger/internal/summary"
)

func TestSpentOfBudgetFormula(t *testing.T) {
	tests := []struct {
		name     string
		spending int64
		budget   int64
		want     *string
		wantErr  bool
	}{
		{name: "82.50 exact", spending: 8250, budget: 10000, want: stringPtr("82.50")},
		{name: "100 percent", spending: 10000, budget: 10000, want: stringPtr("100.00")},
		{name: "over 100", spending: 11025, budget: 10000, want: stringPtr("110.25")},
		{name: "zero spending with positive budget", spending: 0, budget: 10000, want: stringPtr("0.00")},
		{name: "zero budget with spending", spending: 2500, budget: 0, want: nil},
		{name: "zero budget without spending", spending: 0, budget: 0, want: nil},
		{name: "one third truncates toward zero", spending: 100, budget: 300, want: stringPtr("33.33")},
		{name: "multiply overflow", spending: math.MaxInt64, budget: 1, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := summary.SpentOfBudget(tt.spending, tt.budget)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("SpentOfBudget(%d, %d) = %v, nil error; want overflow", tt.spending, tt.budget, spentDebug(got))
				}
				if got != nil {
					t.Fatalf("SpentOfBudget overflow returned %v, want nil percent", spentDebug(got))
				}
				return
			}
			if err != nil {
				t.Fatalf("SpentOfBudget(%d, %d) error = %v", tt.spending, tt.budget, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("SpentOfBudget(%d, %d) = %s, want %s", tt.spending, tt.budget, spentDebug(got), spentDebug(tt.want))
			}
		})
	}
}

func spentDebug(value *string) string {
	if value == nil {
		return "null"
	}
	return *value
}
