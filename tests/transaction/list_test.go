package transaction_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/transaction"
)

func mustList(t *testing.T, ctx context.Context, store *transaction.Store, in transaction.ListInput) transaction.ListResult {
	t.Helper()
	result, fields, err := store.List(ctx, in)
	if err != nil || len(fields) != 0 {
		t.Fatalf("List() = %#v fields %#v error %v", result, fields, err)
	}
	if result.Transactions == nil {
		t.Fatal("List() transactions is nil, want non-nil slice")
	}
	return result
}

func assertEmptyPage(t *testing.T, result transaction.ListResult, limit, offset, total int64) {
	t.Helper()
	if result.Transactions == nil || len(result.Transactions) != 0 {
		t.Fatalf("transactions = %#v, want empty non-nil slice", result.Transactions)
	}
	encoded, err := json.Marshal(result.Transactions)
	if err != nil || string(encoded) != "[]" {
		t.Fatalf("empty page JSON = %s, %v; want []", encoded, err)
	}
	want := contract.Page{Limit: limit, Offset: offset, Returned: 0, Total: total, HasMore: false}
	if result.Page != want {
		t.Fatalf("page = %#v, want %#v", result.Page, want)
	}
}

func transactionIDs(rows []contract.Transaction) []int64 {
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return ids
}

func TestListEmptyDatabaseReturnsEmptyNonNilPage(t *testing.T) {
	ctx := context.Background()
	store, _, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	result := mustList(t, ctx, store, transaction.ListInput{})
	assertEmptyPage(t, result, 50, 0, 0)
}

func TestListUnfilteredIncludesInactiveCategoryRows(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	createCategory(t, ctx, categories, "Dining")
	active := addTransaction(t, ctx, store, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Metro",
		Category: stringPtr("Groceries"),
		Date:     stringPtr("2026-08-14"),
		Note:     stringPtr("weekly"),
	})
	inactive := addTransaction(t, ctx, store, transaction.AddInput{
		Amount:   "15.00",
		Merchant: "Cafe",
		Category: stringPtr("Dining"),
		Date:     stringPtr("2026-08-13"),
	})
	if _, changed, _, err := categories.Disable(ctx, "Dining"); err != nil || !changed {
		t.Fatalf("Disable(Dining) = changed %v, error %v", changed, err)
	}

	result := mustList(t, ctx, store, transaction.ListInput{})
	if !reflect.DeepEqual(transactionIDs(result.Transactions), []int64{active.ID, inactive.ID}) {
		t.Fatalf("unfiltered IDs = %v, want active then inactive-category row", transactionIDs(result.Transactions))
	}
	if transactionCategory(result.Transactions[1]) != "Dining" || transactionCategoryID(result.Transactions[1]) != *inactive.CategoryID {
		t.Fatalf("inactive-category row = %#v, want stored Dining spelling", result.Transactions[1])
	}
	if result.Transactions[0].Note == nil || *result.Transactions[0].Note != "weekly" {
		t.Fatalf("active note = %#v, want weekly", result.Transactions[0].Note)
	}
	if result.Transactions[1].Note != nil {
		t.Fatalf("unset note = %#v, want nil", result.Transactions[1].Note)
	}
}

func TestListStartDateOnlyIncludesThatDayAndLater(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "1.00", Merchant: "Jul", Category: stringPtr("Groceries"), Date: stringPtr("2026-07-31"),
	})
	onBound := addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "2.00", Merchant: "Aug1", Category: stringPtr("Groceries"), Date: stringPtr("2026-08-01"),
	})
	later := addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "3.00", Merchant: "Aug14", Category: stringPtr("Groceries"), Date: stringPtr("2026-08-14"),
	})

	result := mustList(t, ctx, store, transaction.ListInput{StartDate: stringPtr("2026-08-01")})
	if !reflect.DeepEqual(transactionIDs(result.Transactions), []int64{later.ID, onBound.ID}) {
		t.Fatalf("start-only IDs = %v, want on-bound and later", transactionIDs(result.Transactions))
	}
}

