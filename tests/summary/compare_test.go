package summary_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/jordanp2002/local-finance-mcp/internal/summary"
)

func TestCompareMonthsTotalsCategoriesAndCanonicalNames(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 9, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	dining := createCategory(t, ctx, fx.categories, "Dining")
	health := createCategory(t, ctx, fx.categories, "Health")

	insertBudget(t, ctx, fx.db, groceries.ID, "2026-07", "500.00")
	insertBudget(t, ctx, fx.db, dining.ID, "2026-07", "100.00")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-08", "450.00")
	addTransaction(t, ctx, fx.transactions, "90.00", "Metro", "Groceries", "2026-07-10")
	addTransaction(t, ctx, fx.transactions, "120.00", "Metro", "Groceries", "2026-08-10")
	addTransaction(t, ctx, fx.transactions, "20.00", "Shoppers", "Health", "2026-08-11")
	addTransaction(t, ctx, fx.transactions, "30.00", "Cafe", "Dining", "2026-07-12")
	if _, changed, _, err := fx.categories.Disable(ctx, "Dining"); err != nil || !changed {
		t.Fatalf("Disable(Dining) = changed %v, error %v", changed, err)
	}

	if _, err := fx.db.ExecContext(ctx, `UPDATE categories SET name = ? WHERE id = ?`, "Food & Dining", dining.ID); err != nil {
		t.Fatalf("rename Dining: %v", err)
	}
	before := loadSnapshot(t, ctx, fx.db)

	result, fields, err := fx.summary.Compare(ctx, "2026-07", "2026-08")
	if err != nil || len(fields) != 0 {
		t.Fatalf("Compare() = %#v fields %#v error %v", result, fields, err)
	}
	if result.From != (contract.ComparisonMonth{
		Month: "2026-07", TotalBaseBudget: "600.00", TotalRolloverAdjustment: "0.00", TotalBudget: "600.00", TotalSpending: "120.00", Remaining: "480.00",
	}) {
		t.Fatalf("from = %#v", result.From)
	}
	if result.To != (contract.ComparisonMonth{
		Month: "2026-08", TotalBaseBudget: "450.00", TotalRolloverAdjustment: "0.00", TotalBudget: "450.00", TotalSpending: "140.00", Remaining: "310.00",
	}) {
		t.Fatalf("to = %#v", result.To)
	}
	if result.Change != (contract.ComparisonChange{
		TotalBaseBudget: "-150.00", TotalRolloverAdjustment: "0.00", TotalBudget: "-150.00", TotalSpending: "20.00", Remaining: "-170.00",
	}) {
		t.Fatalf("change = %#v", result.Change)
	}
	wantCategories := []contract.ComparisonCategory{
		{
			CategoryID: dining.ID, Category: "Food & Dining",
			FromBaseBudget: "100.00", ToBaseBudget: "0.00", BaseBudgetChange: "-100.00",
			FromRolloverAdjustment: "0.00", ToRolloverAdjustment: "0.00", RolloverAdjustmentChange: "0.00",
			FromBudget: "100.00", ToBudget: "0.00", BudgetChange: "-100.00",
			FromSpending: "30.00", ToSpending: "0.00", SpendingChange: "-30.00",
		},
		{
			CategoryID: groceries.ID, Category: "Groceries",
			FromBaseBudget: "500.00", ToBaseBudget: "450.00", BaseBudgetChange: "-50.00",
			FromRolloverAdjustment: "0.00", ToRolloverAdjustment: "0.00", RolloverAdjustmentChange: "0.00",
			FromBudget: "500.00", ToBudget: "450.00", BudgetChange: "-50.00",
			FromSpending: "90.00", ToSpending: "120.00", SpendingChange: "30.00",
		},
		{
			CategoryID: health.ID, Category: "Health",
			FromBaseBudget: "0.00", ToBaseBudget: "0.00", BaseBudgetChange: "0.00",
			FromRolloverAdjustment: "0.00", ToRolloverAdjustment: "0.00", RolloverAdjustmentChange: "0.00",
			FromBudget: "0.00", ToBudget: "0.00", BudgetChange: "0.00",
			FromSpending: "0.00", ToSpending: "20.00", SpendingChange: "20.00",
		},
	}
	if !reflect.DeepEqual(result.Categories, wantCategories) {
		t.Fatalf("categories = %#v, want %#v", result.Categories, wantCategories)
	}
	assertUnchanged(t, ctx, fx.db, before)
}

