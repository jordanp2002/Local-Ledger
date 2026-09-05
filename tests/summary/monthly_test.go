package summary_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/summary"
)

func TestMonthlyBudgetedAndSpentCategories(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	dining := createCategory(t, ctx, fx.categories, "Dining")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-08", "500.00")
	insertBudget(t, ctx, fx.db, dining.ID, "2026-08", "150.00")
	addTransaction(t, ctx, fx.transactions, "90.00", "Metro", "Groceries", "2026-08-14")
	addTransaction(t, ctx, fx.transactions, "30.00", "Cafe", "Dining", "2026-08-15")
	before := loadSnapshot(t, ctx, fx.db)

	result := mustMonthly(t, ctx, fx.summary, "2026-08")
	if result.Month != "2026-08" || result.TotalBudget != "650.00" || result.TotalSpending != "120.00" || result.Remaining != "530.00" {
		t.Fatalf("totals = %#v", result)
	}
	if !reflect.DeepEqual(result.SpentOfBudget, stringPtr("18.46")) {
		t.Fatalf("total spent_of_budget = %s, want 18.46 from 120.00/650.00", spentDebug(result.SpentOfBudget))
	}
	if len(result.Categories) != 2 {
		t.Fatalf("categories = %#v, want Dining then Groceries", result.Categories)
	}
	if !reflect.DeepEqual(result.Categories[0], contract.MonthlySummaryCategory{
		CategoryID: dining.ID, Category: "Dining", BaseBudget: "150.00", SinkingFund: false, SinkingFundOpening: "0.00", RolloverAdjustment: "0.00", Budget: "150.00", Spending: "30.00", Remaining: "120.00", SpentOfBudget: stringPtr("20.00"), ShareOfBaseBudget: stringPtr("23.07"), ShareOfSpending: stringPtr("25.00"),
	}) {
		t.Fatalf("Dining row = %#v", result.Categories[0])
	}
	if !reflect.DeepEqual(result.Categories[1], contract.MonthlySummaryCategory{
		CategoryID: groceries.ID, Category: "Groceries", BaseBudget: "500.00", SinkingFund: false, SinkingFundOpening: "0.00", RolloverAdjustment: "0.00", Budget: "500.00", Spending: "90.00", Remaining: "410.00", SpentOfBudget: stringPtr("18.00"), ShareOfBaseBudget: stringPtr("76.92"), ShareOfSpending: stringPtr("75.00"),
	}) {
		t.Fatalf("Groceries row = %#v", result.Categories[1])
	}
	assertUnchanged(t, ctx, fx.db, before)
}

func TestMonthlyOmitsZeroBudgetWithoutSpending(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	health := createCategory(t, ctx, fx.categories, "Health")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-08", "500.00")
	insertBudget(t, ctx, fx.db, health.ID, "2026-08", "0.00")

	result := mustMonthly(t, ctx, fx.summary, "2026-08")
	if result.TotalBudget != "500.00" || result.TotalSpending != "0.00" || result.Remaining != "500.00" {
		t.Fatalf("totals = %#v", result)
	}
	if !reflect.DeepEqual(result.SpentOfBudget, stringPtr("0.00")) {
		t.Fatalf("total spent_of_budget = %s, want 0.00", spentDebug(result.SpentOfBudget))
	}
	if len(result.Categories) != 1 || result.Categories[0].Category != "Groceries" {
		t.Fatalf("categories = %#v, want only Groceries", result.Categories)
	}
	if !reflect.DeepEqual(result.Categories[0].SpentOfBudget, stringPtr("0.00")) {
		t.Fatalf("Groceries spent_of_budget = %s, want 0.00", spentDebug(result.Categories[0].SpentOfBudget))
	}
}

func TestMonthlyIncludesZeroBudgetAndUnbudgetedSpending(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	dining := createCategory(t, ctx, fx.categories, "Dining")
	health := createCategory(t, ctx, fx.categories, "Health")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-08", "500.00")
	insertBudget(t, ctx, fx.db, dining.ID, "2026-08", "0.00")
	addTransaction(t, ctx, fx.transactions, "10.00", "Cafe", "Dining", "2026-08-10")
	addTransaction(t, ctx, fx.transactions, "25.00", "Shoppers", "Health", "2026-08-11")

	result := mustMonthly(t, ctx, fx.summary, "2026-08")
	if result.TotalBudget != "500.00" || result.TotalSpending != "35.00" || result.Remaining != "465.00" {
		t.Fatalf("totals = %#v", result)
	}
	if len(result.Categories) != 3 {
		t.Fatalf("categories = %#v, want Dining, Groceries, Health", result.Categories)
	}
	if result.Categories[0].Category != "Dining" || result.Categories[0].Budget != "0.00" || result.Categories[0].Spending != "10.00" || result.Categories[0].Remaining != "-10.00" || result.Categories[0].SpentOfBudget != nil {
		t.Fatalf("Dining = %#v", result.Categories[0])
	}
	if result.Categories[1].Category != "Groceries" || result.Categories[1].Budget != "500.00" {
		t.Fatalf("Groceries = %#v", result.Categories[1])
	}
	if result.Categories[2].Category != "Health" || result.Categories[2].Budget != "0.00" || result.Categories[2].Spending != "25.00" || result.Categories[2].Remaining != "-25.00" || result.Categories[2].SpentOfBudget != nil {
		t.Fatalf("Health = %#v", result.Categories[2])
	}
	_ = health
}

