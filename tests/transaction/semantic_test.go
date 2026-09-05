package transaction_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/transaction"
)

func TestLocalDateTorontoVsUTCSameInstant(t *testing.T) {
	local := torontoTime(t, 2026, 8, 15, 23, 30)
	utc := local.UTC()

	if got := utc.Format("2006-01-02 15:04"); got != "2026-08-16 03:30" {
		t.Fatalf("same instant in UTC = %s, want 2026-08-16 03:30", got)
	}
	if got := transaction.LocalDate(local); got != "2026-08-15" {
		t.Fatalf("LocalDate(Toronto 2026-08-15 23:30) = %q, want 2026-08-15", got)
	}
	if got := transaction.LocalDate(utc); got != "2026-08-16" {
		t.Fatalf("LocalDate(UTC of same instant) = %q, want 2026-08-16", got)
	}
}

func TestAddRejectsEmptyWhitespaceAndNULMerchant(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	for _, merchantName := range []string{"", " \t\n\r\v\f "} {
		_, fields, err := store.Add(ctx, transaction.AddInput{Amount: "1.00", Merchant: merchantName, Category: stringPtr("Groceries")})
		if err != nil {
			t.Fatalf("Add(%q) error = %v, want semantic issue", merchantName, err)
		}
		want := []contract.FieldIssue{{Field: "merchant", Reason: "must not be empty"}}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("Add(%q) fields = %#v, want %#v", merchantName, fields, want)
		}
	}

	_, fields, err := store.Add(ctx, transaction.AddInput{Amount: "1.00", Merchant: "Metro\x00", Category: stringPtr("Groceries")})
	if err != nil {
		t.Fatalf("Add(NUL merchant) error = %v, want semantic issue", err)
	}
	want := []contract.FieldIssue{{Field: "merchant", Reason: "must not contain NUL characters"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("Add(NUL merchant) fields = %#v, want %#v", fields, want)
	}
	assertNoWrites(t, ctx, db)
}

func TestAddTrimsASCIIMerchantWhitespaceAndPreservesUnicode(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	const nbsp = "\u00a0"

	result, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "1.00",
		Merchant: " \t\n\r\v\fMetro \t\n\r\v\f",
		Category: stringPtr("Groceries"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add(ASCII-padded Metro) = %#v fields %#v error %v", result, fields, err)
	}
	if result.Transaction.Merchant != "Metro" {
		t.Fatalf("trimmed merchant = %q, want Metro", result.Transaction.Merchant)
	}

	unicode, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "1.00",
		Merchant: nbsp + "Metro" + nbsp,
		Category: stringPtr("Groceries"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add(unicode-padded Metro) = %#v fields %#v error %v", unicode, fields, err)
	}
	if unicode.Transaction.Merchant != nbsp+"Metro"+nbsp {
		t.Fatalf("unicode merchant = %q, want NBSP preserved", unicode.Transaction.Merchant)
	}
	if got := countMappings(t, ctx, db); got != 2 {
		t.Fatalf("mapping count = %d, want 2 independent spellings", got)
	}
}

func TestAddNormalizesSuccessfulAmounts(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")

	tests := []struct {
		input string
		want  string
	}{
		{input: "0.01", want: "0.01"},
		{input: "1", want: "1.00"},
		{input: "1.0", want: "1.00"},
		{input: "20.50", want: "20.50"},
	}
	for _, tt := range tests {
		result, fields, err := store.Add(ctx, transaction.AddInput{
			Amount:   tt.input,
			Merchant: "Metro-" + tt.input,
			Category: stringPtr("Groceries"),
		})
		if err != nil || len(fields) != 0 {
			t.Fatalf("Add(amount %q) = %#v fields %#v error %v", tt.input, result, fields, err)
		}
		if result.Transaction.Amount != tt.want {
			t.Fatalf("Add(amount %q) amount = %q, want %q", tt.input, result.Transaction.Amount, tt.want)
		}
	}
}

