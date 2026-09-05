package summary_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/summary"
)

func TestMonthlyMalformedMonthReturnsOnlyCanonicalReason(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	before := loadSnapshot(t, ctx, fx.db)
	want := []contract.FieldIssue{{Field: "month", Reason: "must be a valid YYYY-MM month"}}

	for _, input := range []string{"", "2026-8", "2026-00", "2026-13", "2026/08", "2026-08-01", " 2026-08", "2026-08 "} {
		_, fields, err := fx.summary.Monthly(ctx, input)
		if err != nil {
			t.Fatalf("Monthly(%q) error = %v, want semantic issue", input, err)
		}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("Monthly(%q) fields = %#v, want %#v", input, fields, want)
		}
	}
	assertUnchanged(t, ctx, fx.db, before)
}

func TestCategorySemanticIssuesUseStableFieldOrder(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	before := loadSnapshot(t, ctx, fx.db)

	_, fields, err := fx.summary.Category(ctx, "  \t  ", "2026-8")
	if err != nil {
		t.Fatalf("Category() error = %v, want semantic issues", err)
	}
	want := []contract.FieldIssue{
		{Field: "category", Reason: "must not be empty"},
		{Field: "month", Reason: "must be a valid YYYY-MM month"},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("fields = %#v, want %#v", fields, want)
	}
	assertUnchanged(t, ctx, fx.db, before)
}

func TestCategoryEmptyAndNULNamesAreInvalidInput(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, emptyFields, err := fx.summary.Category(ctx, "   ", "2026-08")
	if err != nil {
		t.Fatalf("empty category error = %v", err)
	}
	if !reflect.DeepEqual(emptyFields, []contract.FieldIssue{{Field: "category", Reason: "must not be empty"}}) {
		t.Fatalf("empty fields = %#v", emptyFields)
	}

	_, nulFields, err := fx.summary.Category(ctx, "Groceries\x00", "2026-08")
	if err != nil {
		t.Fatalf("NUL category error = %v", err)
	}
	if !reflect.DeepEqual(nulFields, []contract.FieldIssue{{Field: "category", Reason: "must not contain NUL characters"}}) {
		t.Fatalf("NUL fields = %#v", nulFields)
	}
}

func TestSpendingSemanticIssuesUseStableFieldOrder(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	before := loadSnapshot(t, ctx, fx.db)

	_, fields, err := fx.summary.Spending(ctx, summary.SpendingInput{
		StartDate: stringPtr("2026-8-31"),
		EndDate:   stringPtr("2026/08/01"),
		Category:  stringPtr(" "),
		Merchant:  stringPtr(" \t "),
	})
	if err != nil {
		t.Fatalf("Spending() error = %v, want semantic issues", err)
	}
	want := []contract.FieldIssue{
		{Field: "start_date", Reason: "must be a valid YYYY-MM-DD date"},
		{Field: "end_date", Reason: "must be a valid YYYY-MM-DD date"},
		{Field: "category", Reason: "must not be empty"},
		{Field: "merchant", Reason: "must not be empty"},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("fields = %#v, want %#v", fields, want)
	}
	assertUnchanged(t, ctx, fx.db, before)
}

func TestSpendingReversedRangeAndMalformedDates(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := fx.summary.Spending(ctx, summary.SpendingInput{
		StartDate: stringPtr("2026-08-31"),
		EndDate:   stringPtr("2026-08-01"),
	})
	if err != nil {
		t.Fatalf("reversed error = %v, want semantic issue", err)
	}
	if !reflect.DeepEqual(fields, []contract.FieldIssue{{Field: "end_date", Reason: "must be on or after start_date"}}) {
		t.Fatalf("reversed fields = %#v", fields)
	}

	_, fields, err = fx.summary.Spending(ctx, summary.SpendingInput{
		StartDate: stringPtr(" 2026-08-01"),
		EndDate:   stringPtr("2026-8-31"),
	})
	if err != nil {
		t.Fatalf("malformed error = %v, want semantic issues", err)
	}
	want := []contract.FieldIssue{
		{Field: "start_date", Reason: "must be a valid YYYY-MM-DD date"},
		{Field: "end_date", Reason: "must be a valid YYYY-MM-DD date"},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("malformed fields = %#v, want %#v", fields, want)
	}
}

func TestSpendingEmptyAndNULMerchantAreInvalidInput(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, emptyFields, err := fx.summary.Spending(ctx, summary.SpendingInput{Merchant: stringPtr("   ")})
	if err != nil {
		t.Fatalf("empty merchant error = %v", err)
	}
	if !reflect.DeepEqual(emptyFields, []contract.FieldIssue{{Field: "merchant", Reason: "must not be empty"}}) {
		t.Fatalf("empty fields = %#v", emptyFields)
	}

	_, nulFields, err := fx.summary.Spending(ctx, summary.SpendingInput{Merchant: stringPtr("Metro\x00")})
	if err != nil {
		t.Fatalf("NUL merchant error = %v", err)
	}
	if !reflect.DeepEqual(nulFields, []contract.FieldIssue{{Field: "merchant", Reason: "must not contain NUL characters"}}) {
		t.Fatalf("NUL fields = %#v", nulFields)
	}
}

func TestPastAndFutureMonthsAreNotSemanticErrors(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := fx.summary.Monthly(ctx, "2025-01")
	if len(fields) != 0 {
		t.Fatalf("past month fields = %#v, want none", fields)
	}
	var notFound *summary.NotFoundError
	if err == nil || !errors.As(err, &notFound) {
		t.Fatalf("past month error = %v, want monthly budget not found", err)
	}

	_, fields, err = fx.summary.Monthly(ctx, "2027-01")
	if len(fields) != 0 {
		t.Fatalf("future month fields = %#v, want none", fields)
	}
	if err == nil || !errors.As(err, &notFound) {
		t.Fatalf("future month error = %v, want monthly budget not found", err)
	}
}