func TestListEndDateOnlyIncludesThatDayAndEarlier(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	earlier := addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "1.00", Merchant: "Jul", Category: stringPtr("Groceries"), Date: stringPtr("2026-07-31"),
	})
	onBound := addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "2.00", Merchant: "Aug1", Category: stringPtr("Groceries"), Date: stringPtr("2026-08-01"),
	})
	addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "3.00", Merchant: "Aug14", Category: stringPtr("Groceries"), Date: stringPtr("2026-08-14"),
	})

	result := mustList(t, ctx, store, transaction.ListInput{EndDate: stringPtr("2026-08-01")})
	if !reflect.DeepEqual(transactionIDs(result.Transactions), []int64{onBound.ID, earlier.ID}) {
		t.Fatalf("end-only IDs = %v, want on-bound and earlier", transactionIDs(result.Transactions))
	}
}

func TestListInclusiveRangeIncludesEndpointsAndExcludesOutside(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 9, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "1.00", Merchant: "Jul", Category: stringPtr("Groceries"), Date: stringPtr("2026-07-31"),
	})
	start := addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "2.00", Merchant: "Aug1", Category: stringPtr("Groceries"), Date: stringPtr("2026-08-01"),
	})
	end := addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "3.00", Merchant: "Aug31", Category: stringPtr("Groceries"), Date: stringPtr("2026-08-31"),
	})
	addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "4.00", Merchant: "Sep", Category: stringPtr("Groceries"), Date: stringPtr("2026-09-01"),
	})

	result := mustList(t, ctx, store, transaction.ListInput{
		StartDate: stringPtr("2026-08-01"),
		EndDate:   stringPtr("2026-08-31"),
	})
	if !reflect.DeepEqual(transactionIDs(result.Transactions), []int64{end.ID, start.ID}) {
		t.Fatalf("August range IDs = %v, want endpoints only", transactionIDs(result.Transactions))
	}
	if result.Page.Total != 2 || result.Page.HasMore {
		t.Fatalf("August range page = %#v, want total 2", result.Page)
	}
}

func TestListCategoryLookupIsCaseInsensitiveAndPreservesStoredSpelling(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	stored := addTransaction(t, ctx, store, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Metro",
		Category: stringPtr("Groceries"),
		Date:     stringPtr("2026-08-01"),
	})

	result := mustList(t, ctx, store, transaction.ListInput{Category: stringPtr("GROCERIES")})
	if result.Page.Total != 1 || result.Transactions[0].ID != stored.ID {
		t.Fatalf("case-insensitive list = %#v, want stored row", result)
	}
	if transactionCategory(result.Transactions[0]) != "Groceries" || transactionCategoryID(result.Transactions[0]) != *stored.CategoryID {
		t.Fatalf("returned category = %#v, want stored Groceries spelling", result.Transactions[0])
	}
}

func TestListDoesNotFoldUnicodeBeyondSQLiteNoCase(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Café")
	addTransaction(t, ctx, store, transaction.AddInput{
		Amount:   "4.00",
		Merchant: "Cafe",
		Category: stringPtr("Café"),
		Date:     stringPtr("2026-08-01"),
	})

	_, fields, err := store.List(ctx, transaction.ListInput{Category: stringPtr("CAFÉ")})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	var missing *transaction.CategoryNotFoundError
	if !errors.As(err, &missing) || missing.Requested != "CAFÉ" {
		t.Fatalf("error = %v, want CategoryNotFoundError for unfolded CAFÉ", err)
	}
}

