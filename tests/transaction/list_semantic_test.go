package transaction_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/transaction"
)

func TestListOmittedEverythingUsesDefaultsAndNoFilter(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	createCategory(t, ctx, categories, "Dining")
	groceries := addTransaction(t, ctx, store, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Metro",
		Category: stringPtr("Groceries"),
		Date:     stringPtr("2026-08-01"),
	})
	dining := addTransaction(t, ctx, store, transaction.AddInput{
		Amount:   "15.00",
		Merchant: "Cafe",
		Category: stringPtr("Dining"),
		Date:     stringPtr("2026-07-31"),
	})

	result := mustList(t, ctx, store, transaction.ListInput{})
	if result.Page != (contract.Page{Limit: 50, Offset: 0, Returned: 2, Total: 2}) {
		t.Fatalf("page = %#v, want default limit 50 offset 0 over both rows", result.Page)
	}
	if len(result.Transactions) != 2 || result.Transactions[0].ID != groceries.ID || result.Transactions[1].ID != dining.ID {
		t.Fatalf("transactions = %#v, want both rows newest-first", result.Transactions)
	}
}

func TestListMalformedStartDateReturnsOnlyCanonicalReason(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	dropTransactionsTable(t, ctx, db)
	want := []contract.FieldIssue{{Field: "start_date", Reason: "must be a valid YYYY-MM-DD date"}}

	for _, input := range []string{"", "2026-8-16", "2026-13-01", "9999-13-01", "2026/08/15", "2026-02-29"} {
		_, fields, err := store.List(ctx, transaction.ListInput{StartDate: stringPtr(input)})
		if err != nil {
			t.Fatalf("List(start %q) error = %v, want semantic issue", input, err)
		}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("List(start %q) fields = %#v, want %#v", input, fields, want)
		}
	}
}

func TestListMalformedEndDateReturnsOnlyCanonicalReason(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	dropTransactionsTable(t, ctx, db)
	want := []contract.FieldIssue{{Field: "end_date", Reason: "must be a valid YYYY-MM-DD date"}}

	for _, input := range []string{"", "2026-8-16", "2026-13-01", "2026/08/15", "2026-02-29"} {
		_, fields, err := store.List(ctx, transaction.ListInput{EndDate: stringPtr(input)})
		if err != nil {
			t.Fatalf("List(end %q) error = %v, want semantic issue", input, err)
		}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("List(end %q) fields = %#v, want %#v", input, fields, want)
		}
	}
}

func TestListDoesNotTrimDates(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	dropTransactionsTable(t, ctx, db)

	for _, field := range []struct {
		name  string
		input transaction.ListInput
	}{
		{name: "start leading", input: transaction.ListInput{StartDate: stringPtr(" 2026-08-01")}},
		{name: "start trailing", input: transaction.ListInput{StartDate: stringPtr("2026-08-01 ")}},
		{name: "end leading", input: transaction.ListInput{EndDate: stringPtr(" 2026-08-31")}},
		{name: "end trailing", input: transaction.ListInput{EndDate: stringPtr("2026-08-31 ")}},
	} {
		_, fields, err := store.List(ctx, field.input)
		if err != nil {
			t.Fatalf("List(%s) error = %v, want semantic issue", field.name, err)
		}
		if len(fields) != 1 || fields[0].Reason != "must be a valid YYYY-MM-DD date" {
			t.Fatalf("List(%s) fields = %#v, want canonical-date issue", field.name, fields)
		}
	}
}

