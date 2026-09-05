package summary_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/summary"
)

func TestTopMerchantsGroupsCaseInsensitivelyAndOrdersDeterministically(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 20, 12, 0))
	createCategory(t, ctx, fx.categories, "Groceries")

	addTransaction(t, ctx, fx.transactions, "35.00", "Metro", "Groceries", "2026-08-14")
	addTransaction(t, ctx, fx.transactions, "35.00", "metro", "Groceries", "2026-08-14")
	addTransaction(t, ctx, fx.transactions, "20.00", "Cafe", "Groceries", "2026-08-13")
	addTransaction(t, ctx, fx.transactions, "20.00", "Cafe", "Groceries", "2026-08-12")
	addTransaction(t, ctx, fx.transactions, "40.00", "Alpha", "Groceries", "2026-08-11")
	addTransaction(t, ctx, fx.transactions, "99.00", "Metro Grocery", "Groceries", "2026-08-15")

	result := mustTopMerchants(t, ctx, fx.summary, summary.TopMerchantsInput{Limit: int64Ptr(1)})
	if result.TotalSpending != "249.00" || result.TransactionCount != 6 {
		t.Fatalf("totals = %#v, want all filtered rows, not only returned merchant", result)
	}
	if result.Limit != 1 || result.Returned != 1 || result.MerchantCount != 4 {
		t.Fatalf("counts = %#v, want limit 1, returned 1, merchant_count 4", result)
	}
	wantFirst := []contract.MerchantSpending{{Merchant: "Metro Grocery", Spending: "99.00", TransactionCount: 1}}
	if !reflect.DeepEqual(result.Merchants, wantFirst) {
		t.Fatalf("top merchant = %#v, want %#v", result.Merchants, wantFirst)
	}

	result = mustTopMerchants(t, ctx, fx.summary, summary.TopMerchantsInput{Limit: int64Ptr(50)})
	want := []contract.MerchantSpending{
		{Merchant: "Metro Grocery", Spending: "99.00", TransactionCount: 1},
		{Merchant: "metro", Spending: "70.00", TransactionCount: 2},
		{Merchant: "Cafe", Spending: "40.00", TransactionCount: 2},
		{Merchant: "Alpha", Spending: "40.00", TransactionCount: 1},
	}
	if !reflect.DeepEqual(result.Merchants, want) {
		t.Fatalf("merchant ranking = %#v, want %#v", result.Merchants, want)
	}
}

func TestTopMerchantsDefaultLimitTruncatesAllGroups(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 20, 12, 0))
	createCategory(t, ctx, fx.categories, "Groceries")
	for i := 0; i < 11; i++ {
		addTransaction(t, ctx, fx.transactions, "1.00", fmt.Sprintf("Merchant%02d", i), "Groceries", "2026-08-01")
	}

	result := mustTopMerchants(t, ctx, fx.summary, summary.TopMerchantsInput{})
	if result.Limit != 10 || result.Returned != 10 || result.MerchantCount != 11 {
		t.Fatalf("limit counts = %#v, want default 10 of 11 groups", result)
	}
	if result.TotalSpending != "11.00" || result.TransactionCount != 11 || len(result.Merchants) != 10 {
		t.Fatalf("totals/page = %#v, want all 11 transactions and 10 returned groups", result)
	}
}

func TestTopMerchantsDateAndInactiveCategoryFilters(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 9, 20, 12, 0))
	createCategory(t, ctx, fx.categories, "Groceries")
	createCategory(t, ctx, fx.categories, "Dining")
	addTransaction(t, ctx, fx.transactions, "10.00", "Metro", "Groceries", "2026-07-31")
	addTransaction(t, ctx, fx.transactions, "20.00", "Metro", "Groceries", "2026-08-14")
	addTransaction(t, ctx, fx.transactions, "15.00", "Cafe", "Dining", "2026-08-14")
	addTransaction(t, ctx, fx.transactions, "25.00", "Cafe", "Dining", "2026-08-31")
	addTransaction(t, ctx, fx.transactions, "30.00", "Metro", "Groceries", "2026-09-01")
	if _, changed, _, err := fx.categories.Disable(ctx, "Dining"); err != nil || !changed {
		t.Fatalf("Disable(Dining) = changed %v, error %v", changed, err)
	}

	result := mustTopMerchants(t, ctx, fx.summary, summary.TopMerchantsInput{
		StartDate: stringPtr("2026-08-01"),
		EndDate:   stringPtr("2026-08-31"),
		Category:  stringPtr("dining"),
	})
	if result.StartDate == nil || *result.StartDate != "2026-08-01" || result.EndDate == nil || *result.EndDate != "2026-08-31" {
		t.Fatalf("date echoes = %#v, want inclusive canonical bounds", result)
	}
	if result.Category == nil || *result.Category != "Dining" {
		t.Fatalf("category echo = %#v, want stored inactive category spelling", result.Category)
	}
	if result.TotalSpending != "40.00" || result.TransactionCount != 2 || result.MerchantCount != 1 {
		t.Fatalf("filtered totals = %#v, want inactive Dining spending", result)
	}
	want := []contract.MerchantSpending{{Merchant: "Cafe", Spending: "40.00", TransactionCount: 2}}
	if !reflect.DeepEqual(result.Merchants, want) {
		t.Fatalf("filtered merchants = %#v, want %#v", result.Merchants, want)
	}
}