func TestListInactiveCategoryFilterSucceeds(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	createCategory(t, ctx, categories, "Dining")
	addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "20.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: stringPtr("2026-08-14"),
	})
	historical := addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "15.00", Merchant: "Cafe", Category: stringPtr("Dining"), Date: stringPtr("2026-08-13"),
	})
	if _, changed, _, err := categories.Disable(ctx, "Dining"); err != nil || !changed {
		t.Fatalf("Disable(Dining) = changed %v, error %v", changed, err)
	}

	result, fields, err := store.List(ctx, transaction.ListInput{Category: stringPtr("dining")})
	if err != nil || len(fields) != 0 {
		t.Fatalf("List(inactive Dining) = %#v fields %#v error %v", result, fields, err)
	}
	if errors.Is(err, transaction.ErrCategoryInactive) {
		t.Fatal("inactive category filter returned category_inactive")
	}
	if result.Page.Total != 1 || result.Transactions[0].ID != historical.ID || transactionCategory(result.Transactions[0]) != "Dining" {
		t.Fatalf("inactive filter = %#v, want historical Dining row", result)
	}
}

func TestListMissingCategoryIsNotFoundAndSkipsPageQuery(t *testing.T) {
	ctx := context.Background()
	now := torontoTime(t, 2026, 8, 15, 12, 0)

	t.Run("empty active list", func(t *testing.T) {
		store, _, _, db := openTransactionStore(t, now)
		dropTransactionsTable(t, ctx, db)

		got, fields, err := store.List(ctx, transaction.ListInput{Category: stringPtr(" Pharmacy ")})
		if len(fields) != 0 {
			t.Fatalf("fields = %#v, want none", fields)
		}
		if !reflect.DeepEqual(got, transaction.ListResult{}) {
			t.Fatalf("result = %#v, want zero value", got)
		}
		var missing *transaction.CategoryNotFoundError
		if !errors.As(err, &missing) || !errors.Is(err, transaction.ErrCategoryNotFound) {
			t.Fatalf("error = %v, want CategoryNotFoundError", err)
		}
		if missing.Requested != "Pharmacy" || missing.ActiveCategories == nil || len(missing.ActiveCategories) != 0 {
			t.Fatalf("missing recovery = %#v, want trimmed name and empty non-nil list", missing)
		}
	})

	t.Run("active recovery list", func(t *testing.T) {
		store, categories, _, db := openTransactionStore(t, now)
		alpha := createCategory(t, ctx, categories, "Alpha")
		beta := createCategory(t, ctx, categories, "beta")
		dropTransactionsTable(t, ctx, db)

		_, fields, err := store.List(ctx, transaction.ListInput{Category: stringPtr("Pharmacy")})
		if len(fields) != 0 {
			t.Fatalf("fields = %#v, want none", fields)
		}
		var missing *transaction.CategoryNotFoundError
		if !errors.As(err, &missing) {
			t.Fatalf("error = %v, want CategoryNotFoundError", err)
		}
		if missing.ActiveCategories == nil || len(missing.ActiveCategories) != 2 {
			t.Fatalf("active recovery = %#v, want Alpha, beta", missing.ActiveCategories)
		}
		if missing.ActiveCategories[0].ID != alpha.ID || missing.ActiveCategories[1].ID != beta.ID {
			t.Fatalf("active recovery order = %#v, want Alpha, beta", missing.ActiveCategories)
		}
	})
}

func TestListMerchantExactMatchPreservesStoredSpelling(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	metro := addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "20.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: stringPtr("2026-08-14"),
	})
	addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "8.00", Merchant: "Cafe", Category: stringPtr("Groceries"), Date: stringPtr("2026-08-13"),
	})

	result := mustList(t, ctx, store, transaction.ListInput{Merchant: stringPtr(" \tMetro \n")})
	if !reflect.DeepEqual(transactionIDs(result.Transactions), []int64{metro.ID}) {
		t.Fatalf("exact merchant IDs = %v, want only Metro", transactionIDs(result.Transactions))
	}
	if result.Transactions[0].Merchant != "Metro" {
		t.Fatalf("returned merchant = %q, want stored Metro spelling", result.Transactions[0].Merchant)
	}
}

func TestListMerchantMatchIsCaseInsensitive(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	stored := addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "20.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: stringPtr("2026-08-14"),
	})

	result := mustList(t, ctx, store, transaction.ListInput{Merchant: stringPtr("metro")})
	if result.Page.Total != 1 || result.Transactions[0].ID != stored.ID {
		t.Fatalf("case-insensitive merchant list = %#v, want stored Metro row", result)
	}
	if result.Transactions[0].Merchant != "Metro" {
		t.Fatalf("returned merchant = %q, want stored Metro spelling", result.Transactions[0].Merchant)
	}
}