func TestAddZeroAmountsReturnOnlyGreaterThanZero(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	want := []contract.FieldIssue{{Field: "amount", Reason: "must be greater than zero"}}
	for _, input := range []string{"0", "0.0", "0.00"} {
		_, fields, err := store.Add(ctx, transaction.AddInput{Amount: input, Merchant: "Metro", Category: stringPtr("Groceries")})
		if err != nil {
			t.Fatalf("Add(%q) error = %v, want semantic issue", input, err)
		}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("Add(%q) fields = %#v, want %#v", input, fields, want)
		}
	}
	assertNoWrites(t, ctx, db)
}

func TestAddRejectsInvalidAmountFormats(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	want := []contract.FieldIssue{{Field: "amount", Reason: "must be a positive amount with at most two decimal places"}}
	for _, input := range []string{
		"",
		"-1",
		"+20",
		"1e2",
		" 20",
		"20 ",
		"1,000",
		"1_000",
		"20.500",
		"92233720368547758.08",
	} {
		_, fields, err := store.Add(ctx, transaction.AddInput{Amount: input, Merchant: "Metro", Category: stringPtr("Groceries")})
		if err != nil {
			t.Fatalf("Add(%q) error = %v, want semantic issue", input, err)
		}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("Add(%q) fields = %#v, want %#v", input, fields, want)
		}
	}
	assertNoWrites(t, ctx, db)
}

func TestAddOmittedDateUsesCapturedLocalDate(t *testing.T) {
	ctx := context.Background()
	now := torontoTime(t, 2026, 8, 15, 12, 0)
	store, categories, _, _ := openTransactionStore(t, now)
	createCategory(t, ctx, categories, "Groceries")

	result, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "1.00",
		Merchant: "Metro",
		Category: stringPtr("Groceries"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add(omitted date) = %#v fields %#v error %v", result, fields, err)
	}
	if result.Transaction.Date != "2026-08-15" {
		t.Fatalf("omitted date = %q, want 2026-08-15", result.Transaction.Date)
	}
}

func TestAddFixedNonUTCClockAcceptsLocalDateDefaultAndSameDay(t *testing.T) {
	ctx := context.Background()
	now := torontoTime(t, 2026, 8, 15, 23, 30)
	store, categories, _, _ := openTransactionStore(t, now)
	createCategory(t, ctx, categories, "Groceries")

	omitted, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "1.00",
		Merchant: "DefaultDate",
		Category: stringPtr("Groceries"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add(default date) = %#v fields %#v error %v", omitted, fields, err)
	}
	if omitted.Transaction.Date != "2026-08-15" {
		t.Fatalf("default date = %q, want 2026-08-15", omitted.Transaction.Date)
	}

	sameDay, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "1.00",
		Merchant: "SameDay",
		Category: stringPtr("Groceries"),
		Date:     stringPtr("2026-08-15"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add(same-day date) = %#v fields %#v error %v", sameDay, fields, err)
	}
	if sameDay.Transaction.Date != "2026-08-15" {
		t.Fatalf("same-day date = %q, want 2026-08-15", sameDay.Transaction.Date)
	}
}