func TestMonthlyIncludesInactiveSpendingAndHistoricalBudget(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 10, 15, 12, 0))
	dining := createCategory(t, ctx, fx.categories, "Dining")
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	insertBudget(t, ctx, fx.db, dining.ID, "2026-08", "150.00")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-08", "500.00")
	addTransaction(t, ctx, fx.transactions, "40.00", "Cafe", "Dining", "2026-08-10")
	if _, changed, _, err := fx.categories.Disable(ctx, "Dining"); err != nil || !changed {
		t.Fatalf("Disable(Dining) = changed %v, error %v", changed, err)
	}

	result := mustMonthly(t, ctx, fx.summary, "2026-08")
	if len(result.Categories) != 2 || result.Categories[0].Category != "Dining" || result.Categories[0].Budget != "150.00" || result.Categories[0].Spending != "40.00" {
		t.Fatalf("August after later disable = %#v", result.Categories)
	}
}

func TestMonthlyDisabledCurrentMonthShowsSpendingAgainstZero(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	dining := createCategory(t, ctx, fx.categories, "Dining")
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	insertBudget(t, ctx, fx.db, dining.ID, "2026-08", "150.00")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-08", "500.00")
	addTransaction(t, ctx, fx.transactions, "40.00", "Cafe", "Dining", "2026-08-10")
	if _, changed, removed, err := fx.categories.Disable(ctx, "Dining"); err != nil || !changed || removed == nil {
		t.Fatalf("Disable(Dining) changed=%v removed=%v err=%v", changed, removed, err)
	}

	result := mustMonthly(t, ctx, fx.summary, "2026-08")
	if result.TotalBudget != "500.00" || result.TotalSpending != "40.00" || result.Remaining != "460.00" {
		t.Fatalf("totals = %#v", result)
	}
	if len(result.Categories) != 2 {
		t.Fatalf("categories = %#v", result.Categories)
	}
	if result.Categories[0].Category != "Dining" || result.Categories[0].Budget != "0.00" || result.Categories[0].Remaining != "-40.00" {
		t.Fatalf("disabled Dining = %#v", result.Categories[0])
	}
}

func TestMonthlyIncludesFirstAndLastDayAndExcludesNeighbors(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 9, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-08", "500.00")
	addTransaction(t, ctx, fx.transactions, "1.00", "Jul", "Groceries", "2026-07-31")
	addTransaction(t, ctx, fx.transactions, "2.00", "Aug1", "Groceries", "2026-08-01")
	addTransaction(t, ctx, fx.transactions, "3.00", "Aug31", "Groceries", "2026-08-31")
	addTransaction(t, ctx, fx.transactions, "4.00", "Sep", "Groceries", "2026-09-01")

	result := mustMonthly(t, ctx, fx.summary, "2026-08")
	if result.TotalSpending != "5.00" || result.Categories[0].Spending != "5.00" {
		t.Fatalf("August spending = %#v, want 5.00 from first and last day", result)
	}
}

func TestMonthlyFebruaryBounds(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 3, 1, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-02", "100.00")
	insertBudget(t, ctx, fx.db, groceries.ID, "2024-02", "100.00")
	addTransaction(t, ctx, fx.transactions, "10.00", "Feb28", "Groceries", "2026-02-28")
	addTransaction(t, ctx, fx.transactions, "11.00", "Mar1", "Groceries", "2026-03-01")
	addTransaction(t, ctx, fx.transactions, "12.00", "Leap", "Groceries", "2024-02-29")

	feb2026 := mustMonthly(t, ctx, fx.summary, "2026-02")
	if feb2026.TotalSpending != "10.00" {
		t.Fatalf("2026-02 spending = %s, want 10.00", feb2026.TotalSpending)
	}
	feb2024 := mustMonthly(t, ctx, fx.summary, "2024-02")
	if feb2024.TotalSpending != "12.00" {
		t.Fatalf("2024-02 spending = %s, want 12.00", feb2024.TotalSpending)
	}
}