func TestListMerchantDoesNotSubstringMatch(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	metro := addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "20.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: stringPtr("2026-08-14"),
	})
	addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "12.00", Merchant: "Metro Grocery", Category: stringPtr("Groceries"), Date: stringPtr("2026-08-13"),
	})

	result := mustList(t, ctx, store, transaction.ListInput{Merchant: stringPtr("Metro")})
	if !reflect.DeepEqual(transactionIDs(result.Transactions), []int64{metro.ID}) {
		t.Fatalf("merchant IDs = %v, want exact Metro only, not Metro Grocery", transactionIDs(result.Transactions))
	}
}

func TestListMerchantCombinesWithCategoryAndDates(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 9, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	createCategory(t, ctx, categories, "Dining")
	addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "10.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: stringPtr("2026-07-31"),
	})
	inRange := addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "20.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: stringPtr("2026-08-14"),
	})
	addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "15.00", Merchant: "Metro", Category: stringPtr("Dining"), Date: stringPtr("2026-08-14"),
	})
	addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "8.00", Merchant: "Cafe", Category: stringPtr("Groceries"), Date: stringPtr("2026-08-14"),
	})
	addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "9.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: stringPtr("2026-09-01"),
	})

	result := mustList(t, ctx, store, transaction.ListInput{
		StartDate: stringPtr("2026-08-01"),
		EndDate:   stringPtr("2026-08-31"),
		Category:  stringPtr("groceries"),
		Merchant:  stringPtr("metro"),
	})
	if !reflect.DeepEqual(transactionIDs(result.Transactions), []int64{inRange.ID}) {
		t.Fatalf("AND filter IDs = %v, want only August Groceries Metro", transactionIDs(result.Transactions))
	}
}

func TestListUnknownMerchantReturnsEmptyPage(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "20.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: stringPtr("2026-08-14"),
	})

	result := mustList(t, ctx, store, transaction.ListInput{Merchant: stringPtr("Unknown Store")})
	assertEmptyPage(t, result, 50, 0, 0)
}

func TestListMerchantPaginationTotalsCountOnlyMatches(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	createCategory(t, ctx, categories, "Dining")
	var metroIDs []int64
	for _, date := range []string{"2026-08-01", "2026-08-05", "2026-08-10"} {
		metroIDs = append(metroIDs, addTransaction(t, ctx, store, transaction.AddInput{
			Amount: "1.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: stringPtr(date),
		}).ID)
	}
	addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "2.00", Merchant: "Cafe", Category: stringPtr("Dining"), Date: stringPtr("2026-08-12"),
	})
	addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "2.00", Merchant: "Metro Grocery", Category: stringPtr("Groceries"), Date: stringPtr("2026-08-14"),
	})

	page := mustList(t, ctx, store, transaction.ListInput{
		Merchant: stringPtr("metro"),
		Limit:    int64Ptr(1),
		Offset:   int64Ptr(0),
	})
	if page.Page != (contract.Page{Limit: 1, Offset: 0, Returned: 1, Total: 3, HasMore: true}) {
		t.Fatalf("merchant first page = %#v, want total 3 not 5", page.Page)
	}
	if page.Transactions[0].ID != metroIDs[2] {
		t.Fatalf("merchant first row = %d, want newest Metro %d", page.Transactions[0].ID, metroIDs[2])
	}

	empty := mustList(t, ctx, store, transaction.ListInput{
		Merchant:  stringPtr("Metro"),
		StartDate: stringPtr("2026-09-01"),
		Limit:     int64Ptr(1),
	})
	assertEmptyPage(t, empty, 1, 0, 0)
}

