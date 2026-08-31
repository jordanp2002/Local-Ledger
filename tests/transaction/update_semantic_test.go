package transaction_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/jordanp2002/local-finance-mcp/internal/transaction"
)

func TestUpdateRejectsZeroAndNegativeIDs(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	before := loadStoredTransaction(t, ctx, db, seeded.ID)
	want := []contract.FieldIssue{
		{Field: "id", Reason: "must be a positive integer"},
		{Field: "id", Reason: "at least one of amount, merchant, category, date, or note must be supplied"},
	}

	for _, id := range []int64{0, -1, -42} {
		_, fields, err := store.Update(ctx, transaction.UpdateInput{ID: id})
		if err != nil {
			t.Fatalf("Update(id=%d) error = %v, want semantic issues", id, err)
		}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("Update(id=%d) fields = %#v, want %#v", id, fields, want)
		}
	}
	assertStoredUnchanged(t, ctx, db, before)
}

func TestUpdateNoMutableFieldReturnsOnlyThatIssue(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	before := loadStoredTransaction(t, ctx, db, seeded.ID)
	want := []contract.FieldIssue{{
		Field:  "id",
		Reason: "at least one of amount, merchant, category, date, or note must be supplied",
	}}

	for _, id := range []int64{seeded.ID, 42, 999} {
		_, fields, err := store.Update(ctx, transaction.UpdateInput{ID: id})
		if err != nil {
			t.Fatalf("Update(id=%d) error = %v, want semantic issue", id, err)
		}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("Update(id=%d) fields = %#v, want %#v", id, fields, want)
		}
		var notFound *transaction.TransactionNotFoundError
		if errors.As(err, &notFound) {
			t.Fatal("no-mutable-field request queried the database")
		}
	}
	assertStoredUnchanged(t, ctx, db, before)
}

func TestUpdateZeroIDWithoutPatchReturnsBothIDReasons(t *testing.T) {
	ctx := context.Background()
	store, _, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.Update(ctx, transaction.UpdateInput{ID: 0})
	if err != nil {
		t.Fatalf("Update(id=0) error = %v, want semantic issues", err)
	}
	want := []contract.FieldIssue{
		{Field: "id", Reason: "must be a positive integer"},
		{Field: "id", Reason: "at least one of amount, merchant, category, date, or note must be supplied"},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("fields = %#v, want %#v", fields, want)
	}
}

func TestUpdateZeroIDWithAmountNullReturnsIDAndAmountIssues(t *testing.T) {
	ctx := context.Background()
	store, _, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.Update(ctx, transaction.UpdateInput{ID: 0, AmountNull: true})
	if err != nil {
		t.Fatalf("Update(id=0, amount null) error = %v, want semantic issues", err)
	}
	want := []contract.FieldIssue{
		{Field: "id", Reason: "must be a positive integer"},
		{Field: "amount", Reason: "must not be null"},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("fields = %#v, want %#v", fields, want)
	}
}

func TestUpdateExplicitNullsCountAsPresentAndStayInFieldOrder(t *testing.T) {
	ctx := context.Background()
	store, _, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.Update(ctx, transaction.UpdateInput{
		ID:           0,
		AmountNull:   true,
		MerchantNull: true,
		Category:     stringPtr(""),
		DateNull:     true,
		Note:         noteValue("bad\x00"),
	})
	if err != nil {
		t.Fatalf("Update(nulls) error = %v, want semantic issues", err)
	}
	want := []contract.FieldIssue{
		{Field: "id", Reason: "must be a positive integer"},
		{Field: "amount", Reason: "must not be null"},
		{Field: "merchant", Reason: "must not be null"},
		{Field: "category", Reason: "must not be empty"},
		{Field: "date", Reason: "must not be null"},
		{Field: "note", Reason: "must not contain NUL characters"},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("fields = %#v, want %#v", fields, want)
	}
}

func TestUpdateNotePresentCountsAsMutable(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)

	result := mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Note: noteClear()})
	if result.Transaction.Note != nil {
		t.Fatalf("cleared note = %#v, want nil", result.Transaction.Note)
	}
	if loadStoredTransaction(t, ctx, db, seeded.ID).Note.Valid {
		t.Fatal("cleared note left a stored string")
	}

	_, fields, err := store.Update(ctx, transaction.UpdateInput{ID: 0, Note: noteClear()})
	if err != nil {
		t.Fatalf("Update(id=0, note clear) error = %v, want semantic issue", err)
	}
	want := []contract.FieldIssue{{Field: "id", Reason: "must be a positive integer"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("note-present zero id fields = %#v, want %#v", fields, want)
	}
}