func TestListReversedRangeReturnsOnlyEndDateIssue(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	dropTransactionsTable(t, ctx, db)

	_, fields, err := store.List(ctx, transaction.ListInput{
		StartDate: stringPtr("2026-08-31"),
		EndDate:   stringPtr("2026-08-01"),
	})
	if err != nil {
		t.Fatalf("List(reversed) error = %v, want semantic issue", err)
	}
	want := []contract.FieldIssue{{Field: "end_date", Reason: "must be on or after start_date"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("reversed fields = %#v, want %#v", fields, want)
	}
}

func TestListBothMalformedDatesReturnTwoCanonicalIssuesNoReversedRange(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	dropTransactionsTable(t, ctx, db)

	_, fields, err := store.List(ctx, transaction.ListInput{
		StartDate: stringPtr("2026-8-31"),
		EndDate:   stringPtr("2026/08/01"),
	})
	if err != nil {
		t.Fatalf("List(both malformed) error = %v, want semantic issues", err)
	}
	want := []contract.FieldIssue{
		{Field: "start_date", Reason: "must be a valid YYYY-MM-DD date"},
		{Field: "end_date", Reason: "must be a valid YYYY-MM-DD date"},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("both malformed fields = %#v, want %#v", fields, want)
	}

	_, fields, err = store.List(ctx, transaction.ListInput{
		StartDate: stringPtr("2026-08-31"),
		EndDate:   stringPtr("not-a-date"),
	})
	if err != nil {
		t.Fatalf("List(valid start, bad end) error = %v, want semantic issue", err)
	}
	want = []contract.FieldIssue{{Field: "end_date", Reason: "must be a valid YYYY-MM-DD date"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("one malformed fields = %#v, want %#v without reversed-range", fields, want)
	}
}

func TestListEqualDatesAreValid(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	sameDay := addTransaction(t, ctx, store, transaction.AddInput{
		Amount:   "8.00",
		Merchant: "Metro",
		Category: stringPtr("Groceries"),
		Date:     stringPtr("2026-08-14"),
	})
	addTransaction(t, ctx, store, transaction.AddInput{
		Amount:   "9.00",
		Merchant: "Later Metro",
		Category: stringPtr("Groceries"),
		Date:     stringPtr("2026-08-15"),
	})

	result := mustList(t, ctx, store, transaction.ListInput{
		StartDate: stringPtr("2026-08-14"),
		EndDate:   stringPtr("2026-08-14"),
	})
	if result.Page.Total != 1 || len(result.Transactions) != 1 || result.Transactions[0].ID != sameDay.ID {
		t.Fatalf("equal-date list = %#v, want only the 2026-08-14 row", result)
	}
}

func TestListFutureCanonicalBoundIsValid(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	stored := addTransaction(t, ctx, store, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Metro",
		Category: stringPtr("Groceries"),
		Date:     stringPtr("2026-08-01"),
	})

	empty := mustList(t, ctx, store, transaction.ListInput{StartDate: stringPtr("2026-08-16")})
	assertEmptyPage(t, empty, 50, 0, 0)

	included := mustList(t, ctx, store, transaction.ListInput{
		StartDate: stringPtr("2026-08-01"),
		EndDate:   stringPtr("2026-12-31"),
	})
	if included.Page.Total != 1 || included.Transactions[0].ID != stored.ID {
		t.Fatalf("future end bound = %#v, want stored row included", included)
	}
}

func TestListEmptyWhitespaceAndNULCategoryAreInvalid(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	dropTransactionsTable(t, ctx, db)

	for _, categoryName := range []string{"", " \t\n\r\v\f "} {
		_, fields, err := store.List(ctx, transaction.ListInput{Category: stringPtr(categoryName)})
		if err != nil {
			t.Fatalf("List(empty category %q) error = %v, want semantic issue", categoryName, err)
		}
		want := []contract.FieldIssue{{Field: "category", Reason: "must not be empty"}}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("List(empty category %q) fields = %#v, want %#v", categoryName, fields, want)
		}
		if errors.Is(err, transaction.ErrCategoryNotFound) {
			t.Fatal("empty supplied category returned category_not_found")
		}
	}

	_, fields, err := store.List(ctx, transaction.ListInput{Category: stringPtr("Groceries\x00")})
	if err != nil {
		t.Fatalf("List(NUL category) error = %v, want semantic issue", err)
	}
	want := []contract.FieldIssue{{Field: "category", Reason: "must not contain NUL characters"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("NUL category fields = %#v, want %#v", fields, want)
	}
}

func TestListOmittedCategoryIsNotSemanticError(t *testing.T) {
	ctx := context.Background()
	store, _, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	result, fields, err := store.List(ctx, transaction.ListInput{})
	if err != nil || len(fields) != 0 {
		t.Fatalf("List(omitted category) = %#v fields %#v error %v", result, fields, err)
	}
	if errors.Is(err, transaction.ErrCategoryNotFound) {
		t.Fatal("omitted category returned category_not_found")
	}
}

func TestListEmptyWhitespaceAndNULMerchantAreInvalid(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	dropTransactionsTable(t, ctx, db)

	for _, merchantName := range []string{"", " \t\n\r\v\f "} {
		_, fields, err := store.List(ctx, transaction.ListInput{Merchant: stringPtr(merchantName)})
		if err != nil {
			t.Fatalf("List(empty merchant %q) error = %v, want semantic issue", merchantName, err)
		}
		want := []contract.FieldIssue{{Field: "merchant", Reason: "must not be empty"}}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("List(empty merchant %q) fields = %#v, want %#v", merchantName, fields, want)
		}
	}

	_, fields, err := store.List(ctx, transaction.ListInput{Merchant: stringPtr("Metro\x00")})
	if err != nil {
		t.Fatalf("List(NUL merchant) error = %v, want semantic issue", err)
	}
	want := []contract.FieldIssue{{Field: "merchant", Reason: "must not contain NUL characters"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("NUL merchant fields = %#v, want %#v", fields, want)
	}
}

func TestListTrimsASCIICategoryWhitespaceAndPreservesUnicode(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	stored := addTransaction(t, ctx, store, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Metro",
		Category: stringPtr("Groceries"),
		Date:     stringPtr("2026-08-01"),
	})
	const nbsp = "\u00a0"

	trimmed := mustList(t, ctx, store, transaction.ListInput{Category: stringPtr(" \t\n\r\v\fGroceries \t\n\r\v\f")})
	if trimmed.Page.Total != 1 || trimmed.Transactions[0].ID != stored.ID || transactionCategory(trimmed.Transactions[0]) != "Groceries" {
		t.Fatalf("ASCII-trimmed category list = %#v, want stored Groceries row", trimmed)
	}

	_, fields, err := store.List(ctx, transaction.ListInput{Category: stringPtr(nbsp + "Groceries" + nbsp)})
	if len(fields) != 0 {
		t.Fatalf("unicode-padded category fields = %#v, want domain lookup", fields)
	}
	var missing *transaction.CategoryNotFoundError
	if !errors.As(err, &missing) || missing.Requested != nbsp+"Groceries"+nbsp {
		t.Fatalf("unicode-padded category error = %v, want CategoryNotFoundError with NBSP preserved", err)
	}
}