func TestListAugustGroceriesExcludesDining(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 9, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	createCategory(t, ctx, categories, "Dining")
	addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "10.00", Merchant: "Jul Grocery", Category: stringPtr("Groceries"), Date: stringPtr("2026-07-31"),
	})
	inRange := addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "20.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: stringPtr("2026-08-14"),
	})
	addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "15.00", Merchant: "Cafe", Category: stringPtr("Dining"), Date: stringPtr("2026-08-14"),
	})
	addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "8.00", Merchant: "Sep Grocery", Category: stringPtr("Groceries"), Date: stringPtr("2026-09-01"),
	})

	result := mustList(t, ctx, store, transaction.ListInput{
		StartDate: stringPtr("2026-08-01"),
		EndDate:   stringPtr("2026-08-31"),
		Category:  stringPtr("groceries"),
	})
	if !reflect.DeepEqual(transactionIDs(result.Transactions), []int64{inRange.ID}) {
		t.Fatalf("August Groceries IDs = %v, want only in-range Groceries", transactionIDs(result.Transactions))
	}
	if transactionCategory(result.Transactions[0]) != "Groceries" || result.Transactions[0].Merchant != "Metro" {
		t.Fatalf("filtered row = %#v, want Metro/Groceries", result.Transactions[0])
	}
}

func TestListRemovedRowsDoNotAppear(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	kept := addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "20.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: stringPtr("2026-08-14"),
	})
	removed := addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "5.00", Merchant: "Old Metro", Category: stringPtr("Groceries"), Date: stringPtr("2026-08-13"),
	})
	mustRemove(t, ctx, store, removed.ID)

	result := mustList(t, ctx, store, transaction.ListInput{})
	if !reflect.DeepEqual(transactionIDs(result.Transactions), []int64{kept.ID}) {
		t.Fatalf("list after remove = %v, want only kept id %d", transactionIDs(result.Transactions), kept.ID)
	}
	if got := countTransactions(t, ctx, db); got != 1 {
		t.Fatalf("transaction count = %d, want 1", got)
	}
}

func TestListSameDateOrdersByIDDescending(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	first := addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "10.00", Merchant: "Metro A", Category: stringPtr("Groceries"), Date: stringPtr("2026-08-14"),
	})
	second := addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "11.00", Merchant: "Metro B", Category: stringPtr("Groceries"), Date: stringPtr("2026-08-14"),
	})
	if second.ID <= first.ID {
		t.Fatalf("expected later insert id %d > %d", second.ID, first.ID)
	}

	result := mustList(t, ctx, store, transaction.ListInput{Category: stringPtr("Groceries")})
	if !reflect.DeepEqual(transactionIDs(result.Transactions), []int64{second.ID, first.ID}) {
		t.Fatalf("same-date order = %v, want higher ID first", transactionIDs(result.Transactions))
	}
}

func TestListReturnsCanonicalJoinedRecords(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	created := addTransaction(t, ctx, store, transaction.AddInput{
		Amount:   "20.5",
		Merchant: "Metro",
		Category: stringPtr("groceries"),
		Date:     stringPtr("2026-08-01"),
		Note:     stringPtr(" weekly "),
	})
	const frozen = "2020-01-01T00:00:00.000Z"
	freezeTransactionTimestamps(t, ctx, db, created.ID, frozen)

	result := mustList(t, ctx, store, transaction.ListInput{})
	if len(result.Transactions) != 1 {
		t.Fatalf("listed %d rows, want 1", len(result.Transactions))
	}
	got := result.Transactions[0]
	if got.ID != created.ID || got.Amount != "20.50" || got.Merchant != "Metro" || got.Date != "2026-08-01" {
		t.Fatalf("canonical scalars = %#v, want normalized Metro/20.50/2026-08-01", got)
	}
	if transactionCategoryID(got) != transactionCategoryID(created) || transactionCategory(got) != "Groceries" {
		t.Fatalf("canonical category = %#v, want stored Groceries", got)
	}
	if got.Note == nil || *got.Note != "weekly" {
		t.Fatalf("canonical note = %#v, want weekly", got.Note)
	}
	if got.CreatedAt != frozen || got.UpdatedAt != frozen {
		t.Fatalf("canonical timestamps = %#v, want frozen", got)
	}
}

