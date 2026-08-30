package transaction_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/jordanp2002/local-finance-mcp/internal/transaction"
)

const frozenTimestamp = "2020-01-01T00:00:00.000Z"

func TestUpdatePatchesEachFieldIndependently(t *testing.T) {
	ctx := context.Background()
	now := torontoTime(t, 2026, 8, 15, 12, 0)

	t.Run("amount", func(t *testing.T) {
		store, categories, _, db := openTransactionStore(t, now)
		createCategory(t, ctx, categories, "Groceries")
		seeded := seedGroceryTransaction(t, ctx, store)
		result := mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Amount: stringPtr("23.50")})
		if result.Transaction.Amount != "23.50" {
			t.Fatalf("amount = %q, want 23.50", result.Transaction.Amount)
		}
		assertUnpatchedFields(t, result.Transaction, seeded, "amount")
		stored := loadStoredTransaction(t, ctx, db, seeded.ID)
		if stored.AmountHundredths != 2350 {
			t.Fatalf("stored hundredths = %d, want 2350", stored.AmountHundredths)
		}
	})

	t.Run("merchant", func(t *testing.T) {
		store, categories, _, _ := openTransactionStore(t, now)
		createCategory(t, ctx, categories, "Groceries")
		seeded := seedGroceryTransaction(t, ctx, store)
		result := mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Merchant: stringPtr("Metro grocery store")})
		if result.Transaction.Merchant != "Metro grocery store" {
			t.Fatalf("merchant = %q, want Metro grocery store", result.Transaction.Merchant)
		}
		assertUnpatchedFields(t, result.Transaction, seeded, "merchant")
	})

	t.Run("category", func(t *testing.T) {
		store, categories, _, _ := openTransactionStore(t, now)
		createCategory(t, ctx, categories, "Groceries")
		health := createCategory(t, ctx, categories, "Health")
		seeded := seedGroceryTransaction(t, ctx, store)
		result := mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Category: stringPtr("Health")})
		if transactionCategoryID(result.Transaction) != health.ID || transactionCategory(result.Transaction) != "Health" {
			t.Fatalf("category = %#v, want Health", result.Transaction)
		}
		assertUnpatchedFields(t, result.Transaction, seeded, "category")
	})

	t.Run("date", func(t *testing.T) {
		store, categories, _, _ := openTransactionStore(t, now)
		createCategory(t, ctx, categories, "Groceries")
		seeded := seedGroceryTransaction(t, ctx, store)
		result := mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Date: stringPtr("2026-08-13")})
		if result.Transaction.Date != "2026-08-13" {
			t.Fatalf("date = %q, want 2026-08-13", result.Transaction.Date)
		}
		assertUnpatchedFields(t, result.Transaction, seeded, "date")
	})

	t.Run("note", func(t *testing.T) {
		store, categories, _, _ := openTransactionStore(t, now)
		createCategory(t, ctx, categories, "Groceries")
		seeded := seedGroceryTransaction(t, ctx, store)
		result := mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Note: noteValue("  corrected  ")})
		if result.Transaction.Note == nil || *result.Transaction.Note != "corrected" {
			t.Fatalf("note = %#v, want corrected", result.Transaction.Note)
		}
		assertUnpatchedFields(t, result.Transaction, seeded, "note")
	})
}

func TestUpdateAmountOnlyPreservesOtherColumns(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	freezeTransactionTimestamps(t, ctx, db, seeded.ID, frozenTimestamp)
	before := loadStoredTransaction(t, ctx, db, seeded.ID)

	result := mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Amount: stringPtr("23.50")})
	if result.Transaction.ID != seeded.ID {
		t.Fatalf("id = %d, want %d", result.Transaction.ID, seeded.ID)
	}
	if result.Transaction.CreatedAt != frozenTimestamp {
		t.Fatalf("created_at = %q, want frozen %s", result.Transaction.CreatedAt, frozenTimestamp)
	}
	if result.Transaction.UpdatedAt == frozenTimestamp || result.Transaction.UpdatedAt == "" {
		t.Fatalf("updated_at = %q, want advanced", result.Transaction.UpdatedAt)
	}
	if result.Transaction.Merchant != "Metro" || result.Transaction.Date != "2026-08-01" || transactionCategory(result.Transaction) != "Groceries" {
		t.Fatalf("unpatched columns changed: %#v", result.Transaction)
	}
	if result.Transaction.Note == nil || *result.Transaction.Note != "weekly" {
		t.Fatalf("note = %#v, want weekly", result.Transaction.Note)
	}
	stored := loadStoredTransaction(t, ctx, db, seeded.ID)
	if stored.Merchant != before.Merchant || stored.Date != before.Date || stored.CategoryID != before.CategoryID || stored.Note != before.Note {
		t.Fatalf("stored unpatched columns changed: %#v vs %#v", stored, before)
	}
	if stored.CreatedAt != frozenTimestamp {
		t.Fatalf("stored created_at = %q, want frozen", stored.CreatedAt)
	}
	if stored.UpdatedAt == frozenTimestamp {
		t.Fatal("stored updated_at was not advanced")
	}
}