func TestTopMerchantsEmptyLedgerAndOmittedFilters(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 20, 12, 0))

	result := mustTopMerchants(t, ctx, fx.summary, summary.TopMerchantsInput{})
	if result.StartDate != nil || result.EndDate != nil || result.Category != nil {
		t.Fatalf("omitted filters = %#v, want nulls", result)
	}
	if result.TotalSpending != "0.00" || result.TransactionCount != 0 || result.Limit != 10 || result.Returned != 0 || result.MerchantCount != 0 {
		t.Fatalf("empty result = %#v, want zero totals and default limit", result)
	}
	encoded, err := json.Marshal(result.Merchants)
	if err != nil || string(encoded) != "[]" {
		t.Fatalf("empty merchants JSON = %s, %v; want []", encoded, err)
	}
}

func TestTopMerchantsSemanticIssuesUsePlanOrder(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 20, 12, 0))
	before := loadSnapshot(t, ctx, fx.db)

	_, fields, err := fx.summary.TopMerchants(ctx, summary.TopMerchantsInput{
		StartDate: stringPtr("2026-08-31"),
		EndDate:   stringPtr("2026-08-01"),
		Category:  stringPtr(" "),
		Limit:     int64Ptr(0),
	})
	if err != nil {
		t.Fatalf("TopMerchants() error = %v, want semantic issues", err)
	}
	want := []contract.FieldIssue{
		{Field: "end_date", Reason: "must be on or after start_date"},
		{Field: "category", Reason: "must not be empty"},
		{Field: "limit", Reason: "must be between 1 and 50"},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("fields = %#v, want %#v", fields, want)
	}
	assertUnchanged(t, ctx, fx.db, before)

	_, fields, err = fx.summary.TopMerchants(ctx, summary.TopMerchantsInput{
		StartDate: stringPtr("2026-8-31"),
		EndDate:   stringPtr("2026/08/01"),
		Limit:     int64Ptr(51),
	})
	if err != nil {
		t.Fatalf("TopMerchants() malformed error = %v, want semantic issues", err)
	}
	want = []contract.FieldIssue{
		{Field: "start_date", Reason: "must be a valid YYYY-MM-DD date"},
		{Field: "end_date", Reason: "must be a valid YYYY-MM-DD date"},
		{Field: "limit", Reason: "must be between 1 and 50"},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("malformed fields = %#v, want %#v", fields, want)
	}

	_, fields, err = fx.summary.TopMerchants(ctx, summary.TopMerchantsInput{Limit: int64Ptr(-1)})
	if err != nil {
		t.Fatalf("TopMerchants() negative limit error = %v, want semantic issue", err)
	}
	want = []contract.FieldIssue{{Field: "limit", Reason: "must be between 1 and 50"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("negative limit fields = %#v, want %#v", fields, want)
	}
}

func TestTopMerchantsMissingCategoryReturnsRecovery(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 20, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	before := loadSnapshot(t, ctx, fx.db)

	_, fields, err := fx.summary.TopMerchants(ctx, summary.TopMerchantsInput{Category: stringPtr(" Pharmacy ")})
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

func TestTopMerchantsOverflowIsChecked(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 20, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	insertRawTransaction(t, ctx, fx.db, "Huge", math.MaxInt64, "2026-08-01", groceries.ID)
	insertRawTransaction(t, ctx, fx.db, "Huge", 1, "2026-08-02", groceries.ID)
	before := loadSnapshot(t, ctx, fx.db)

	_, fields, err := fx.summary.TopMerchants(ctx, summary.TopMerchantsInput{})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	if err == nil {
		t.Fatal("TopMerchants() overflow error = nil")
	}
	var missing *summary.CategoryNotFoundError
	if errors.As(err, &missing) {
		t.Fatalf("overflow mapped to category_not_found: %v", err)
	}
	assertUnchanged(t, ctx, fx.db, before)
}

func TestTopMerchantsDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 20, 12, 0))
	createCategory(t, ctx, fx.categories, "Groceries")
	addTransaction(t, ctx, fx.transactions, "20.00", "Metro", "Groceries", "2026-08-14")
	before := loadSnapshot(t, ctx, fx.db)

	result := mustTopMerchants(t, ctx, fx.summary, summary.TopMerchantsInput{
		StartDate: stringPtr("2026-08-01"),
		EndDate:   stringPtr("2026-08-31"),
		Category:  stringPtr("Groceries"),
	})
	if result.TotalSpending != "20.00" || result.TransactionCount != 1 {
		t.Fatalf("result = %#v, want one unbudgeted transaction", result)
	}
	assertUnchanged(t, ctx, fx.db, before)
}

func mustTopMerchants(t *testing.T, ctx context.Context, store *summary.Store, in summary.TopMerchantsInput) summary.TopMerchantsResult {
	t.Helper()
	result, fields, err := store.TopMerchants(ctx, in)
	if err != nil || len(fields) != 0 {
		t.Fatalf("TopMerchants() = %#v fields %#v error %v", result, fields, err)
	}
	if result.Merchants == nil {
		t.Fatal("TopMerchants() merchants is nil, want non-nil slice")
	}
	return result
}

func int64Ptr(value int64) *int64 {
	return &value
}