func TestListPaginationMetadata(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	ids := make([]int64, 5)
	for i, date := range []string{"2026-08-01", "2026-08-05", "2026-08-10", "2026-08-12", "2026-08-14"} {
		ids[i] = addTransaction(t, ctx, store, transaction.AddInput{
			Amount:   "1.00",
			Merchant: "Metro-" + date,
			Category: stringPtr("Groceries"),
			Date:     stringPtr(date),
		}).ID
	}
	newestFirst := []int64{ids[4], ids[3], ids[2], ids[1], ids[0]}

	first := mustList(t, ctx, store, transaction.ListInput{Limit: int64Ptr(2), Offset: int64Ptr(0)})
	if first.Page != (contract.Page{Limit: 2, Offset: 0, Returned: 2, Total: 5, HasMore: true}) {
		t.Fatalf("first page = %#v", first.Page)
	}
	if !reflect.DeepEqual(transactionIDs(first.Transactions), newestFirst[:2]) {
		t.Fatalf("first IDs = %v, want %v", transactionIDs(first.Transactions), newestFirst[:2])
	}

	middle := mustList(t, ctx, store, transaction.ListInput{Limit: int64Ptr(2), Offset: int64Ptr(2)})
	if middle.Page != (contract.Page{Limit: 2, Offset: 2, Returned: 2, Total: 5, HasMore: true}) {
		t.Fatalf("middle page = %#v", middle.Page)
	}
	if !reflect.DeepEqual(transactionIDs(middle.Transactions), newestFirst[2:4]) {
		t.Fatalf("middle IDs = %v, want %v", transactionIDs(middle.Transactions), newestFirst[2:4])
	}

	last := mustList(t, ctx, store, transaction.ListInput{Limit: int64Ptr(2), Offset: int64Ptr(4)})
	if last.Page != (contract.Page{Limit: 2, Offset: 4, Returned: 1, Total: 5, HasMore: false}) {
		t.Fatalf("last page = %#v", last.Page)
	}
	if !reflect.DeepEqual(transactionIDs(last.Transactions), newestFirst[4:]) {
		t.Fatalf("last IDs = %v, want %v", transactionIDs(last.Transactions), newestFirst[4:])
	}

	atEnd := mustList(t, ctx, store, transaction.ListInput{Limit: int64Ptr(2), Offset: int64Ptr(5)})
	assertEmptyPage(t, atEnd, 2, 5, 5)

	beyond := mustList(t, ctx, store, transaction.ListInput{Limit: int64Ptr(2), Offset: int64Ptr(99)})
	assertEmptyPage(t, beyond, 2, 99, 5)

	omitted := mustList(t, ctx, store, transaction.ListInput{})
	if omitted.Page != (contract.Page{Limit: 50, Offset: 0, Returned: 5, Total: 5}) {
		t.Fatalf("omitted pagination page = %#v, want effective 50/0", omitted.Page)
	}
}

func TestListFilteredPaginationTotalsCountOnlyFilteredSet(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	createCategory(t, ctx, categories, "Dining")
	var groceryIDs []int64
	for _, date := range []string{"2026-08-01", "2026-08-05", "2026-08-10"} {
		groceryIDs = append(groceryIDs, addTransaction(t, ctx, store, transaction.AddInput{
			Amount: "1.00", Merchant: "G-" + date, Category: stringPtr("Groceries"), Date: stringPtr(date),
		}).ID)
	}
	addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "2.00", Merchant: "D1", Category: stringPtr("Dining"), Date: stringPtr("2026-08-12"),
	})
	addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "2.00", Merchant: "D2", Category: stringPtr("Dining"), Date: stringPtr("2026-08-14"),
	})

	page := mustList(t, ctx, store, transaction.ListInput{
		Category: stringPtr("Groceries"),
		Limit:    int64Ptr(1),
		Offset:   int64Ptr(0),
	})
	if page.Page != (contract.Page{Limit: 1, Offset: 0, Returned: 1, Total: 3, HasMore: true}) {
		t.Fatalf("filtered first page = %#v, want total 3 not 5", page.Page)
	}
	if page.Transactions[0].ID != groceryIDs[2] {
		t.Fatalf("filtered first row = %d, want newest grocery %d", page.Transactions[0].ID, groceryIDs[2])
	}

	empty := mustList(t, ctx, store, transaction.ListInput{
		Category:  stringPtr("Groceries"),
		StartDate: stringPtr("2026-09-01"),
		Limit:     int64Ptr(1),
	})
	assertEmptyPage(t, empty, 1, 0, 0)
}