func TestUpdateMerchantOnlyKeepsCategoryIncludingInactive(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	health := createCategory(t, ctx, categories, "Health")
	createCategory(t, ctx, categories, "Groceries")
	seeded := addTransaction(t, ctx, store, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Shoppers",
		Category: stringPtr("Health"),
		Date:     stringPtr("2026-08-01"),
		Note:     stringPtr("rx"),
	})
	if _, changed, _, err := categories.Disable(ctx, "Health"); err != nil || !changed {
		t.Fatalf("Disable(Health) = changed %v, error %v", changed, err)
	}

	result := mustUpdate(t, ctx, store, transaction.UpdateInput{
		ID:       seeded.ID,
		Merchant: stringPtr("  Shoppers Drug Mart  "),
	})
	if result.Transaction.Merchant != "Shoppers Drug Mart" {
		t.Fatalf("merchant = %q, want trimmed Shoppers Drug Mart", result.Transaction.Merchant)
	}
	if transactionCategoryID(result.Transaction) != health.ID || transactionCategory(result.Transaction) != "Health" {
		t.Fatalf("category = %#v, want inactive Health retained", result.Transaction)
	}
	if loadStoredTransaction(t, ctx, db, seeded.ID).CategoryID != health.ID {
		t.Fatalf("stored category_id = %d, want %d", loadStoredTransaction(t, ctx, db, seeded.ID).CategoryID, health.ID)
	}
}

func TestUpdateCategoryOnlyChangesThisRow(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	health := createCategory(t, ctx, categories, "Health")
	first := seedGroceryTransaction(t, ctx, store)
	second := addTransaction(t, ctx, store, transaction.AddInput{
		Amount:   "5.00",
		Merchant: "Other Metro",
		Category: stringPtr("Groceries"),
		Date:     stringPtr("2026-08-02"),
	})
	freezeTransactionTimestamps(t, ctx, db, second.ID, frozenTimestamp)
	beforeSecond := loadStoredTransaction(t, ctx, db, second.ID)

	result := mustUpdate(t, ctx, store, transaction.UpdateInput{ID: first.ID, Category: stringPtr(" health ")})
	if transactionCategoryID(result.Transaction) != health.ID || transactionCategory(result.Transaction) != "Health" {
		t.Fatalf("updated row = %#v, want Health", result.Transaction)
	}
	if result.Transaction.Amount != "20.00" || result.Transaction.Merchant != "Metro" {
		t.Fatalf("category-only update changed other fields: %#v", result.Transaction)
	}
	if loadStoredTransaction(t, ctx, db, first.ID).CategoryID != health.ID {
		t.Fatal("target category_id was not updated")
	}
	assertStoredUnchanged(t, ctx, db, beforeSecond)
	if loadStoredTransaction(t, ctx, db, second.ID).CategoryID != groceries.ID {
		t.Fatal("sibling row category changed")
	}
}

func TestUpdateDateOnlyToPastAndCurrent(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)

	past := mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Date: stringPtr("2026-08-13")})
	if past.Transaction.Date != "2026-08-13" {
		t.Fatalf("past date = %q, want 2026-08-13", past.Transaction.Date)
	}
	assertUnpatchedFields(t, past.Transaction, seeded, "date")

	current := mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Date: stringPtr("2026-08-15")})
	if current.Transaction.Date != "2026-08-15" {
		t.Fatalf("current date = %q, want 2026-08-15", current.Transaction.Date)
	}
}

func TestUpdateNoteOnlyStoresTrimmedString(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)

	result := mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Note: noteValue("  birthday cake  ")})
	if result.Transaction.Note == nil || *result.Transaction.Note != "birthday cake" {
		t.Fatalf("note = %#v, want birthday cake", result.Transaction.Note)
	}
	assertUnpatchedFields(t, result.Transaction, seeded, "note")
	stored := loadStoredTransaction(t, ctx, db, seeded.ID)
	if !stored.Note.Valid || stored.Note.String != "birthday cake" {
		t.Fatalf("stored note = %#v, want birthday cake", stored.Note)
	}
}