func TestUpdateRejectsEmptyWhitespaceAndNULMerchant(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	before := loadStoredTransaction(t, ctx, db, seeded.ID)

	for _, merchantName := range []string{"", " \t\n\r\v\f "} {
		_, fields, err := store.Update(ctx, transaction.UpdateInput{ID: seeded.ID, Merchant: stringPtr(merchantName)})
		if err != nil {
			t.Fatalf("Update(%q) error = %v, want semantic issue", merchantName, err)
		}
		want := []contract.FieldIssue{{Field: "merchant", Reason: "must not be empty"}}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("Update(%q) fields = %#v, want %#v", merchantName, fields, want)
		}
	}

	_, fields, err := store.Update(ctx, transaction.UpdateInput{ID: seeded.ID, Merchant: stringPtr("Metro\x00")})
	if err != nil {
		t.Fatalf("Update(NUL merchant) error = %v, want semantic issue", err)
	}
	want := []contract.FieldIssue{{Field: "merchant", Reason: "must not contain NUL characters"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("NUL merchant fields = %#v, want %#v", fields, want)
	}
	assertStoredUnchanged(t, ctx, db, before)
}

func TestUpdateTrimsASCIIMerchantWhitespaceAndPreservesUnicode(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	const nbsp = "\u00a0"

	trimmed := mustUpdate(t, ctx, store, transaction.UpdateInput{
		ID:       seeded.ID,
		Merchant: stringPtr(" \t\n\r\v\fMetro grocery \t\n\r\v\f"),
	})
	if trimmed.Transaction.Merchant != "Metro grocery" {
		t.Fatalf("trimmed merchant = %q, want Metro grocery", trimmed.Transaction.Merchant)
	}

	unicode := mustUpdate(t, ctx, store, transaction.UpdateInput{
		ID:       seeded.ID,
		Merchant: stringPtr(nbsp + "Metro" + nbsp),
	})
	if unicode.Transaction.Merchant != nbsp+"Metro"+nbsp {
		t.Fatalf("unicode merchant = %q, want NBSP preserved", unicode.Transaction.Merchant)
	}
	if got := countMappings(t, ctx, db); got != 1 {
		t.Fatalf("mapping count = %d, want 1 original Metro mapping", got)
	}
}

func TestUpdateNormalizesSuccessfulAmounts(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)

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
		result := mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Amount: stringPtr(tt.input)})
		if result.Transaction.Amount != tt.want {
			t.Fatalf("Update(amount %q) amount = %q, want %q", tt.input, result.Transaction.Amount, tt.want)
		}
	}
}

func TestUpdateZeroAmountsReturnOnlyGreaterThanZero(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	before := loadStoredTransaction(t, ctx, db, seeded.ID)
	want := []contract.FieldIssue{{Field: "amount", Reason: "must be greater than zero"}}

	for _, input := range []string{"0", "0.0", "0.00"} {
		_, fields, err := store.Update(ctx, transaction.UpdateInput{ID: seeded.ID, Amount: stringPtr(input)})
		if err != nil {
			t.Fatalf("Update(%q) error = %v, want semantic issue", input, err)
		}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("Update(%q) fields = %#v, want %#v", input, fields, want)
		}
	}
	assertStoredUnchanged(t, ctx, db, before)
}

func TestUpdateRejectsInvalidAmountFormats(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	before := loadStoredTransaction(t, ctx, db, seeded.ID)
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
		_, fields, err := store.Update(ctx, transaction.UpdateInput{ID: seeded.ID, Amount: stringPtr(input)})
		if err != nil {
			t.Fatalf("Update(%q) error = %v, want semantic issue", input, err)
		}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("Update(%q) fields = %#v, want %#v", input, fields, want)
		}
	}
	assertStoredUnchanged(t, ctx, db, before)
}