func TestListClosedDatabaseWritesNothing(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	insertBudget(t, ctx, db, transactionCategoryID(seeded), "2026-08", "500.00")
	const frozen = "2020-01-01T00:00:00.000Z"
	freezeTransactionTimestamps(t, ctx, db, seeded.ID, frozen)
	freezeMappingTimestamps(t, ctx, db, loadStoredMapping(t, ctx, db, "Metro").ID, frozen)
	freezeBudgetTimestamps(t, ctx, db, frozen)

	if err := db.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
		t.Fatalf("close database: %v", err)
	}
	_, fields, err := store.List(ctx, transaction.ListInput{})
	if err == nil {
		t.Fatal("List(closed DB) error = nil, want failure")
	}
	if len(fields) != 0 {
		t.Fatalf("closed DB fields = %#v, want none", fields)
	}
}

func TestListNilStoreReturnsError(t *testing.T) {
	var store *transaction.Store
	_, fields, err := store.List(context.Background(), transaction.ListInput{})
	if err == nil {
		t.Fatal("List(nil store) error = nil")
	}
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}

	empty := &transaction.Store{}
	_, fields, err = empty.List(context.Background(), transaction.ListInput{})
	if err == nil {
		t.Fatal("List(nil DB) error = nil")
	}
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
}

func TestListDoesNotMutateTransactionsMappingsOrBudgets(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	createCategory(t, ctx, categories, "Dining")
	seeded := seedGroceryTransaction(t, ctx, store)
	addTransaction(t, ctx, store, transaction.AddInput{
		Amount: "12.00", Merchant: "Cafe", Category: stringPtr("Dining"), Date: stringPtr("2026-08-02"),
	})
	insertBudget(t, ctx, db, transactionCategoryID(seeded), "2026-08", "500.00")
	const frozen = "2020-01-01T00:00:00.000Z"
	for _, row := range listStoredTransactions(t, ctx, db) {
		freezeTransactionTimestamps(t, ctx, db, row.ID, frozen)
	}
	for _, row := range listStoredMappings(t, ctx, db) {
		freezeMappingTimestamps(t, ctx, db, row.ID, frozen)
	}
	freezeBudgetTimestamps(t, ctx, db, frozen)

	beforeTransactions := listStoredTransactions(t, ctx, db)
	beforeMappings := listStoredMappings(t, ctx, db)
	beforeBudgets := listStoredBudgets(t, ctx, db)

	mustList(t, ctx, store, transaction.ListInput{
		StartDate: stringPtr("2026-08-01"),
		EndDate:   stringPtr("2026-08-31"),
		Category:  stringPtr("Groceries"),
		Limit:     int64Ptr(1),
	})
	mustList(t, ctx, store, transaction.ListInput{})

	if !reflect.DeepEqual(listStoredTransactions(t, ctx, db), beforeTransactions) {
		t.Fatal("listing mutated transactions")
	}
	if !reflect.DeepEqual(listStoredMappings(t, ctx, db), beforeMappings) {
		t.Fatal("listing mutated known_merchants")
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db), beforeBudgets) {
		t.Fatal("listing mutated budgets")
	}
	if got := countTransactions(t, ctx, db); got != 2 {
		t.Fatalf("transaction count = %d, want 2", got)
	}
	if got := countMappings(t, ctx, db); got != 2 {
		t.Fatalf("mapping count = %d, want 2", got)
	}
	if got := countBudgets(t, ctx, db); got != 1 {
		t.Fatalf("budget count = %d, want 1", got)
	}
}