func TestUpdateNotePatchClearSetsSQLNull(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)

	result := mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Note: noteClear()})
	if result.Transaction.Note != nil {
		t.Fatalf("cleared note = %#v, want nil", result.Transaction.Note)
	}
	if loadStoredTransaction(t, ctx, db, seeded.ID).Note.Valid {
		t.Fatalf("stored note = %#v, want SQL NULL", loadStoredTransaction(t, ctx, db, seeded.ID).Note)
	}
	assertUnpatchedFields(t, result.Transaction, seeded, "note")
}

func TestUpdateSameValueStillAdvancesUpdatedAt(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	freezeTransactionTimestamps(t, ctx, db, seeded.ID, frozenTimestamp)

	result := mustUpdate(t, ctx, store, transaction.UpdateInput{
		ID:       seeded.ID,
		Amount:   stringPtr("20.00"),
		Merchant: stringPtr("Metro"),
		Category: stringPtr("Groceries"),
		Date:     stringPtr("2026-08-01"),
		Note:     noteValue("weekly"),
	})
	if result.Transaction.Amount != "20.00" || result.Transaction.Merchant != "Metro" || transactionCategory(result.Transaction) != "Groceries" {
		t.Fatalf("same-value update changed columns: %#v", result.Transaction)
	}
	if result.Transaction.CreatedAt != frozenTimestamp {
		t.Fatalf("created_at = %q, want frozen", result.Transaction.CreatedAt)
	}
	if result.Transaction.UpdatedAt == frozenTimestamp {
		t.Fatal("same-value update left updated_at unchanged")
	}
	stored := loadStoredTransaction(t, ctx, db, seeded.ID)
	if stored.UpdatedAt == frozenTimestamp || stored.CreatedAt != frozenTimestamp {
		t.Fatalf("stored timestamps = created %q updated %q", stored.CreatedAt, stored.UpdatedAt)
	}
}

func TestUpdateMissingIDIsTransactionNotFound(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	before := loadStoredTransaction(t, ctx, db, seeded.ID)
	beforeMappings := listStoredMappings(t, ctx, db)

	_, fields, err := store.Update(ctx, transaction.UpdateInput{ID: 42, Amount: stringPtr("23.50")})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	var notFound *transaction.TransactionNotFoundError
	if !errors.As(err, &notFound) || !errors.Is(err, transaction.ErrTransactionNotFound) {
		t.Fatalf("error = %v, want TransactionNotFoundError", err)
	}
	if notFound.ID != 42 {
		t.Fatalf("not-found id = %d, want 42", notFound.ID)
	}
	assertStoredUnchanged(t, ctx, db, before)
	if !reflect.DeepEqual(listStoredMappings(t, ctx, db), beforeMappings) {
		t.Fatal("mappings changed after missing-id update")
	}
}

func TestUpdateMissingCategoryIsNotFound(t *testing.T) {
	ctx := context.Background()
	now := torontoTime(t, 2026, 8, 15, 12, 0)

	t.Run("empty active list", func(t *testing.T) {
		store, categories, _, db := openTransactionStore(t, now)
		createCategory(t, ctx, categories, "Groceries")
		seeded := seedGroceryTransaction(t, ctx, store)
		if _, changed, _, err := categories.Disable(ctx, "Groceries"); err != nil || !changed {
			t.Fatalf("Disable(Groceries) = changed %v, error %v", changed, err)
		}
		before := loadStoredTransaction(t, ctx, db, seeded.ID)

		_, fields, err := store.Update(ctx, transaction.UpdateInput{ID: seeded.ID, Category: stringPtr(" Pharmacy ")})
		if len(fields) != 0 {
			t.Fatalf("fields = %#v, want none", fields)
		}
		var missing *transaction.CategoryNotFoundError
		if !errors.As(err, &missing) || !errors.Is(err, transaction.ErrCategoryNotFound) {
			t.Fatalf("error = %v, want CategoryNotFoundError", err)
		}
		if missing.Requested != "Pharmacy" || missing.ActiveCategories == nil || len(missing.ActiveCategories) != 0 {
			t.Fatalf("missing recovery = %#v, want trimmed name and empty non-nil list", missing)
		}
		assertStoredUnchanged(t, ctx, db, before)
	})

	t.Run("active recovery list", func(t *testing.T) {
		store, categories, _, db := openTransactionStore(t, now)
		alpha := createCategory(t, ctx, categories, "Alpha")
		beta := createCategory(t, ctx, categories, "beta")
		createCategory(t, ctx, categories, "Groceries")
		seeded := seedGroceryTransaction(t, ctx, store)
		before := loadStoredTransaction(t, ctx, db, seeded.ID)

		_, fields, err := store.Update(ctx, transaction.UpdateInput{ID: seeded.ID, Category: stringPtr("Pharmacy")})
		if len(fields) != 0 {
			t.Fatalf("fields = %#v, want none", fields)
		}
		var missing *transaction.CategoryNotFoundError
		if !errors.As(err, &missing) {
			t.Fatalf("error = %v, want CategoryNotFoundError", err)
		}
		if missing.ActiveCategories == nil || len(missing.ActiveCategories) != 3 {
			t.Fatalf("active recovery = %#v, want Alpha, beta, Groceries", missing.ActiveCategories)
		}
		if missing.ActiveCategories[0].ID != alpha.ID || missing.ActiveCategories[1].ID != beta.ID {
			t.Fatalf("active recovery order = %#v", missing.ActiveCategories)
		}
		assertStoredUnchanged(t, ctx, db, before)
	})
}