func TestAddDateBoundaryUsesLocalClockBeforeUTC(t *testing.T) {
	ctx := context.Background()
	local := torontoTime(t, 2026, 8, 15, 23, 30)
	utc := local.UTC()

	torontoStore, torontoCategories, _, _ := openTransactionStore(t, local)
	createCategory(t, ctx, torontoCategories, "Groceries")
	torontoResult, fields, err := torontoStore.Add(ctx, transaction.AddInput{
		Amount:   "1.00",
		Merchant: "Toronto",
		Category: stringPtr("Groceries"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add(Toronto clock) = %#v fields %#v error %v", torontoResult, fields, err)
	}
	if torontoResult.Transaction.Date != "2026-08-15" {
		t.Fatalf("Toronto default date = %q, want 2026-08-15", torontoResult.Transaction.Date)
	}

	utcStore, utcCategories, _, _ := openTransactionStore(t, utc)
	createCategory(t, ctx, utcCategories, "Groceries")
	utcResult, fields, err := utcStore.Add(ctx, transaction.AddInput{
		Amount:   "1.00",
		Merchant: "UTC",
		Category: stringPtr("Groceries"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add(UTC clock) = %#v fields %#v error %v", utcResult, fields, err)
	}
	if utcResult.Transaction.Date != "2026-08-16" {
		t.Fatalf("UTC default date = %q, want 2026-08-16", utcResult.Transaction.Date)
	}
}

func TestAddStoresSuppliedPastDateUnchanged(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")

	result, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "1.00",
		Merchant: "Metro",
		Category: stringPtr("Groceries"),
		Date:     stringPtr("2026-08-01"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add(past date) = %#v fields %#v error %v", result, fields, err)
	}
	if result.Transaction.Date != "2026-08-01" {
		t.Fatalf("past date = %q, want 2026-08-01", result.Transaction.Date)
	}
}

func TestAddFutureDateWritesNothing(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "1.00",
		Merchant: "Metro",
		Category: stringPtr("Groceries"),
		Date:     stringPtr("2026-08-16"),
	})
	if err != nil {
		t.Fatalf("Add(future date) error = %v, want semantic issue", err)
	}
	want := []contract.FieldIssue{{Field: "date", Reason: "must not be in the future"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("future date fields = %#v, want %#v", fields, want)
	}
	assertNoWrites(t, ctx, db)
}

func TestAddMalformedDateReturnsOnlyCanonicalReason(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	want := []contract.FieldIssue{{Field: "date", Reason: "must be a valid YYYY-MM-DD date"}}
	for _, input := range []string{"", "2026-8-16", "2026-13-01", "9999-13-01", "2026/08/15", "2026-02-29"} {
		_, fields, err := store.Add(ctx, transaction.AddInput{
			Amount:   "1.00",
			Merchant: "Metro",
			Category: stringPtr("Groceries"),
			Date:     stringPtr(input),
		})
		if err != nil {
			t.Fatalf("Add(date %q) error = %v, want semantic issue", input, err)
		}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("Add(date %q) fields = %#v, want %#v", input, fields, want)
		}
	}
	assertNoWrites(t, ctx, db)
}

func TestAddDoesNotTrimDates(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	want := []contract.FieldIssue{{Field: "date", Reason: "must be a valid YYYY-MM-DD date"}}
	for _, input := range []string{" 2026-08-15", "2026-08-15 "} {
		_, fields, err := store.Add(ctx, transaction.AddInput{
			Amount:   "1.00",
			Merchant: "Metro",
			Category: stringPtr("Groceries"),
			Date:     stringPtr(input),
		})
		if err != nil {
			t.Fatalf("Add(padded date %q) error = %v, want semantic issue", input, err)
		}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("Add(padded date %q) fields = %#v, want %#v", input, fields, want)
		}
	}
	assertNoWrites(t, ctx, db)
}

func TestAddSuppliedEmptyCategoryIsInvalidInputNotRequired(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	for _, categoryName := range []string{"", " \t"} {
		_, fields, err := store.Add(ctx, transaction.AddInput{
			Amount:   "1.00",
			Merchant: "Metro",
			Category: stringPtr(categoryName),
		})
		if err != nil {
			t.Fatalf("Add(empty category %q) error = %v, want semantic issue", categoryName, err)
		}
		want := []contract.FieldIssue{{Field: "category", Reason: "must not be empty"}}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("Add(empty category %q) fields = %#v, want %#v", categoryName, fields, want)
		}
		if errors.Is(err, transaction.ErrMerchantCategoryRequired) {
			t.Fatal("empty supplied category returned merchant_category_required")
		}
	}
	assertNoWrites(t, ctx, db)
}

func TestAddOmittedCategoryIsNotSemanticError(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.Add(ctx, transaction.AddInput{Amount: "1.00", Merchant: "Metro"})
	if len(fields) != 0 {
		t.Fatalf("omitted category fields = %#v, want none", fields)
	}
	var required *transaction.MerchantCategoryRequiredError
	if !errors.As(err, &required) || !errors.Is(err, transaction.ErrMerchantCategoryRequired) {
		t.Fatalf("omitted category error = %v, want MerchantCategoryRequiredError", err)
	}
	if required.Merchant != "Metro" {
		t.Fatalf("required merchant = %q, want Metro", required.Merchant)
	}
	assertNoWrites(t, ctx, db)
}