func TestUpdateOmittedDateLeavesHistoricalDate(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	if seeded.Date != "2026-08-01" {
		t.Fatalf("seed date = %q, want 2026-08-01", seeded.Date)
	}

	result := mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Amount: stringPtr("23.50")})
	if result.Transaction.Date != "2026-08-01" {
		t.Fatalf("omitted date rewritten to %q, want 2026-08-01", result.Transaction.Date)
	}
	if loadStoredTransaction(t, ctx, db, seeded.ID).Date != "2026-08-01" {
		t.Fatalf("stored date = %q, want historical 2026-08-01", loadStoredTransaction(t, ctx, db, seeded.ID).Date)
	}
}

func TestUpdateStoresSuppliedPastDate(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)

	result := mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Date: stringPtr("2026-07-31")})
	if result.Transaction.Date != "2026-07-31" {
		t.Fatalf("past date = %q, want 2026-07-31", result.Transaction.Date)
	}
}

func TestUpdateAcceptsCurrentLocalDate(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)

	result := mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Date: stringPtr("2026-08-15")})
	if result.Transaction.Date != "2026-08-15" {
		t.Fatalf("current local date = %q, want 2026-08-15", result.Transaction.Date)
	}
}

func TestUpdateDateBoundaryUsesLocalClockBeforeUTC(t *testing.T) {
	ctx := context.Background()
	local := torontoTime(t, 2026, 8, 15, 23, 30)
	utc := local.UTC()

	torontoStore, torontoCategories, _, torontoDB := openTransactionStore(t, local)
	createCategory(t, ctx, torontoCategories, "Groceries")
	torontoSeed := seedGroceryTransaction(t, ctx, torontoStore)
	torontoBefore := loadStoredTransaction(t, ctx, torontoDB, torontoSeed.ID)

	torontoToday := mustUpdate(t, ctx, torontoStore, transaction.UpdateInput{
		ID:   torontoSeed.ID,
		Date: stringPtr("2026-08-15"),
	})
	if torontoToday.Transaction.Date != "2026-08-15" {
		t.Fatalf("Toronto today = %q, want 2026-08-15", torontoToday.Transaction.Date)
	}

	_, fields, err := torontoStore.Update(ctx, transaction.UpdateInput{
		ID:   torontoSeed.ID,
		Date: stringPtr("2026-08-16"),
	})
	if err != nil {
		t.Fatalf("Toronto future error = %v, want semantic issue", err)
	}
	want := []contract.FieldIssue{{Field: "date", Reason: "must not be in the future"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("Toronto future fields = %#v, want %#v", fields, want)
	}
	if got := loadStoredTransaction(t, ctx, torontoDB, torontoSeed.ID).Date; got != "2026-08-15" {
		t.Fatalf("Toronto stored date after future reject = %q, want 2026-08-15", got)
	}
	if loadStoredTransaction(t, ctx, torontoDB, torontoSeed.ID).AmountHundredths != torontoBefore.AmountHundredths {
		t.Fatal("Toronto future date wrote other columns")
	}

	utcStore, utcCategories, _, _ := openTransactionStore(t, utc)
	createCategory(t, ctx, utcCategories, "Groceries")
	utcSeed := seedGroceryTransaction(t, ctx, utcStore)
	utcToday := mustUpdate(t, ctx, utcStore, transaction.UpdateInput{
		ID:   utcSeed.ID,
		Date: stringPtr("2026-08-16"),
	})
	if utcToday.Transaction.Date != "2026-08-16" {
		t.Fatalf("UTC today = %q, want 2026-08-16", utcToday.Transaction.Date)
	}
}

func TestUpdateFutureDateWritesNothing(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	before := loadStoredTransaction(t, ctx, db, seeded.ID)

	_, fields, err := store.Update(ctx, transaction.UpdateInput{ID: seeded.ID, Date: stringPtr("2026-08-16")})
	if err != nil {
		t.Fatalf("Update(future date) error = %v, want semantic issue", err)
	}
	want := []contract.FieldIssue{{Field: "date", Reason: "must not be in the future"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("future date fields = %#v, want %#v", fields, want)
	}
	assertStoredUnchanged(t, ctx, db, before)
}

func TestUpdateMalformedDateReturnsOnlyCanonicalReason(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	before := loadStoredTransaction(t, ctx, db, seeded.ID)
	want := []contract.FieldIssue{{Field: "date", Reason: "must be a valid YYYY-MM-DD date"}}

	for _, input := range []string{"", "2026-8-16", "2026-13-01", "9999-13-01", "2026/08/15", "2026-02-29"} {
		_, fields, err := store.Update(ctx, transaction.UpdateInput{ID: seeded.ID, Date: stringPtr(input)})
		if err != nil {
			t.Fatalf("Update(date %q) error = %v, want semantic issue", input, err)
		}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("Update(date %q) fields = %#v, want %#v", input, fields, want)
		}
	}
	assertStoredUnchanged(t, ctx, db, before)
}

func TestUpdateDoesNotTrimDatesOrAmounts(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	before := loadStoredTransaction(t, ctx, db, seeded.ID)

	for _, input := range []string{" 2026-08-15", "2026-08-15 "} {
		_, fields, err := store.Update(ctx, transaction.UpdateInput{ID: seeded.ID, Date: stringPtr(input)})
		if err != nil {
			t.Fatalf("Update(padded date %q) error = %v, want semantic issue", input, err)
		}
		want := []contract.FieldIssue{{Field: "date", Reason: "must be a valid YYYY-MM-DD date"}}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("Update(padded date %q) fields = %#v, want %#v", input, fields, want)
		}
	}
	for _, input := range []string{" 20.00", "20.00 "} {
		_, fields, err := store.Update(ctx, transaction.UpdateInput{ID: seeded.ID, Amount: stringPtr(input)})
		if err != nil {
			t.Fatalf("Update(padded amount %q) error = %v, want semantic issue", input, err)
		}
		want := []contract.FieldIssue{{Field: "amount", Reason: "must be a positive amount with at most two decimal places"}}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("Update(padded amount %q) fields = %#v, want %#v", input, fields, want)
		}
	}
	assertStoredUnchanged(t, ctx, db, before)
}