func TestUpdateInactiveCategoryReturnsCanonicalRow(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	alpha := createCategory(t, ctx, categories, "Alpha")
	dining := createCategory(t, ctx, categories, "Dining")
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	if _, changed, _, err := categories.Disable(ctx, "Dining"); err != nil || !changed {
		t.Fatalf("Disable(Dining) = changed %v, error %v", changed, err)
	}
	before := loadStoredTransaction(t, ctx, db, seeded.ID)

	_, fields, err := store.Update(ctx, transaction.UpdateInput{ID: seeded.ID, Category: stringPtr("dining")})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	var inactive *transaction.CategoryInactiveError
	if !errors.As(err, &inactive) || !errors.Is(err, transaction.ErrCategoryInactive) {
		t.Fatalf("error = %v, want CategoryInactiveError", err)
	}
	if inactive.Category.ID != dining.ID || inactive.Category.Name != "Dining" || inactive.Category.Active {
		t.Fatalf("inactive category = %#v, want canonical Dining", inactive.Category)
	}
	if inactive.ActiveCategories == nil || len(inactive.ActiveCategories) != 2 {
		t.Fatalf("active recovery = %#v, want Alpha and Groceries", inactive.ActiveCategories)
	}
	if inactive.ActiveCategories[0].ID != alpha.ID {
		t.Fatalf("active recovery order = %#v, want Alpha first", inactive.ActiveCategories)
	}
	assertStoredUnchanged(t, ctx, db, before)
}

func TestUpdateOwnInactiveCategoryIsInactive(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	health := createCategory(t, ctx, categories, "Health")
	createCategory(t, ctx, categories, "Groceries")
	seeded := addTransaction(t, ctx, store, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Shoppers",
		Category: stringPtr("Health"),
	})
	if _, changed, _, err := categories.Disable(ctx, "Health"); err != nil || !changed {
		t.Fatalf("Disable(Health) = changed %v, error %v", changed, err)
	}
	before := loadStoredTransaction(t, ctx, db, seeded.ID)

	_, fields, err := store.Update(ctx, transaction.UpdateInput{ID: seeded.ID, Category: stringPtr("Health")})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	var inactive *transaction.CategoryInactiveError
	if !errors.As(err, &inactive) || !errors.Is(err, transaction.ErrCategoryInactive) {
		t.Fatalf("error = %v, want CategoryInactiveError", err)
	}
	if inactive.Category.ID != health.ID || inactive.Category.Active {
		t.Fatalf("inactive category = %#v, want own Health", inactive.Category)
	}
	assertStoredUnchanged(t, ctx, db, before)
}

func TestUpdateNonCategoryPatchOnInactiveCategorySucceeds(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	health := createCategory(t, ctx, categories, "Health")
	createCategory(t, ctx, categories, "Groceries")
	seeded := addTransaction(t, ctx, store, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Shoppers",
		Category: stringPtr("Health"),
		Note:     stringPtr("rx"),
	})
	if _, changed, _, err := categories.Disable(ctx, "Health"); err != nil || !changed {
		t.Fatalf("Disable(Health) = changed %v, error %v", changed, err)
	}

	amount := mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Amount: stringPtr("21.00")})
	if amount.Transaction.Amount != "21.00" || transactionCategoryID(amount.Transaction) != health.ID || transactionCategory(amount.Transaction) != "Health" {
		t.Fatalf("amount patch = %#v, want Health retained", amount.Transaction)
	}

	merchant := mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Merchant: stringPtr("Shoppers Drug Mart")})
	if merchant.Transaction.Merchant != "Shoppers Drug Mart" || transactionCategoryID(merchant.Transaction) != health.ID {
		t.Fatalf("merchant patch = %#v, want Health retained", merchant.Transaction)
	}
	if loadStoredTransaction(t, ctx, db, seeded.ID).CategoryID != health.ID {
		t.Fatal("non-category patch rewrote category_id")
	}
}

