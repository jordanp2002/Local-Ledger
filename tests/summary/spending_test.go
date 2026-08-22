package summary_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/jordanp2002/local-finance-mcp/internal/summary"
)

func TestSpendingAllTimeAndInclusiveDateBounds(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 9, 15, 12, 0))
	createCategory(t, ctx, fx.categories, "Groceries")
	addTransaction(t, ctx, fx.transactions, "1.00", "Jul", "Groceries", "2026-07-31")
	addTransaction(t, ctx, fx.transactions, "2.00", "Aug1", "Groceries", "2026-08-01")
	addTransaction(t, ctx, fx.transactions, "3.00", "Aug31", "Groceries", "2026-08-31")
	addTransaction(t, ctx, fx.transactions, "4.00", "Sep", "Groceries", "2026-09-01")

	allTime := mustSpending(t, ctx, fx.summary, summary.SpendingInput{})
	if allTime.TotalSpending != "10.00" || allTime.TransactionCount != 4 {
		t.Fatalf("all-time = %#v, want 10.00 from four rows", allTime)
	}
	if allTime.StartDate != nil || allTime.EndDate != nil || allTime.Category != nil || allTime.Merchant != nil {
		t.Fatalf("omitted filters = %#v, want nulls", allTime)
	}

	startOnly := mustSpending(t, ctx, fx.summary, summary.SpendingInput{StartDate: stringPtr("2026-08-01")})
	if startOnly.TotalSpending != "9.00" || startOnly.TransactionCount != 3 || *startOnly.StartDate != "2026-08-01" {
		t.Fatalf("start-only = %#v, want 9.00 from on-bound and later", startOnly)
	}

	endOnly := mustSpending(t, ctx, fx.summary, summary.SpendingInput{EndDate: stringPtr("2026-08-01")})
	if endOnly.TotalSpending != "3.00" || endOnly.TransactionCount != 2 || *endOnly.EndDate != "2026-08-01" {
		t.Fatalf("end-only = %#v, want 3.00 from on-bound and earlier", endOnly)
	}

	both := mustSpending(t, ctx, fx.summary, summary.SpendingInput{
		StartDate: stringPtr("2026-08-01"),
		EndDate:   stringPtr("2026-08-31"),
	})
	if both.TotalSpending != "5.00" || both.TransactionCount != 2 {
		t.Fatalf("August range = %#v, want 5.00 from inclusive endpoints", both)
	}
	if *both.StartDate != "2026-08-01" || *both.EndDate != "2026-08-31" {
		t.Fatalf("echoed dates = %#v", both)
	}
}

func TestSpendingCategoryFilterIncludesInactive(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, fx.categories, "Groceries")
	dining := createCategory(t, ctx, fx.categories, "Dining")
	addTransaction(t, ctx, fx.transactions, "20.00", "Metro", "Groceries", "2026-08-14")
	addTransaction(t, ctx, fx.transactions, "15.00", "Cafe", "Dining", "2026-08-13")
	if _, changed, _, err := fx.categories.Disable(ctx, "Dining"); err != nil || !changed {
		t.Fatalf("Disable(Dining) = changed %v, error %v", changed, err)
	}

	allTime := mustSpending(t, ctx, fx.summary, summary.SpendingInput{})
	if allTime.TotalSpending != "35.00" || allTime.TransactionCount != 2 {
		t.Fatalf("all-time after disable = %#v, want inactive spending included", allTime)
	}

	filtered := mustSpending(t, ctx, fx.summary, summary.SpendingInput{Category: stringPtr("dining")})
	if filtered.TotalSpending != "15.00" || filtered.TransactionCount != 1 {
		t.Fatalf("inactive filter totals = %#v, want Dining 15.00", filtered)
	}
	if filtered.Category == nil || *filtered.Category != "Dining" {
		t.Fatalf("echoed category = %#v, want stored Dining spelling", filtered.Category)
	}
	if len(filtered.Categories) != 1 || filtered.Categories[0].CategoryID != dining.ID || filtered.Categories[0].Spending != "15.00" {
		t.Fatalf("inactive filter rows = %#v, want Dining", filtered.Categories)
	}
}

func TestSpendingMissingCategoryIsNotFound(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	before := loadSnapshot(t, ctx, fx.db)

	_, fields, err := fx.summary.Spending(ctx, summary.SpendingInput{Category: stringPtr(" Pharmacy ")})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	var missing *summary.CategoryNotFoundError
	if !errors.As(err, &missing) || missing.Requested != "Pharmacy" {
		t.Fatalf("error = %v, want CategoryNotFoundError for Pharmacy", err)
	}
	if len(missing.ActiveCategories) != 1 || missing.ActiveCategories[0].ID != groceries.ID {
		t.Fatalf("recovery = %#v, want Groceries", missing.ActiveCategories)
	}
	assertUnchanged(t, ctx, fx.db, before)
}

func TestSpendingMerchantExactCaseInsensitiveMatchAndMismatch(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, fx.categories, "Groceries")
	addTransaction(t, ctx, fx.transactions, "20.00", "Metro", "Groceries", "2026-08-14")
	addTransaction(t, ctx, fx.transactions, "12.00", "Metro Grocery", "Groceries", "2026-08-13")

	matched := mustSpending(t, ctx, fx.summary, summary.SpendingInput{Merchant: stringPtr(" metro ")})
	if matched.TotalSpending != "20.00" || matched.TransactionCount != 1 {
		t.Fatalf("exact merchant = %#v, want Metro 20.00 without substring hit", matched)
	}
	if matched.Merchant == nil || *matched.Merchant != "metro" {
		t.Fatalf("echoed merchant = %#v, want trimmed submitted spelling", matched.Merchant)
	}

	mismatch := mustSpending(t, ctx, fx.summary, summary.SpendingInput{Merchant: stringPtr("Unknown Store")})
	if mismatch.TotalSpending != "0.00" || mismatch.TransactionCount != 0 || len(mismatch.Categories) != 0 {
		t.Fatalf("mismatch = %#v, want zeros", mismatch)
	}
	if mismatch.Merchant == nil || *mismatch.Merchant != "Unknown Store" {
		t.Fatalf("echoed unknown merchant = %#v", mismatch.Merchant)
	}
}