func TestMonthlyMissingSnapshotEvenWhenTransactionsExist(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	july := createCategory(t, ctx, fx.categories, "Groceries")
	insertBudget(t, ctx, fx.db, july.ID, "2026-07", "400.00")
	createCategory(t, ctx, fx.categories, "Dining")
	addTransaction(t, ctx, fx.transactions, "20.00", "Metro", "Groceries", "2026-08-14")
	before := loadSnapshot(t, ctx, fx.db)

	_, fields, err := fx.summary.Monthly(ctx, "2026-08")
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	var notFound *summary.NotFoundError
	if err == nil || !errors.As(err, &notFound) {
		t.Fatalf("error = %v, want NotFoundError", err)
	}
	if notFound.Month != "2026-08" || notFound.LatestEarlierMonth == nil || *notFound.LatestEarlierMonth != "2026-07" {
		t.Fatalf("not found = %#v, want August with July source", notFound)
	}
	assertUnchanged(t, ctx, fx.db, before)
}

func TestMonthlyLatestEarlierMonthSkipsGapsAndIgnoresLater(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 10, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-03", "100.00")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-08", "200.00")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-11", "300.00")

	_, _, err := fx.summary.Monthly(ctx, "2026-10")
	var notFound *summary.NotFoundError
	if err == nil || !errors.As(err, &notFound) || notFound.LatestEarlierMonth == nil || *notFound.LatestEarlierMonth != "2026-08" {
		t.Fatalf("October source = %#v, want 2026-08", err)
	}

	_, _, err = fx.summary.Monthly(ctx, "2026-02")
	if err == nil || !errors.As(err, &notFound) || notFound.LatestEarlierMonth != nil {
		t.Fatalf("February source = %#v, want nil", err)
	}
}

func TestMonthlyZeroOnlySnapshotReturnsEmptyCategories(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	health := createCategory(t, ctx, fx.categories, "Health")
	insertBudget(t, ctx, fx.db, health.ID, "2026-08", "0.00")

	result := mustMonthly(t, ctx, fx.summary, "2026-08")
	if result.TotalBudget != "0.00" || result.TotalSpending != "0.00" || result.Remaining != "0.00" || result.SpentOfBudget != nil {
		t.Fatalf("totals = %#v", result)
	}
	encoded, err := json.Marshal(result.Categories)
	if err != nil || string(encoded) != "[]" {
		t.Fatalf("categories JSON = %s, %v; want []", encoded, err)
	}
}

func TestMonthlyPreservesStoredCategorySpelling(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-08", "500.00")
	addTransaction(t, ctx, fx.transactions, "20.00", "Metro", "groceries", "2026-08-14")

	result := mustMonthly(t, ctx, fx.summary, "2026-08")
	if result.Categories[0].Category != "Groceries" {
		t.Fatalf("category spelling = %q, want Groceries", result.Categories[0].Category)
	}
}

func TestMonthlyOverflowIsInternalErrorAndDoesNotWrap(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-08", "1.00")
	insertRawTransaction(t, ctx, fx.db, "Huge", math.MaxInt64, "2026-08-01", groceries.ID)
	insertRawTransaction(t, ctx, fx.db, "One", 1, "2026-08-02", groceries.ID)
	before := loadSnapshot(t, ctx, fx.db)

	_, fields, err := fx.summary.Monthly(ctx, "2026-08")
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	if err == nil {
		t.Fatal("Monthly() overflow error = nil")
	}
	var notFound *summary.NotFoundError
	if errors.As(err, &notFound) {
		t.Fatalf("overflow mapped to not found: %v", err)
	}
	assertUnchanged(t, ctx, fx.db, before)
}

func TestMonthlySpentOfBudgetMultiplyOverflow(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-08", "1.00")
	insertRawTransaction(t, ctx, fx.db, "Huge", math.MaxInt64, "2026-08-01", groceries.ID)
	before := loadSnapshot(t, ctx, fx.db)

	_, fields, err := fx.summary.Monthly(ctx, "2026-08")
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	if err == nil {
		t.Fatal("Monthly() spent_of_budget overflow error = nil")
	}
	var notFound *summary.NotFoundError
	if errors.As(err, &notFound) {
		t.Fatalf("overflow mapped to not found: %v", err)
	}
	assertUnchanged(t, ctx, fx.db, before)
}