func TestUpdateSuppliedEmptyCategoryIsInvalidInput(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	before := loadStoredTransaction(t, ctx, db, seeded.ID)

	for _, categoryName := range []string{"", " \t"} {
		_, fields, err := store.Update(ctx, transaction.UpdateInput{ID: seeded.ID, Category: stringPtr(categoryName)})
		if err != nil {
			t.Fatalf("Update(empty category %q) error = %v, want semantic issue", categoryName, err)
		}
		want := []contract.FieldIssue{{Field: "category", Reason: "must not be empty"}}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("Update(empty category %q) fields = %#v, want %#v", categoryName, fields, want)
		}
		if errors.Is(err, transaction.ErrCategoryNotFound) {
			t.Fatal("empty supplied category returned category_not_found")
		}
	}
	assertStoredUnchanged(t, ctx, db, before)
}

func TestUpdateOmittedCategoryIsNotSemanticError(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)

	result := mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Amount: stringPtr("21.00")})
	if transactionCategory(result.Transaction) != "Groceries" {
		t.Fatalf("omitted category changed stored category to %q", transactionCategory(result.Transaction))
	}
}

func TestUpdateRejectsNULCategoryAndNote(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	before := loadStoredTransaction(t, ctx, db, seeded.ID)

	_, fields, err := store.Update(ctx, transaction.UpdateInput{
		ID:       seeded.ID,
		Category: stringPtr("Groceries\x00"),
		Note:     noteValue("weekly\x00"),
	})
	if err != nil {
		t.Fatalf("Update(NUL category/note) error = %v, want semantic issues", err)
	}
	want := []contract.FieldIssue{
		{Field: "category", Reason: "must not contain NUL characters"},
		{Field: "note", Reason: "must not contain NUL characters"},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("NUL fields = %#v, want %#v", fields, want)
	}
	assertStoredUnchanged(t, ctx, db, before)
}

func TestUpdateEmptyAndWhitespaceNotesBecomeNull(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)

	for _, note := range []string{"", " \t\n\r\v\f "} {
		result := mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Note: noteValue(note)})
		if result.Transaction.Note != nil {
			t.Fatalf("note %q stored as %#v, want nil", note, result.Transaction.Note)
		}
		if loadStoredTransaction(t, ctx, db, seeded.ID).Note.Valid {
			t.Fatalf("note %q left a stored string", note)
		}
	}

	trimmed := mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Note: noteValue("  corrected  ")})
	if trimmed.Transaction.Note == nil || *trimmed.Transaction.Note != "corrected" {
		t.Fatalf("trimmed note = %#v, want corrected", trimmed.Transaction.Note)
	}
}