func TestCompareMonthsValidationOrderAndRelationship(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 9, 15, 12, 0))
	wantMalformed := []contract.FieldIssue{
		{Field: "from_month", Reason: "must be a valid YYYY-MM month"},
		{Field: "to_month", Reason: "must be a valid YYYY-MM month"},
	}
	_, fields, err := fx.summary.Compare(ctx, "2026-7", "2026-13")
	if err != nil || !reflect.DeepEqual(fields, wantMalformed) {
		t.Fatalf("malformed fields = %#v, error %v; want %#v", fields, err, wantMalformed)
	}

	wantRelationship := []contract.FieldIssue{{Field: "to_month", Reason: "must be later than from_month"}}
	for _, tc := range []struct {
		from string
		to   string
	}{
		{from: "2026-08", to: "2026-08"},
		{from: "2026-09", to: "2026-08"},
	} {
		_, fields, err := fx.summary.Compare(ctx, tc.from, tc.to)
		if err != nil || !reflect.DeepEqual(fields, wantRelationship) {
			t.Fatalf("Compare(%q, %q) fields = %#v, error %v; want %#v", tc.from, tc.to, fields, err, wantRelationship)
		}
	}
}

func TestCompareMonthsMissingSnapshotsAreCheckedInOrder(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 9, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-07", "100.00")

	_, fields, err := fx.summary.Compare(ctx, "2026-06", "2026-08")
	if len(fields) != 0 {
		t.Fatalf("from-missing fields = %#v, want none", fields)
	}
	var notFound *summary.NotFoundError
	if err == nil || !errors.As(err, &notFound) {
		t.Fatalf("from-missing error = %v, want NotFoundError", err)
	}
	if notFound.Month != "2026-06" || notFound.LatestEarlierMonth != nil {
		t.Fatalf("from-missing not found = %#v", notFound)
	}

	_, fields, err = fx.summary.Compare(ctx, "2026-07", "2026-08")
	if len(fields) != 0 {
		t.Fatalf("to-missing fields = %#v, want none", fields)
	}
	if err == nil || !errors.As(err, &notFound) {
		t.Fatalf("to-missing error = %v, want NotFoundError", err)
	}
	if notFound.Month != "2026-08" || notFound.LatestEarlierMonth == nil || *notFound.LatestEarlierMonth != "2026-07" {
		t.Fatalf("to-missing not found = %#v", notFound)
	}
}

func TestCompareMonthsEmptyCategoriesIsNonNil(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 9, 15, 12, 0))
	health := createCategory(t, ctx, fx.categories, "Health")
	insertBudget(t, ctx, fx.db, health.ID, "2026-07", "0.00")
	insertBudget(t, ctx, fx.db, health.ID, "2026-08", "0.00")

	result, fields, err := fx.summary.Compare(ctx, "2026-07", "2026-08")
	if err != nil || len(fields) != 0 {
		t.Fatalf("Compare() = %#v fields %#v error %v", result, fields, err)
	}
	if result.Categories == nil || len(result.Categories) != 0 {
		t.Fatalf("categories = %#v, want non-nil empty slice", result.Categories)
	}
	encoded, err := json.Marshal(result.Categories)
	if err != nil || string(encoded) != "[]" {
		t.Fatalf("categories JSON = %s, %v; want []", encoded, err)
	}
}

func TestCompareMonthsRemainingChangeOverflowIsInternalError(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 9, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-07", "0.00")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-08", "92233720368547758.07")
	insertRawTransaction(t, ctx, fx.db, "Huge", math.MaxInt64, "2026-07-01", groceries.ID)
	before := loadSnapshot(t, ctx, fx.db)

	_, fields, err := fx.summary.Compare(ctx, "2026-07", "2026-08")
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	if err == nil {
		t.Fatal("Compare() overflow error = nil")
	}
	var notFound *summary.NotFoundError
	if errors.As(err, &notFound) {
		t.Fatalf("overflow mapped to not found: %v", err)
	}
	assertUnchanged(t, ctx, fx.db, before)
}