func TestSpendingCombinedFilters(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 9, 15, 12, 0))
	createCategory(t, ctx, fx.categories, "Groceries")
	createCategory(t, ctx, fx.categories, "Dining")
	addTransaction(t, ctx, fx.transactions, "10.00", "Metro", "Groceries", "2026-07-31")
	addTransaction(t, ctx, fx.transactions, "20.00", "Metro", "Groceries", "2026-08-14")
	addTransaction(t, ctx, fx.transactions, "15.00", "Metro", "Dining", "2026-08-14")
	addTransaction(t, ctx, fx.transactions, "8.00", "Cafe", "Groceries", "2026-08-14")
	addTransaction(t, ctx, fx.transactions, "9.00", "Metro", "Groceries", "2026-09-01")

	result := mustSpending(t, ctx, fx.summary, summary.SpendingInput{
		StartDate: stringPtr("2026-08-01"),
		EndDate:   stringPtr("2026-08-31"),
		Category:  stringPtr("groceries"),
		Merchant:  stringPtr("metro"),
	})
	if result.TotalSpending != "20.00" || result.TransactionCount != 1 {
		t.Fatalf("AND filter = %#v, want August Groceries Metro", result)
	}
	if len(result.Categories) != 1 || result.Categories[0].Category != "Groceries" || result.Categories[0].Spending != "20.00" {
		t.Fatalf("AND filter rows = %#v", result.Categories)
	}
}

func TestSpendingEmptyLedgerReturnsZeros(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	before := loadSnapshot(t, ctx, fx.db)

	result := mustSpending(t, ctx, fx.summary, summary.SpendingInput{})
	if result.TotalSpending != "0.00" || result.TransactionCount != 0 {
		t.Fatalf("empty ledger = %#v, want zeros", result)
	}
	encoded, err := json.Marshal(result.Categories)
	if err != nil || string(encoded) != "[]" {
		t.Fatalf("empty categories JSON = %s, %v; want []", encoded, err)
	}
	assertUnchanged(t, ctx, fx.db, before)
}

func TestSpendingUnbudgetedSpendingAndCategoryOrder(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	dining := createCategory(t, ctx, fx.categories, "Dining")
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	health := createCategory(t, ctx, fx.categories, "Health")
	addTransaction(t, ctx, fx.transactions, "90.00", "Metro", "Groceries", "2026-08-14")
	addTransaction(t, ctx, fx.transactions, "30.00", "Cafe", "Dining", "2026-08-15")
	addTransaction(t, ctx, fx.transactions, "25.00", "Shoppers", "Health", "2026-08-11")
	addTransaction(t, ctx, fx.transactions, "10.00", "Cafe", "Dining", "2026-08-10")

	result := mustSpending(t, ctx, fx.summary, summary.SpendingInput{})
	if result.TotalSpending != "155.00" || result.TransactionCount != 4 {
		t.Fatalf("unbudgeted totals = %#v, want 155.00", result)
	}
	want := []contract.SpendingSummaryCategory{
		{CategoryID: dining.ID, Category: "Dining", Spending: "40.00", TransactionCount: 2},
		{CategoryID: groceries.ID, Category: "Groceries", Spending: "90.00", TransactionCount: 1},
		{CategoryID: health.ID, Category: "Health", Spending: "25.00", TransactionCount: 1},
	}
	if len(result.Categories) != 3 {
		t.Fatalf("categories = %#v, want Dining, Groceries, Health", result.Categories)
	}
	for i, row := range want {
		if result.Categories[i] != row {
			t.Fatalf("categories[%d] = %#v, want %#v", i, result.Categories[i], row)
		}
	}
}

func TestSpendingDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, fx.categories, "Groceries")
	addTransaction(t, ctx, fx.transactions, "20.00", "Metro", "Groceries", "2026-08-14")
	before := loadSnapshot(t, ctx, fx.db)

	mustSpending(t, ctx, fx.summary, summary.SpendingInput{
		StartDate: stringPtr("2026-08-01"),
		EndDate:   stringPtr("2026-08-31"),
		Category:  stringPtr("Groceries"),
		Merchant:  stringPtr("Metro"),
	})
	assertUnchanged(t, ctx, fx.db, before)
}

func TestSpendingOverflowIsInternalErrorAndDoesNotWrap(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	insertRawTransaction(t, ctx, fx.db, "Huge", math.MaxInt64, "2026-08-01", groceries.ID)
	insertRawTransaction(t, ctx, fx.db, "One", 1, "2026-08-02", groceries.ID)
	before := loadSnapshot(t, ctx, fx.db)

	_, fields, err := fx.summary.Spending(ctx, summary.SpendingInput{})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	if err == nil {
		t.Fatal("Spending() overflow error = nil")
	}
	var missing *summary.CategoryNotFoundError
	if errors.As(err, &missing) {
		t.Fatalf("overflow mapped to category_not_found: %v", err)
	}
	assertUnchanged(t, ctx, fx.db, before)
}