func TestUpdateCategoryErrorsHappenBeforeWrite(t *testing.T) {
	ctx := context.Background()
	now := torontoTime(t, 2026, 8, 15, 12, 0)

	t.Run("missing category with no mapping for new merchant", func(t *testing.T) {
		store, categories, _, db := openTransactionStore(t, now)
		createCategory(t, ctx, categories, "Groceries")
		seeded := seedGroceryTransaction(t, ctx, store)
		before := loadStoredTransaction(t, ctx, db, seeded.ID)
		beforeMappings := listStoredMappings(t, ctx, db)

		_, _, err := store.Update(ctx, transaction.UpdateInput{
			ID:       seeded.ID,
			Merchant: stringPtr("Metro grocery store"),
			Category: stringPtr("Pharmacy"),
		})
		var missing *transaction.CategoryNotFoundError
		if !errors.As(err, &missing) {
			t.Fatalf("error = %v, want CategoryNotFoundError", err)
		}
		assertStoredUnchanged(t, ctx, db, before)
		if !reflect.DeepEqual(listStoredMappings(t, ctx, db), beforeMappings) {
			t.Fatal("missing category created or rewrote a mapping")
		}
	})

	t.Run("missing category with inactive mapping", func(t *testing.T) {
		store, categories, merchants, db := openTransactionStore(t, now)
		createCategory(t, ctx, categories, "Health")
		createCategory(t, ctx, categories, "Groceries")
		existing := setMerchant(t, ctx, merchants, "Shoppers", "Health")
		seeded := seedGroceryTransaction(t, ctx, store)
		if _, changed, _, err := categories.Disable(ctx, "Health"); err != nil || !changed {
			t.Fatalf("Disable(Health) = changed %v, error %v", changed, err)
		}
		freezeMappingTimestamps(t, ctx, db, existing.ID, frozenTimestamp)
		before := loadStoredTransaction(t, ctx, db, seeded.ID)

		_, _, err := store.Update(ctx, transaction.UpdateInput{
			ID:       seeded.ID,
			Merchant: stringPtr("Shoppers"),
			Category: stringPtr("Pharmacy"),
		})
		var missing *transaction.CategoryNotFoundError
		if !errors.As(err, &missing) {
			t.Fatalf("error = %v, want CategoryNotFoundError before write", err)
		}
		if errors.Is(err, transaction.ErrMerchantCategoryInactive) {
			t.Fatal("update inspected merchant mapping")
		}
		assertStoredUnchanged(t, ctx, db, before)
		after := loadStoredMapping(t, ctx, db, "Shoppers")
		if after.UpdatedAt != frozenTimestamp || after.CategoryID != existing.CategoryID {
			t.Fatalf("inactive mapping changed: %#v", after)
		}
	})

	t.Run("inactive supplied category with no mapping", func(t *testing.T) {
		store, categories, _, db := openTransactionStore(t, now)
		createCategory(t, ctx, categories, "Dining")
		createCategory(t, ctx, categories, "Groceries")
		seeded := seedGroceryTransaction(t, ctx, store)
		if _, changed, _, err := categories.Disable(ctx, "Dining"); err != nil || !changed {
			t.Fatalf("Disable(Dining) = changed %v, error %v", changed, err)
		}
		before := loadStoredTransaction(t, ctx, db, seeded.ID)
		beforeMappings := listStoredMappings(t, ctx, db)

		_, _, err := store.Update(ctx, transaction.UpdateInput{
			ID:       seeded.ID,
			Merchant: stringPtr("New Cafe"),
			Category: stringPtr("Dining"),
		})
		var inactive *transaction.CategoryInactiveError
		if !errors.As(err, &inactive) {
			t.Fatalf("error = %v, want CategoryInactiveError", err)
		}
		assertStoredUnchanged(t, ctx, db, before)
		if !reflect.DeepEqual(listStoredMappings(t, ctx, db), beforeMappings) {
			t.Fatal("inactive category write created a mapping")
		}
	})
}