func TestListLimitBoundaries(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	for _, limit := range []int64{1, 200} {
		result, fields, err := store.List(ctx, transaction.ListInput{Limit: int64Ptr(limit)})
		if err != nil || len(fields) != 0 {
			t.Fatalf("List(limit %d) = %#v fields %#v error %v", limit, result, fields, err)
		}
		if result.Page.Limit != limit {
			t.Fatalf("effective limit = %d, want %d", result.Page.Limit, limit)
		}
	}

	dropTransactionsTable(t, ctx, db)
	want := []contract.FieldIssue{{Field: "limit", Reason: "must be between 1 and 200"}}
	for _, limit := range []int64{0, -1, 201} {
		_, fields, err := store.List(ctx, transaction.ListInput{Limit: int64Ptr(limit)})
		if err != nil {
			t.Fatalf("List(limit %d) error = %v, want semantic issue", limit, err)
		}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("List(limit %d) fields = %#v, want %#v", limit, fields, want)
		}
	}
}

func TestListOffsetBoundaries(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	result, fields, err := store.List(ctx, transaction.ListInput{Offset: int64Ptr(0)})
	if err != nil || len(fields) != 0 {
		t.Fatalf("List(offset 0) = %#v fields %#v error %v", result, fields, err)
	}
	if result.Page.Offset != 0 {
		t.Fatalf("effective offset = %d, want 0", result.Page.Offset)
	}

	dropTransactionsTable(t, ctx, db)
	_, fields, err = store.List(ctx, transaction.ListInput{Offset: int64Ptr(-1)})
	if err != nil {
		t.Fatalf("List(offset -1) error = %v, want semantic issue", err)
	}
	want := []contract.FieldIssue{{Field: "offset", Reason: "must be zero or greater"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("negative offset fields = %#v, want %#v", fields, want)
	}
}

func TestListCollectsSemanticIssuesInFieldOrder(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	dropTransactionsTable(t, ctx, db)

	_, fields, err := store.List(ctx, transaction.ListInput{
		StartDate: stringPtr("2026-8-31"),
		EndDate:   stringPtr("2026/08/01"),
		Category:  stringPtr(" "),
		Merchant:  stringPtr(" "),
		Limit:     int64Ptr(0),
		Offset:    int64Ptr(-1),
	})
	if err != nil {
		t.Fatalf("List(multi) error = %v, want semantic issues", err)
	}
	want := []contract.FieldIssue{
		{Field: "start_date", Reason: "must be a valid YYYY-MM-DD date"},
		{Field: "end_date", Reason: "must be a valid YYYY-MM-DD date"},
		{Field: "category", Reason: "must not be empty"},
		{Field: "merchant", Reason: "must not be empty"},
		{Field: "limit", Reason: "must be between 1 and 200"},
		{Field: "offset", Reason: "must be zero or greater"},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("multi fields = %#v, want %#v", fields, want)
	}
}

func TestListDoesNotQueryWhenSemanticFieldsAreNonEmpty(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	dropTransactionsTable(t, ctx, db)

	_, fields, err := store.List(ctx, transaction.ListInput{
		StartDate: stringPtr("bad"),
		Category:  stringPtr("Pharmacy"),
	})
	if err != nil {
		t.Fatalf("List() error = %v, want semantic issue without query", err)
	}
	want := []contract.FieldIssue{{Field: "start_date", Reason: "must be a valid YYYY-MM-DD date"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("fields = %#v, want %#v", fields, want)
	}
	if errors.Is(err, transaction.ErrCategoryNotFound) {
		t.Fatal("semantic failure queried the database")
	}
}

func TestListDoesNotCaptureNow(t *testing.T) {
	ctx := context.Background()
	store, _, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	store.Now = func() time.Time {
		t.Fatal("List must not capture Now()")
		return time.Time{}
	}

	result, fields, err := store.List(ctx, transaction.ListInput{StartDate: stringPtr("2099-01-01")})
	if err != nil || len(fields) != 0 {
		t.Fatalf("List(future bound) = %#v fields %#v error %v", result, fields, err)
	}
}

func dropTransactionsTable(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `DROP TABLE transactions`); err != nil {
		t.Fatalf("drop transactions: %v", err)
	}
}
