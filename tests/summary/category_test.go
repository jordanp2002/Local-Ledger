package summary_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/summary"
)

func TestCategorySummaryBudgetedPurchases(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	createCategory(t, ctx, fx.categories, "Dining")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-08", "500.00")
	addTransaction(t, ctx, fx.transactions, "20.00", "Metro", "Groceries", "2026-08-01")
	addTransaction(t, ctx, fx.transactions, "30.00", "No Frills", "Groceries", "2026-08-14")
	addTransaction(t, ctx, fx.transactions, "40.00", "Metro", "Groceries", "2026-08-15")
	addTransaction(t, ctx, fx.transactions, "15.00", "Cafe", "Dining", "2026-08-14")
	before := loadSnapshot(t, ctx, fx.db)

	result := mustCategory(t, ctx, fx.summary, " groceries ", "2026-08")
	if result.CategoryID != groceries.ID || result.Category != "Groceries" || result.Month != "2026-08" {
		t.Fatalf("identity = %#v", result)
	}
	if result.Budget != "500.00" || result.TotalSpending != "90.00" || result.Remaining != "410.00" || result.TransactionCount != 3 {
		t.Fatalf("totals = %#v", result)
	}
	assertUnchanged(t, ctx, fx.db, before)
}

func TestCategorySummaryAllowsInactiveHistory(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 10, 15, 12, 0))
	dining := createCategory(t, ctx, fx.categories, "Dining")
	insertBudget(t, ctx, fx.db, dining.ID, "2026-08", "150.00")
	addTransaction(t, ctx, fx.transactions, "40.00", "Cafe", "Dining", "2026-08-10")
	if _, changed, _, err := fx.categories.Disable(ctx, "Dining"); err != nil || !changed {
		t.Fatalf("Disable(Dining) = changed %v, error %v", changed, err)
	}

	result := mustCategory(t, ctx, fx.summary, "dining", "2026-08")
	if result.Category != "Dining" || result.Budget != "150.00" || result.TotalSpending != "40.00" || result.TransactionCount != 1 {
		t.Fatalf("inactive history = %#v", result)
	}
}

func TestCategoryMissingNameAfterSnapshotCheck(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-08", "500.00")

	_, fields, err := fx.summary.Category(ctx, "  Pharmacy  ", "2026-08")
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	var notFound *summary.CategoryNotFoundError
	if err == nil || !errors.As(err, &notFound) {
		t.Fatalf("error = %v, want CategoryNotFoundError", err)
	}
	if notFound.Requested != "Pharmacy" {
		t.Fatalf("requested = %q, want Pharmacy", notFound.Requested)
	}
	if len(notFound.ActiveCategories) != 1 || notFound.ActiveCategories[0].Name != "Groceries" {
		t.Fatalf("active = %#v", notFound.ActiveCategories)
	}
}

func TestCategorySnapshotAbsencePrecedesMissingCategory(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	july := createCategory(t, ctx, fx.categories, "Groceries")
	insertBudget(t, ctx, fx.db, july.ID, "2026-07", "400.00")

	_, fields, err := fx.summary.Category(ctx, "Pharmacy", "2026-08")
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	var monthNotFound *summary.NotFoundError
	var categoryNotFound *summary.CategoryNotFoundError
	if err == nil || !errors.As(err, &monthNotFound) {
		t.Fatalf("error = %v, want monthly NotFoundError first", err)
	}
	if errors.As(err, &categoryNotFound) {
		t.Fatal("missing month should not also be category_not_found")
	}
	if monthNotFound.Month != "2026-08" || monthNotFound.LatestEarlierMonth == nil || *monthNotFound.LatestEarlierMonth != "2026-07" {
		t.Fatalf("not found = %#v", monthNotFound)
	}
}

func TestCategoryMissingBudgetRowReportsZero(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	createCategory(t, ctx, fx.categories, "Health")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-08", "500.00")
	addTransaction(t, ctx, fx.transactions, "25.00", "Shoppers", "Health", "2026-08-11")

	spent := mustCategory(t, ctx, fx.summary, "Health", "2026-08")
	if spent.Budget != "0.00" || spent.TotalSpending != "25.00" || spent.Remaining != "-25.00" || spent.TransactionCount != 1 {
		t.Fatalf("unbudgeted Health = %#v", spent)
	}

	dining := createCategory(t, ctx, fx.categories, "Dining")
	zero := mustCategory(t, ctx, fx.summary, "Dining", "2026-08")
	if zero.CategoryID != dining.ID || zero.Budget != "0.00" || zero.TotalSpending != "0.00" || zero.Remaining != "0.00" || zero.TransactionCount != 0 {
		t.Fatalf("zero Dining = %#v", zero)
	}
}

func TestCategoryRemovedTransactionsDoNotCount(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-08", "500.00")
	kept := addTransaction(t, ctx, fx.transactions, "20.00", "Metro", "Groceries", "2026-08-01")
	removed := addTransaction(t, ctx, fx.transactions, "30.00", "No Frills", "Groceries", "2026-08-02")
	if _, fields, err := fx.transactions.Remove(ctx, removed.ID); err != nil || len(fields) != 0 {
		t.Fatalf("Remove(%d) fields %#v error %v", removed.ID, fields, err)
	}

	result := mustCategory(t, ctx, fx.summary, "Groceries", "2026-08")
	if result.TotalSpending != "20.00" || result.TransactionCount != 1 {
		t.Fatalf("after remove = %#v, want only %d", result, kept.ID)
	}
}