func TestUpdateNewMerchantSpellingDoesNotCreateMapping(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	metro := loadStoredMapping(t, ctx, db, "Metro")
	freezeMappingTimestamps(t, ctx, db, metro.ID, frozenTimestamp)
	before := loadStoredMapping(t, ctx, db, "Metro")

	result := mustUpdate(t, ctx, store, transaction.UpdateInput{
		ID:       seeded.ID,
		Merchant: stringPtr("Metro grocery store"),
	})
	if result.Transaction.Merchant != "Metro grocery store" || transactionCategory(result.Transaction) != "Groceries" {
		t.Fatalf("updated transaction = %#v", result.Transaction)
	}
	if got := countMappings(t, ctx, db); got != 1 {
		t.Fatalf("mapping count = %d, want 1", got)
	}
	after := loadStoredMapping(t, ctx, db, "Metro")
	if after != before {
		t.Fatalf("Metro mapping changed: %#v vs %#v", after, before)
	}
}

func TestUpdateDoesNotApplyDifferentMerchantMapping(t *testing.T) {
	ctx := context.Background()
	store, categories, merchants, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	health := createCategory(t, ctx, categories, "Health")
	seeded := seedGroceryTransaction(t, ctx, store)
	shoppers := setMerchant(t, ctx, merchants, "Shoppers", "Health")
	freezeMappingTimestamps(t, ctx, db, shoppers.ID, frozenTimestamp)
	metro := loadStoredMapping(t, ctx, db, "Metro")
	freezeMappingTimestamps(t, ctx, db, metro.ID, frozenTimestamp)
	beforeShoppers := loadStoredMapping(t, ctx, db, "Shoppers")
	beforeMetro := loadStoredMapping(t, ctx, db, "Metro")

	result := mustUpdate(t, ctx, store, transaction.UpdateInput{
		ID:       seeded.ID,
		Merchant: stringPtr("Shoppers"),
	})
	if result.Transaction.Merchant != "Shoppers" {
		t.Fatalf("merchant = %q, want Shoppers", result.Transaction.Merchant)
	}
	if transactionCategoryID(result.Transaction) != groceries.ID || transactionCategory(result.Transaction) != "Groceries" {
		t.Fatalf("category = %#v, want existing Groceries not mapped Health", result.Transaction)
	}
	if transactionCategoryID(result.Transaction) == health.ID {
		t.Fatal("applied Shoppers -> Health mapping")
	}
	if loadStoredMapping(t, ctx, db, "Shoppers") != beforeShoppers {
		t.Fatalf("Shoppers mapping changed: %#v", loadStoredMapping(t, ctx, db, "Shoppers"))
	}
	if loadStoredMapping(t, ctx, db, "Metro") != beforeMetro {
		t.Fatalf("Metro mapping changed: %#v", loadStoredMapping(t, ctx, db, "Metro"))
	}
}

func TestUpdateMerchantAndCategoryWritesNoMapping(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	health := createCategory(t, ctx, categories, "Health")
	seeded := seedGroceryTransaction(t, ctx, store)
	metro := loadStoredMapping(t, ctx, db, "Metro")
	freezeMappingTimestamps(t, ctx, db, metro.ID, frozenTimestamp)
	beforeMappings := listStoredMappings(t, ctx, db)

	result := mustUpdate(t, ctx, store, transaction.UpdateInput{
		ID:       seeded.ID,
		Merchant: stringPtr("New Store"),
		Category: stringPtr("Health"),
	})
	if result.Transaction.Merchant != "New Store" || transactionCategoryID(result.Transaction) != health.ID {
		t.Fatalf("updated transaction = %#v", result.Transaction)
	}
	if !reflect.DeepEqual(listStoredMappings(t, ctx, db), beforeMappings) {
		t.Fatalf("mappings changed: %#v vs %#v", listStoredMappings(t, ctx, db), beforeMappings)
	}
	if got := countMappings(t, ctx, db); got != 1 {
		t.Fatalf("mapping count = %d, want 1", got)
	}
}