func TestUpdateCollectsSemanticIssuesInFieldOrder(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	before := loadStoredTransaction(t, ctx, db, seeded.ID)

	_, fields, err := store.Update(ctx, transaction.UpdateInput{
		ID:       0,
		Amount:   stringPtr("-1"),
		Merchant: stringPtr(" \t"),
		Category: stringPtr(" "),
		Date:     stringPtr(" 2026-08-16"),
		Note:     noteValue("weekly\x00"),
	})
	if err != nil {
		t.Fatalf("Update(multi) error = %v, want semantic issues", err)
	}
	want := []contract.FieldIssue{
		{Field: "id", Reason: "must be a positive integer"},
		{Field: "amount", Reason: "must be a positive amount with at most two decimal places"},
		{Field: "merchant", Reason: "must not be empty"},
		{Field: "category", Reason: "must not be empty"},
		{Field: "date", Reason: "must be a valid YYYY-MM-DD date"},
		{Field: "note", Reason: "must not contain NUL characters"},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("multi fields = %#v, want %#v", fields, want)
	}
	assertStoredUnchanged(t, ctx, db, before)
}

func TestUpdateCapturesInjectedNowOnce(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	called := 0
	store.Now = func() time.Time {
		called++
		return torontoTime(t, 2026, 8, 15, 12, 0)
	}

	_, fields, err := store.Update(ctx, transaction.UpdateInput{
		ID:   seeded.ID,
		Date: stringPtr("2026-08-16"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v, want semantic issue", err)
	}
	if len(fields) != 1 || fields[0].Field != "date" {
		t.Fatalf("fields = %#v, want future date", fields)
	}
	if called != 1 {
		t.Fatalf("Now() calls = %d, want 1", called)
	}

	called = 0
	mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Amount: stringPtr("21.00")})
	if called != 1 {
		t.Fatalf("Now() calls on omitted-date update = %d, want 1", called)
	}
	if got := loadStoredTransaction(t, ctx, db, seeded.ID).Date; got != "2026-08-01" {
		t.Fatalf("omitted-date update rewrote date to %q", got)
	}
}

func TestUpdateDoesNotFoldUnicodeBeyondSQLiteNoCase(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := addTransaction(t, ctx, store, transaction.AddInput{
		Amount:   "1.00",
		Merchant: "Café",
		Category: stringPtr("Groceries"),
	})
	beforeMapping := loadStoredMapping(t, ctx, db, "Café")

	result := mustUpdate(t, ctx, store, transaction.UpdateInput{
		ID:       seeded.ID,
		Merchant: stringPtr("CAFÉ"),
		Category: stringPtr("GROCERIES"),
	})
	if result.Transaction.Merchant != "CAFÉ" {
		t.Fatalf("updated merchant = %q, want CAFÉ", result.Transaction.Merchant)
	}
	if transactionCategory(result.Transaction) != "Groceries" {
		t.Fatalf("ASCII-folded category = %q, want stored Groceries", transactionCategory(result.Transaction))
	}
	if got := countMappings(t, ctx, db); got != 1 {
		t.Fatalf("mapping count = %d, want 1", got)
	}
	after := loadStoredMapping(t, ctx, db, "Café")
	if after != beforeMapping {
		t.Fatalf("Café mapping changed: %#v vs %#v", after, beforeMapping)
	}
}

func TestUpdateDoesNotQueryWhenSemanticFieldsAreNonEmpty(t *testing.T) {
	ctx := context.Background()
	store, _, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.Update(ctx, transaction.UpdateInput{
		ID:     42,
		Amount: stringPtr("-1"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v, want semantic issue", err)
	}
	want := []contract.FieldIssue{{Field: "amount", Reason: "must be a positive amount with at most two decimal places"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("fields = %#v, want %#v", fields, want)
	}
	var notFound *transaction.TransactionNotFoundError
	if errors.As(err, &notFound) {
		t.Fatal("semantic failure queried the database")
	}
}