func TestAddRejectsNULCategoryAndNote(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "1.00",
		Merchant: "Metro",
		Category: stringPtr("Groceries\x00"),
		Note:     stringPtr("weekly\x00"),
	})
	if err != nil {
		t.Fatalf("Add(NUL category/note) error = %v, want semantic issues", err)
	}
	want := []contract.FieldIssue{
		{Field: "category", Reason: "must not contain NUL characters"},
		{Field: "note", Reason: "must not contain NUL characters"},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("NUL fields = %#v, want %#v", fields, want)
	}
	assertNoWrites(t, ctx, db)
}

func TestAddEmptyAndWhitespaceNotesBecomeNull(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")

	omitted, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "1.00",
		Merchant: "OmittedNote",
		Category: stringPtr("Groceries"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add(omitted note) = %#v fields %#v error %v", omitted, fields, err)
	}
	if omitted.Transaction.Note != nil {
		t.Fatalf("omitted note = %#v, want nil", omitted.Transaction.Note)
	}

	for _, note := range []string{"", " \t\n\r\v\f "} {
		result, fields, err := store.Add(ctx, transaction.AddInput{
			Amount:   "1.00",
			Merchant: "Note-" + note,
			Category: stringPtr("Groceries"),
			Note:     stringPtr(note),
		})
		if err != nil || len(fields) != 0 {
			t.Fatalf("Add(note %q) = %#v fields %#v error %v", note, result, fields, err)
		}
		if result.Transaction.Note != nil {
			t.Fatalf("note %q stored as %#v, want nil", note, result.Transaction.Note)
		}
	}

	trimmed, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "1.00",
		Merchant: "TrimmedNote",
		Category: stringPtr("Groceries"),
		Note:     stringPtr("  weekly  "),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add(trimmed note) = %#v fields %#v error %v", trimmed, fields, err)
	}
	if trimmed.Transaction.Note == nil || *trimmed.Transaction.Note != "weekly" {
		t.Fatalf("trimmed note = %#v, want weekly", trimmed.Transaction.Note)
	}
}

func TestAddCollectsSemanticIssuesInFieldOrder(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "-1",
		Merchant: " \t",
		Category: stringPtr(" "),
		Date:     stringPtr(" 2026-08-16"),
		Note:     stringPtr("weekly\x00"),
	})
	if err != nil {
		t.Fatalf("Add(multi) error = %v, want semantic issues", err)
	}
	want := []contract.FieldIssue{
		{Field: "amount", Reason: "must be a positive amount with at most two decimal places"},
		{Field: "merchant", Reason: "must not be empty"},
		{Field: "category", Reason: "must not be empty"},
		{Field: "date", Reason: "must be a valid YYYY-MM-DD date"},
		{Field: "note", Reason: "must not contain NUL characters"},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("multi fields = %#v, want %#v", fields, want)
	}
	assertNoWrites(t, ctx, db)
}

func TestAddCapturesInjectedNowOnce(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	called := 0
	store.Now = func() time.Time {
		called++
		return torontoTime(t, 2026, 8, 15, 12, 0)
	}

	_, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "1.00",
		Merchant: "Metro",
		Date:     stringPtr("2026-08-16"),
	})
	if err != nil {
		t.Fatalf("Add() error = %v, want semantic issue", err)
	}
	if len(fields) != 1 || fields[0].Field != "date" {
		t.Fatalf("fields = %#v, want future date", fields)
	}
	if called != 1 {
		t.Fatalf("Now() calls = %d, want 1", called)
	}
	assertNoWrites(t, ctx, db)
}

func TestAddDoesNotFoldUnicodeBeyondSQLiteNoCase(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")

	created, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "1.00",
		Merchant: "Café",
		Category: stringPtr("Groceries"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add(Café) = %#v fields %#v error %v", created, fields, err)
	}

	_, fields, err = store.Add(ctx, transaction.AddInput{Amount: "1.00", Merchant: "CAFÉ"})
	if len(fields) != 0 {
		t.Fatalf("Add(CAFÉ) fields = %#v, want none", fields)
	}
	var required *transaction.MerchantCategoryRequiredError
	if !errors.As(err, &required) || required.Merchant != "CAFÉ" {
		t.Fatalf("Add(CAFÉ) error = %v, want independent merchant", err)
	}
	if got := countMappings(t, ctx, db); got != 1 {
		t.Fatalf("mapping count after unfolded CAFÉ = %d, want 1", got)
	}
}