func TestUpdateLeavesMappingsAndBudgetsUnchanged(t *testing.T) {
	ctx := context.Background()
	now := torontoTime(t, 2026, 8, 15, 12, 0)

	t.Run("success with current-month snapshot", func(t *testing.T) {
		store, categories, _, db := openTransactionStore(t, now)
		groceries := createCategory(t, ctx, categories, "Groceries")
		insertBudget(t, ctx, db, groceries.ID, "2026-08", "500.00")
		seeded := seedGroceryTransaction(t, ctx, store)
		freezeBudgetTimestamps(t, ctx, db, frozenTimestamp)
		metro := loadStoredMapping(t, ctx, db, "Metro")
		freezeMappingTimestamps(t, ctx, db, metro.ID, frozenTimestamp)
		beforeBudgets := listStoredBudgets(t, ctx, db)
		beforeMappings := listStoredMappings(t, ctx, db)

		mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Amount: stringPtr("23.50")})
		if !reflect.DeepEqual(listStoredBudgets(t, ctx, db), beforeBudgets) {
			t.Fatalf("budgets changed from %#v", beforeBudgets)
		}
		if !reflect.DeepEqual(listStoredMappings(t, ctx, db), beforeMappings) {
			t.Fatalf("mappings changed from %#v", beforeMappings)
		}
	})

	t.Run("success without snapshot", func(t *testing.T) {
		store, categories, _, db := openTransactionStore(t, now)
		createCategory(t, ctx, categories, "Groceries")
		seeded := addTransaction(t, ctx, store, transaction.AddInput{
			Amount:   "20.00",
			Merchant: "Metro",
			Category: stringPtr("Groceries"),
			Date:     stringPtr("2026-07-31"),
		})
		metro := loadStoredMapping(t, ctx, db, "Metro")
		freezeMappingTimestamps(t, ctx, db, metro.ID, frozenTimestamp)
		beforeMappings := listStoredMappings(t, ctx, db)

		mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Amount: stringPtr("21.00")})
		if got := countBudgets(t, ctx, db); got != 0 {
			t.Fatalf("budget rows = %d, want 0", got)
		}
		if !reflect.DeepEqual(listStoredMappings(t, ctx, db), beforeMappings) {
			t.Fatal("mappings changed when no budget snapshot existed")
		}
	})

	t.Run("failed category and missing id", func(t *testing.T) {
		store, categories, _, db := openTransactionStore(t, now)
		groceries := createCategory(t, ctx, categories, "Groceries")
		insertBudget(t, ctx, db, groceries.ID, "2026-08", "500.00")
		seeded := seedGroceryTransaction(t, ctx, store)
		freezeBudgetTimestamps(t, ctx, db, frozenTimestamp)
		metro := loadStoredMapping(t, ctx, db, "Metro")
		freezeMappingTimestamps(t, ctx, db, metro.ID, frozenTimestamp)
		beforeBudgets := listStoredBudgets(t, ctx, db)
		beforeMappings := listStoredMappings(t, ctx, db)

		if _, _, err := store.Update(ctx, transaction.UpdateInput{ID: seeded.ID, Category: stringPtr("Pharmacy")}); !errors.Is(err, transaction.ErrCategoryNotFound) {
			t.Fatalf("missing category error = %v", err)
		}
		if _, _, err := store.Update(ctx, transaction.UpdateInput{ID: 99, Amount: stringPtr("1.00")}); !errors.Is(err, transaction.ErrTransactionNotFound) {
			t.Fatalf("missing id error = %v", err)
		}
		if !reflect.DeepEqual(listStoredBudgets(t, ctx, db), beforeBudgets) {
			t.Fatal("budgets changed after failed updates")
		}
		if !reflect.DeepEqual(listStoredMappings(t, ctx, db), beforeMappings) {
			t.Fatal("mappings changed after failed updates")
		}
	})
}

func TestUpdateLeavesOtherTransactionsUnchanged(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	first := seedGroceryTransaction(t, ctx, store)
	second := addTransaction(t, ctx, store, transaction.AddInput{
		Amount:   "5.00",
		Merchant: "Old Metro",
		Category: stringPtr("Groceries"),
		Date:     stringPtr("2026-07-01"),
		Note:     stringPtr("historical"),
	})
	freezeTransactionTimestamps(t, ctx, db, second.ID, frozenTimestamp)
	beforeSecond := loadStoredTransaction(t, ctx, db, second.ID)

	mustUpdate(t, ctx, store, transaction.UpdateInput{
		ID:       first.ID,
		Amount:   stringPtr("23.50"),
		Merchant: stringPtr("Metro grocery store"),
		Date:     stringPtr("2026-08-13"),
		Note:     noteValue("corrected"),
	})
	assertStoredUnchanged(t, ctx, db, beforeSecond)
}

func TestUpdateReturnsCanonicalJoinedTransaction(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)

	result := mustUpdate(t, ctx, store, transaction.UpdateInput{
		ID:       seeded.ID,
		Amount:   stringPtr("1"),
		Category: stringPtr("groceries"),
		Note:     noteClear(),
	})
	if result.Transaction.ID != seeded.ID {
		t.Fatalf("id = %d, want %d", result.Transaction.ID, seeded.ID)
	}
	if result.Transaction.Amount != "1.00" || transactionCategory(result.Transaction) != "Groceries" || transactionCategoryID(result.Transaction) != groceries.ID {
		t.Fatalf("canonical transaction = %#v", result.Transaction)
	}
	if result.Transaction.Note != nil {
		t.Fatalf("canonical note = %#v, want nil", result.Transaction.Note)
	}
	if result.Transaction.CreatedAt == "" || result.Transaction.UpdatedAt == "" {
		t.Fatalf("canonical timestamps missing: %#v", result.Transaction)
	}
	stored := loadStoredTransaction(t, ctx, db, seeded.ID)
	if stored.AmountHundredths != 100 || stored.CategoryID != groceries.ID {
		t.Fatalf("stored row = %#v", stored)
	}
	if stored.Note.Valid {
		t.Fatalf("stored note = %#v, want SQL NULL", stored.Note)
	}
}

func TestUpdateRollsBackAfterUpdateFailure(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	insertBudget(t, ctx, db, groceries.ID, "2026-08", "500.00")
	seeded := seedGroceryTransaction(t, ctx, store)
	freezeTransactionTimestamps(t, ctx, db, seeded.ID, frozenTimestamp)
	freezeBudgetTimestamps(t, ctx, db, frozenTimestamp)
	metro := loadStoredMapping(t, ctx, db, "Metro")
	freezeMappingTimestamps(t, ctx, db, metro.ID, frozenTimestamp)
	before := loadStoredTransaction(t, ctx, db, seeded.ID)
	beforeMappings := listStoredMappings(t, ctx, db)
	beforeBudgets := listStoredBudgets(t, ctx, db)

	if _, err := db.ExecContext(ctx, `
		CREATE TRIGGER fail_after_transaction_update
		AFTER UPDATE ON transactions
		BEGIN
			SELECT RAISE(ABORT, 'test transaction update failure');
		END
	`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	_, fields, err := store.Update(ctx, transaction.UpdateInput{
		ID:       seeded.ID,
		Amount:   stringPtr("23.50"),
		Merchant: stringPtr("Metro grocery store"),
		Note:     noteClear(),
	})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	if err == nil {
		t.Fatal("Update() error = nil, want trigger failure")
	}
	assertStoredUnchanged(t, ctx, db, before)
	if !reflect.DeepEqual(listStoredMappings(t, ctx, db), beforeMappings) {
		t.Fatal("mappings changed after rolled-back update")
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db), beforeBudgets) {
		t.Fatal("budgets changed after rolled-back update")
	}
}

func TestUpdateLeavesSchemaConstraintsEnforced(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	mustUpdate(t, ctx, store, transaction.UpdateInput{ID: seeded.ID, Amount: stringPtr("21.00")})

	expectExecError(t, ctx, db, `
		UPDATE transactions SET category_id = ? WHERE id = ?
	`, int64(999999), seeded.ID)
	expectExecError(t, ctx, db, `
		UPDATE transactions SET merchant = ? WHERE id = ?
	`, " Metro ", seeded.ID)
	expectExecError(t, ctx, db, `
		UPDATE transactions SET amount_hundredths = ? WHERE id = ?
	`, int64(0), seeded.ID)
	expectExecError(t, ctx, db, `
		UPDATE transactions SET date = ? WHERE id = ?
	`, "2026-8-14", seeded.ID)

	stored := loadStoredTransaction(t, ctx, db, seeded.ID)
	if stored.AmountHundredths != 2100 || stored.Merchant != "Metro" || stored.CategoryID != groceries.ID {
		t.Fatalf("constraint probes mutated the row: %#v", stored)
	}
	if got := countMappings(t, ctx, db); got != 1 {
		t.Fatalf("mapping count after constraint probes = %d, want 1", got)
	}
}

func assertUnpatchedFields(t *testing.T, got, original contract.Transaction, patched string) {
	t.Helper()
	if patched != "amount" && got.Amount != original.Amount {
		t.Fatalf("amount changed to %q, want %q", got.Amount, original.Amount)
	}
	if patched != "merchant" && got.Merchant != original.Merchant {
		t.Fatalf("merchant changed to %q, want %q", got.Merchant, original.Merchant)
	}
	if patched != "category" && (transactionCategoryID(got) != transactionCategoryID(original) || transactionCategory(got) != transactionCategory(original)) {
		t.Fatalf("category changed to %d/%q, want %d/%q", transactionCategoryID(got), transactionCategory(got), transactionCategoryID(original), transactionCategory(original))
	}
	if patched != "date" && got.Date != original.Date {
		t.Fatalf("date changed to %q, want %q", got.Date, original.Date)
	}
	if patched != "note" && !sameOptionalString(got.Note, original.Note) {
		t.Fatalf("note changed to %#v, want %#v", got.Note, original.Note)
	}
	if got.ID != original.ID {
		t.Fatalf("id changed to %d, want %d", got.ID, original.ID)
	}
}

func sameOptionalString(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
