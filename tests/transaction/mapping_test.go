package transaction_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/transaction"
)

func TestAddCreatedInsertsTransactionAndMappingWithSubmittedSpelling(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")

	result, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "20.5",
		Merchant: "  Metro  ",
		Category: stringPtr(" groceries "),
		Note:     stringPtr(" weekly "),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add(created) = %#v fields %#v error %v", result, fields, err)
	}
	if result.CategorySource != transaction.CategorySourceProvided || result.MerchantMappingAction != transaction.MappingActionCreated {
		t.Fatalf("created flags = (%q, %q)", result.CategorySource, result.MerchantMappingAction)
	}
	if result.Transaction.ID <= 0 || result.Transaction.Merchant != "Metro" || result.Transaction.Amount != "20.50" {
		t.Fatalf("created transaction = %#v, want canonical Metro/20.50", result.Transaction)
	}
	if transactionCategoryID(result.Transaction) != groceries.ID || transactionCategory(result.Transaction) != "Groceries" {
		t.Fatalf("created category = %#v, want stored Groceries", result.Transaction)
	}
	if result.Transaction.Note == nil || *result.Transaction.Note != "weekly" {
		t.Fatalf("created note = %#v, want weekly", result.Transaction.Note)
	}
	if result.Transaction.CreatedAt == "" || result.Transaction.UpdatedAt == "" {
		t.Fatalf("created timestamps missing: %#v", result.Transaction)
	}

	mapping := loadStoredMapping(t, ctx, db, "metro")
	if mapping.Merchant != "Metro" || mapping.CategoryID != groceries.ID {
		t.Fatalf("created mapping = %#v, want submitted Metro spelling", mapping)
	}
	if got := countTransactions(t, ctx, db); got != 1 {
		t.Fatalf("transaction count = %d, want 1", got)
	}
}

func TestAddOmittedCategoryWithActiveMappingIsKnownMerchantMatched(t *testing.T) {
	ctx := context.Background()
	store, categories, merchants, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	existing := setMerchant(t, ctx, merchants, "Metro", "Groceries")
	const frozen = "2020-01-01T00:00:00.000Z"
	freezeMappingTimestamps(t, ctx, db, existing.ID, frozen)
	before := loadStoredMapping(t, ctx, db, "Metro")

	result, fields, err := store.Add(ctx, transaction.AddInput{Amount: "20.00", Merchant: "metro"})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add(matched known) = %#v fields %#v error %v", result, fields, err)
	}
	if result.CategorySource != transaction.CategorySourceKnownMerchant || result.MerchantMappingAction != transaction.MappingActionMatched {
		t.Fatalf("matched flags = (%q, %q)", result.CategorySource, result.MerchantMappingAction)
	}
	if result.Transaction.Merchant != "metro" || transactionCategoryID(result.Transaction) != groceries.ID || transactionCategory(result.Transaction) != "Groceries" {
		t.Fatalf("matched transaction = %#v, want submitted metro spelling", result.Transaction)
	}

	after := loadStoredMapping(t, ctx, db, "Metro")
	if after != before || after.Merchant != "Metro" || after.CreatedAt != frozen || after.UpdatedAt != frozen {
		t.Fatalf("mapping after matched = %#v, want unchanged %#v", after, before)
	}
}

func TestAddSuppliedSameActiveCategoryIsProvidedMatched(t *testing.T) {
	ctx := context.Background()
	store, categories, merchants, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	existing := setMerchant(t, ctx, merchants, "Metro", "Groceries")
	const frozen = "2020-01-01T00:00:00.000Z"
	freezeMappingTimestamps(t, ctx, db, existing.ID, frozen)
	before := loadStoredMapping(t, ctx, db, "Metro")

	result, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "METRO",
		Category: stringPtr("groceries"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add(same category) = %#v fields %#v error %v", result, fields, err)
	}
	if result.CategorySource != transaction.CategorySourceProvided || result.MerchantMappingAction != transaction.MappingActionMatched {
		t.Fatalf("same-category flags = (%q, %q)", result.CategorySource, result.MerchantMappingAction)
	}
	if result.Transaction.Merchant != "METRO" {
		t.Fatalf("transaction merchant = %q, want METRO", result.Transaction.Merchant)
	}
	after := loadStoredMapping(t, ctx, db, "Metro")
	if after != before {
		t.Fatalf("mapping after same-category matched = %#v, want %#v", after, before)
	}
}

func TestAddSuppliedDifferentActiveCategoryPreservesMapping(t *testing.T) {
	ctx := context.Background()
	store, categories, merchants, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	health := createCategory(t, ctx, categories, "Health")
	existing := setMerchant(t, ctx, merchants, "Metro", "Groceries")
	const frozen = "2020-01-01T00:00:00.000Z"
	freezeMappingTimestamps(t, ctx, db, existing.ID, frozen)
	before := loadStoredMapping(t, ctx, db, "Metro")

	result, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Metro",
		Category: stringPtr("Health"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add(preserved) = %#v fields %#v error %v", result, fields, err)
	}
	if result.CategorySource != transaction.CategorySourceProvided || result.MerchantMappingAction != transaction.MappingActionPreserved {
		t.Fatalf("preserved flags = (%q, %q)", result.CategorySource, result.MerchantMappingAction)
	}
	if transactionCategoryID(result.Transaction) != health.ID || transactionCategory(result.Transaction) != "Health" {
		t.Fatalf("preserved transaction = %#v, want Health", result.Transaction)
	}
	after := loadStoredMapping(t, ctx, db, "Metro")
	if after != before || after.CategoryID != existing.CategoryID || after.UpdatedAt != frozen {
		t.Fatalf("mapping after preserved = %#v, want unchanged %#v", after, before)
	}
}

func TestAddOmittedCategoryWithInactiveMappingWritesNothing(t *testing.T) {
	ctx := context.Background()
	store, categories, merchants, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	alpha := createCategory(t, ctx, categories, "Alpha")
	health := createCategory(t, ctx, categories, "Health")
	existing := setMerchant(t, ctx, merchants, "Shoppers Drug Mart", "Health")
	if _, changed, _, err := categories.Disable(ctx, "Health"); err != nil || !changed {
		t.Fatalf("Disable(Health) = changed %v, error %v", changed, err)
	}
	const frozen = "2020-01-01T00:00:00.000Z"
	freezeMappingTimestamps(t, ctx, db, existing.ID, frozen)
	before := loadStoredMapping(t, ctx, db, "Shoppers Drug Mart")

	_, fields, err := store.Add(ctx, transaction.AddInput{Amount: "20.00", Merchant: "shoppers drug mart"})
	if len(fields) != 0 {
		t.Fatalf("inactive mapping fields = %#v, want none", fields)
	}
	var inactive *transaction.MerchantCategoryInactiveError
	if !errors.As(err, &inactive) || !errors.Is(err, transaction.ErrMerchantCategoryInactive) {
		t.Fatalf("inactive mapping error = %v, want MerchantCategoryInactiveError", err)
	}
	if inactive.KnownMerchant.ID != existing.ID || inactive.KnownMerchant.Merchant != "Shoppers Drug Mart" {
		t.Fatalf("inactive known_merchant = %#v, want canonical Shoppers", inactive.KnownMerchant)
	}
	if inactive.KnownMerchant.Category != "Health" || inactive.KnownMerchant.CategoryActive || inactive.KnownMerchant.CategoryID != health.ID {
		t.Fatalf("inactive mapping category = %#v, want inactive Health", inactive.KnownMerchant)
	}
	if inactive.ActiveCategories == nil || len(inactive.ActiveCategories) != 1 || inactive.ActiveCategories[0].ID != alpha.ID {
		t.Fatalf("inactive recovery list = %#v, want Alpha", inactive.ActiveCategories)
	}
	if loadStoredMapping(t, ctx, db, "Shoppers Drug Mart") != before {
		t.Fatal("inactive mapping was written")
	}
	if got := countTransactions(t, ctx, db); got != 0 {
		t.Fatalf("transaction rows after inactive mapping = %d, want 0", got)
	}
}

func TestAddSuppliedActiveCategoryReplacesInactiveMapping(t *testing.T) {
	ctx := context.Background()
	store, categories, merchants, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Health")
	groceries := createCategory(t, ctx, categories, "Groceries")
	existing := setMerchant(t, ctx, merchants, "Shoppers", "Health")
	if _, changed, _, err := categories.Disable(ctx, "Health"); err != nil || !changed {
		t.Fatalf("Disable(Health) = changed %v, error %v", changed, err)
	}
	const frozen = "2020-01-01T00:00:00.000Z"
	freezeMappingTimestamps(t, ctx, db, existing.ID, frozen)

	result, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "SHOPPERS",
		Category: stringPtr("Groceries"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add(replaced_inactive) = %#v fields %#v error %v", result, fields, err)
	}
	if result.CategorySource != transaction.CategorySourceProvided || result.MerchantMappingAction != transaction.MappingActionReplacedInactive {
		t.Fatalf("replaced flags = (%q, %q)", result.CategorySource, result.MerchantMappingAction)
	}
	if transactionCategoryID(result.Transaction) != groceries.ID || result.Transaction.Merchant != "SHOPPERS" {
		t.Fatalf("replaced transaction = %#v, want Groceries and submitted spelling", result.Transaction)
	}

	after := loadStoredMapping(t, ctx, db, "Shoppers")
	if after.ID != existing.ID || after.Merchant != "Shoppers" || after.CreatedAt != frozen {
		t.Fatalf("replaced mapping identity = %#v, want preserved Shoppers/%s", after, frozen)
	}
	if after.CategoryID != groceries.ID {
		t.Fatalf("replaced mapping category_id = %d, want %d", after.CategoryID, groceries.ID)
	}
	if after.UpdatedAt == frozen {
		t.Fatal("replaced mapping left updated_at unchanged")
	}
}

func TestAddSupplyingInactiveMappedCategoryIsCategoryInactive(t *testing.T) {
	ctx := context.Background()
	store, categories, merchants, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	health := createCategory(t, ctx, categories, "Health")
	createCategory(t, ctx, categories, "Groceries")
	existing := setMerchant(t, ctx, merchants, "Shoppers", "Health")
	if _, changed, _, err := categories.Disable(ctx, "Health"); err != nil || !changed {
		t.Fatalf("Disable(Health) = changed %v, error %v", changed, err)
	}
	const frozen = "2020-01-01T00:00:00.000Z"
	freezeMappingTimestamps(t, ctx, db, existing.ID, frozen)

	_, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Shoppers",
		Category: stringPtr("health"),
	})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	var inactive *transaction.CategoryInactiveError
	if !errors.As(err, &inactive) || !errors.Is(err, transaction.ErrCategoryInactive) {
		t.Fatalf("error = %v, want CategoryInactiveError", err)
	}
	if inactive.Category.ID != health.ID || inactive.Category.Name != "Health" || inactive.Category.Active {
		t.Fatalf("inactive category = %#v, want canonical Health", inactive.Category)
	}
	after := loadStoredMapping(t, ctx, db, "Shoppers")
	if after.CategoryID != health.ID || after.UpdatedAt != frozen {
		t.Fatalf("mapping after supplying inactive category = %#v, want unchanged", after)
	}
	if got := countTransactions(t, ctx, db); got != 0 {
		t.Fatalf("transaction rows = %d, want 0", got)
	}
}

func TestAddOmittedCategoryWithoutMappingIsRequired(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.Add(ctx, transaction.AddInput{Amount: "20.00", Merchant: "  Metro grocery store  "})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	var required *transaction.MerchantCategoryRequiredError
	if !errors.As(err, &required) || !errors.Is(err, transaction.ErrMerchantCategoryRequired) {
		t.Fatalf("error = %v, want MerchantCategoryRequiredError", err)
	}
	if required.Merchant != "Metro grocery store" {
		t.Fatalf("required merchant = %q, want trimmed submitted spelling", required.Merchant)
	}
	assertNoWrites(t, ctx, db)
}

func TestAddMissingSuppliedCategoryIsNotFound(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Metro",
		Category: stringPtr(" Pharmacy "),
	})
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

	alpha := createCategory(t, ctx, categories, "Alpha")
	beta := createCategory(t, ctx, categories, "beta")
	_, _, err = store.Add(ctx, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Metro",
		Category: stringPtr("Pharmacy"),
	})
	if !errors.As(err, &missing) {
		t.Fatalf("error = %v, want CategoryNotFoundError with active list", err)
	}
	if missing.ActiveCategories == nil || len(missing.ActiveCategories) != 2 {
		t.Fatalf("active recovery = %#v, want Alpha, beta", missing.ActiveCategories)
	}
	if missing.ActiveCategories[0].ID != alpha.ID || missing.ActiveCategories[1].ID != beta.ID {
		t.Fatalf("active recovery order = %#v, want Alpha, beta", missing.ActiveCategories)
	}
	assertNoWrites(t, ctx, db)
}

func TestAddInactiveSuppliedCategoryReturnsCanonicalRowAndActiveList(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	alpha := createCategory(t, ctx, categories, "Alpha")
	dining := createCategory(t, ctx, categories, "Dining")
	if _, changed, _, err := categories.Disable(ctx, "Dining"); err != nil || !changed {
		t.Fatalf("Disable(Dining) = changed %v, error %v", changed, err)
	}

	_, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Metro",
		Category: stringPtr("dining"),
	})
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
	if inactive.ActiveCategories == nil || len(inactive.ActiveCategories) != 1 || inactive.ActiveCategories[0].ID != alpha.ID {
		t.Fatalf("active recovery = %#v, want Alpha", inactive.ActiveCategories)
	}
	assertNoWrites(t, ctx, db)
}

func TestAddSuppliedCategoryErrorsHappenBeforeMappingInspection(t *testing.T) {
	ctx := context.Background()
	now := torontoTime(t, 2026, 8, 15, 12, 0)

	t.Run("missing category with no mapping", func(t *testing.T) {
		store, _, _, db := openTransactionStore(t, now)
		_, _, err := store.Add(ctx, transaction.AddInput{
			Amount:   "20.00",
			Merchant: "Metro",
			Category: stringPtr("Pharmacy"),
		})
		var missing *transaction.CategoryNotFoundError
		if !errors.As(err, &missing) {
			t.Fatalf("error = %v, want CategoryNotFoundError", err)
		}
		if errors.Is(err, transaction.ErrMerchantCategoryRequired) {
			t.Fatal("missing supplied category inspected mapping first")
		}
		assertNoWrites(t, ctx, db)
	})

	t.Run("missing category with inactive mapping", func(t *testing.T) {
		store, categories, merchants, db := openTransactionStore(t, now)
		createCategory(t, ctx, categories, "Health")
		existing := setMerchant(t, ctx, merchants, "Shoppers", "Health")
		if _, changed, _, err := categories.Disable(ctx, "Health"); err != nil || !changed {
			t.Fatalf("Disable(Health) = changed %v, error %v", changed, err)
		}
		const frozen = "2020-01-01T00:00:00.000Z"
		freezeMappingTimestamps(t, ctx, db, existing.ID, frozen)

		_, _, err := store.Add(ctx, transaction.AddInput{
			Amount:   "20.00",
			Merchant: "Shoppers",
			Category: stringPtr("Pharmacy"),
		})
		var missing *transaction.CategoryNotFoundError
		if !errors.As(err, &missing) {
			t.Fatalf("error = %v, want CategoryNotFoundError before mapping inspection", err)
		}
		if errors.Is(err, transaction.ErrMerchantCategoryInactive) {
			t.Fatal("missing supplied category returned merchant_category_inactive")
		}
		after := loadStoredMapping(t, ctx, db, "Shoppers")
		if after.UpdatedAt != frozen || after.CategoryID != existing.CategoryID {
			t.Fatalf("mapping changed during supplied-category error: %#v", after)
		}
		if got := countTransactions(t, ctx, db); got != 0 {
			t.Fatalf("transaction rows = %d, want 0", got)
		}
	})

	t.Run("inactive supplied category with no mapping", func(t *testing.T) {
		store, categories, _, db := openTransactionStore(t, now)
		createCategory(t, ctx, categories, "Dining")
		if _, changed, _, err := categories.Disable(ctx, "Dining"); err != nil || !changed {
			t.Fatalf("Disable(Dining) = changed %v, error %v", changed, err)
		}
		_, _, err := store.Add(ctx, transaction.AddInput{
			Amount:   "20.00",
			Merchant: "Metro",
			Category: stringPtr("Dining"),
		})
		var inactive *transaction.CategoryInactiveError
		if !errors.As(err, &inactive) {
			t.Fatalf("error = %v, want CategoryInactiveError", err)
		}
		if errors.Is(err, transaction.ErrMerchantCategoryRequired) {
			t.Fatal("inactive supplied category inspected mapping first")
		}
		assertNoWrites(t, ctx, db)
	})
}

func TestAddCaseInsensitiveLookupKeepsSubmittedTransactionSpelling(t *testing.T) {
	ctx := context.Background()
	store, categories, merchants, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	existing := setMerchant(t, ctx, merchants, "Metro", "Groceries")

	result, fields, err := store.Add(ctx, transaction.AddInput{Amount: "20.00", Merchant: "METRO"})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add(METRO) = %#v fields %#v error %v", result, fields, err)
	}
	if result.Transaction.Merchant != "METRO" {
		t.Fatalf("transaction merchant = %q, want METRO", result.Transaction.Merchant)
	}
	if loadStoredMapping(t, ctx, db, "metro").Merchant != "Metro" {
		t.Fatalf("existing mapping spelling rewritten: %#v", loadStoredMapping(t, ctx, db, "metro"))
	}
	if loadStoredMapping(t, ctx, db, "metro").ID != existing.ID {
		t.Fatal("case-insensitive lookup created a second mapping")
	}
}

func TestAddCreatedMappingUsesSubmittedSpellingAndDoesNotRewriteExisting(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")

	created, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "METRO",
		Category: stringPtr("Groceries"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add(create METRO) = %#v fields %#v error %v", created, fields, err)
	}
	if loadStoredMapping(t, ctx, db, "metro").Merchant != "METRO" {
		t.Fatalf("created mapping spelling = %#v, want METRO", loadStoredMapping(t, ctx, db, "metro"))
	}

	matched, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "5.00",
		Merchant: "metro",
		Category: stringPtr("Groceries"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add(match metro) = %#v fields %#v error %v", matched, fields, err)
	}
	if matched.MerchantMappingAction != transaction.MappingActionMatched {
		t.Fatalf("second action = %q, want matched", matched.MerchantMappingAction)
	}
	if loadStoredMapping(t, ctx, db, "metro").Merchant != "METRO" {
		t.Fatalf("existing mapping spelling rewritten to %q", loadStoredMapping(t, ctx, db, "metro").Merchant)
	}
}

func TestAddExactMerchantNamesAreIndependent(t *testing.T) {
	ctx := context.Background()
	store, categories, merchants, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	metro := setMerchant(t, ctx, merchants, "Metro", "Groceries")
	const frozen = "2020-01-01T00:00:00.000Z"
	freezeMappingTimestamps(t, ctx, db, metro.ID, frozen)

	_, fields, err := store.Add(ctx, transaction.AddInput{Amount: "20.00", Merchant: "Metro grocery store"})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	var required *transaction.MerchantCategoryRequiredError
	if !errors.As(err, &required) || required.Merchant != "Metro grocery store" {
		t.Fatalf("error = %v, want merchant_category_required for similar name", err)
	}

	created, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Metro grocery store",
		Category: stringPtr("Groceries"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add(similar name) = %#v fields %#v error %v", created, fields, err)
	}
	if created.MerchantMappingAction != transaction.MappingActionCreated {
		t.Fatalf("similar-name action = %q, want created", created.MerchantMappingAction)
	}
	if got := countMappings(t, ctx, db); got != 2 {
		t.Fatalf("mapping count = %d, want 2 exact names", got)
	}
	original := loadStoredMapping(t, ctx, db, "Metro")
	if original.ID != metro.ID || original.UpdatedAt != frozen || original.Merchant != "Metro" {
		t.Fatalf("original Metro mapping changed: %#v", original)
	}
	similar := loadStoredMapping(t, ctx, db, "Metro grocery store")
	if similar.Merchant != "Metro grocery store" || similar.ID == metro.ID {
		t.Fatalf("similar mapping = %#v, want independent row", similar)
	}
}

func TestAddIdenticalCallsInsertTwoTransactionsAndOneMapping(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	in := transaction.AddInput{Amount: "20.00", Merchant: "Metro", Category: stringPtr("Groceries")}

	first, fields, err := store.Add(ctx, in)
	if err != nil || len(fields) != 0 {
		t.Fatalf("first Add = %#v fields %#v error %v", first, fields, err)
	}
	if first.MerchantMappingAction != transaction.MappingActionCreated {
		t.Fatalf("first action = %q, want created", first.MerchantMappingAction)
	}

	second, fields, err := store.Add(ctx, in)
	if err != nil || len(fields) != 0 {
		t.Fatalf("second Add = %#v fields %#v error %v", second, fields, err)
	}
	if second.MerchantMappingAction != transaction.MappingActionMatched {
		t.Fatalf("second action = %q, want matched", second.MerchantMappingAction)
	}
	if first.Transaction.ID == second.Transaction.ID {
		t.Fatal("identical Adds reused a transaction id")
	}
	if got := countTransactions(t, ctx, db); got != 2 {
		t.Fatalf("transaction count = %d, want 2", got)
	}
	if got := countMappings(t, ctx, db); got != 1 {
		t.Fatalf("mapping count = %d, want 1", got)
	}
}

func TestAddReturnsCanonicalJoinedTransaction(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")

	result, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "1",
		Merchant: "Metro",
		Category: stringPtr("groceries"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add() = %#v fields %#v error %v", result, fields, err)
	}
	if result.Transaction.Amount != "1.00" || transactionCategory(result.Transaction) != "Groceries" || transactionCategoryID(result.Transaction) != groceries.ID {
		t.Fatalf("canonical transaction = %#v", result.Transaction)
	}
	if result.Transaction.Note != nil {
		t.Fatalf("canonical note = %#v, want nil", result.Transaction.Note)
	}
	stored := listStoredTransactions(t, ctx, db)
	if len(stored) != 1 || stored[0].ID != result.Transaction.ID || stored[0].AmountHundredths != 100 {
		t.Fatalf("stored transaction = %#v, want matching hundredths", stored)
	}
	if stored[0].Note.Valid {
		t.Fatalf("stored note = %#v, want SQL NULL", stored[0].Note)
	}
}

func TestAddNeverMutatesBudgets(t *testing.T) {
	ctx := context.Background()
	now := torontoTime(t, 2026, 8, 15, 12, 0)

	t.Run("month with snapshot", func(t *testing.T) {
		store, categories, _, db := openTransactionStore(t, now)
		groceries := createCategory(t, ctx, categories, "Groceries")
		insertBudget(t, ctx, db, groceries.ID, "2026-08", "500.00")
		const frozen = "2020-01-01T00:00:00.000Z"
		freezeBudgetTimestamps(t, ctx, db, frozen)
		before := listStoredBudgets(t, ctx, db)

		if _, fields, err := store.Add(ctx, transaction.AddInput{
			Amount:   "20.00",
			Merchant: "Metro",
			Category: stringPtr("Groceries"),
			Date:     stringPtr("2026-08-14"),
		}); err != nil || len(fields) != 0 {
			t.Fatalf("Add() fields %#v error %v", fields, err)
		}
		if !reflect.DeepEqual(listStoredBudgets(t, ctx, db), before) {
			t.Fatalf("budgets changed from %#v", before)
		}
	})

	t.Run("month without snapshot", func(t *testing.T) {
		store, categories, _, db := openTransactionStore(t, now)
		createCategory(t, ctx, categories, "Groceries")

		if _, fields, err := store.Add(ctx, transaction.AddInput{
			Amount:   "20.00",
			Merchant: "Metro",
			Category: stringPtr("Groceries"),
			Date:     stringPtr("2026-07-31"),
		}); err != nil || len(fields) != 0 {
			t.Fatalf("Add() fields %#v error %v", fields, err)
		}
		if got := countBudgets(t, ctx, db); got != 0 {
			t.Fatalf("budget rows = %d, want 0", got)
		}
	})
}

func TestAddLeavesHistoricalRowsAndUnrelatedMappingsUnchanged(t *testing.T) {
	ctx := context.Background()
	store, categories, merchants, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	createCategory(t, ctx, categories, "Dining")
	insertBudget(t, ctx, db, groceries.ID, "2026-07", "400.00")
	const frozen = "2020-01-01T00:00:00.000Z"
	freezeBudgetTimestamps(t, ctx, db, frozen)
	unrelated := setMerchant(t, ctx, merchants, "Shoppers", "Dining")
	freezeMappingTimestamps(t, ctx, db, unrelated.ID, frozen)

	first, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "5.00",
		Merchant: "Old Metro",
		Category: stringPtr("Groceries"),
		Date:     stringPtr("2026-07-01"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("seed transaction error %v fields %#v", err, fields)
	}
	if _, err := db.ExecContext(ctx, `UPDATE transactions SET created_at = ?, updated_at = ? WHERE id = ?`, frozen, frozen, first.Transaction.ID); err != nil {
		t.Fatalf("freeze historical transaction: %v", err)
	}
	beforeBudgets := listStoredBudgets(t, ctx, db)
	beforeTx := listStoredTransactions(t, ctx, db)
	beforeMappings := listStoredMappings(t, ctx, db)

	if _, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Metro",
		Category: stringPtr("Groceries"),
	}); err != nil || len(fields) != 0 {
		t.Fatalf("Add() fields %#v error %v", fields, err)
	}

	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db), beforeBudgets) {
		t.Fatal("historical budgets changed")
	}
	afterTx := listStoredTransactions(t, ctx, db)
	if len(afterTx) != 2 || afterTx[0] != beforeTx[0] {
		t.Fatalf("historical transaction changed: %#v vs %#v", afterTx[0], beforeTx[0])
	}
	afterMappings := listStoredMappings(t, ctx, db)
	if afterMappings[0] != beforeMappings[0] {
		t.Fatalf("unrelated mapping changed: %#v vs %#v", afterMappings[0], beforeMappings[0])
	}
}

func TestAddReEnableAfterReplacedInactiveDoesNotMoveMappingBack(t *testing.T) {
	ctx := context.Background()
	store, categories, merchants, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Health")
	groceries := createCategory(t, ctx, categories, "Groceries")
	existing := setMerchant(t, ctx, merchants, "Shoppers", "Health")
	if _, changed, _, err := categories.Disable(ctx, "Health"); err != nil || !changed {
		t.Fatalf("Disable(Health) = changed %v, error %v", changed, err)
	}

	if _, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Shoppers",
		Category: stringPtr("Groceries"),
	}); err != nil || len(fields) != 0 {
		t.Fatalf("Add(replaced_inactive) fields %#v error %v", fields, err)
	}

	reactivated, created, wasReactivated, err := categories.Create(ctx, "Health")
	if err != nil || created || !wasReactivated || !reactivated.Active {
		t.Fatalf("re-enable Health = (%#v, %v, %v, %v)", reactivated, created, wasReactivated, err)
	}
	mapping := loadStoredMapping(t, ctx, db, "Shoppers")
	if mapping.ID != existing.ID || mapping.CategoryID != groceries.ID {
		t.Fatalf("mapping after re-enable = %#v, want Groceries", mapping)
	}
}

func TestAddRollsBackAfterTransactionInsertFailure(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	if _, err := db.ExecContext(ctx, `
		CREATE TRIGGER fail_after_transaction_insert
		AFTER INSERT ON transactions
		BEGIN
			SELECT RAISE(ABORT, 'test transaction insert failure');
		END
	`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	_, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Metro",
		Category: stringPtr("Groceries"),
	})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	if err == nil {
		t.Fatal("Add() error = nil, want trigger failure")
	}
	assertNoWrites(t, ctx, db)
}

func TestAddRollsBackAfterMappingInsertFailure(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	if _, err := db.ExecContext(ctx, `
		CREATE TRIGGER fail_after_mapping_insert
		AFTER INSERT ON known_merchants
		BEGIN
			SELECT RAISE(ABORT, 'test mapping insert failure');
		END
	`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	_, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Metro",
		Category: stringPtr("Groceries"),
	})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	if err == nil {
		t.Fatal("Add() error = nil, want trigger failure")
	}
	assertNoWrites(t, ctx, db)
}

func TestAddRollsBackAfterMappingUpdateFailure(t *testing.T) {
	ctx := context.Background()
	store, categories, merchants, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	health := createCategory(t, ctx, categories, "Health")
	createCategory(t, ctx, categories, "Groceries")
	existing := setMerchant(t, ctx, merchants, "Shoppers", "Health")
	if _, changed, _, err := categories.Disable(ctx, "Health"); err != nil || !changed {
		t.Fatalf("Disable(Health) = changed %v, error %v", changed, err)
	}
	const frozen = "2020-01-01T00:00:00.000Z"
	freezeMappingTimestamps(t, ctx, db, existing.ID, frozen)
	if _, err := db.ExecContext(ctx, `
		CREATE TRIGGER fail_after_mapping_update
		AFTER UPDATE ON known_merchants
		BEGIN
			SELECT RAISE(ABORT, 'test mapping update failure');
		END
	`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	_, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Shoppers",
		Category: stringPtr("Groceries"),
	})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	if err == nil {
		t.Fatal("Add() error = nil, want trigger failure")
	}
	if got := countTransactions(t, ctx, db); got != 0 {
		t.Fatalf("transaction rows after failed replace = %d, want 0", got)
	}
	after := loadStoredMapping(t, ctx, db, "Shoppers")
	if after.ID != existing.ID || after.CategoryID != health.ID || after.UpdatedAt != frozen {
		t.Fatalf("mapping after failed replace = %#v, want unchanged Health", after)
	}
}

func TestAddLeavesSchemaConstraintsEnforced(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	if _, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Metro",
		Category: stringPtr("Groceries"),
	}); err != nil || len(fields) != 0 {
		t.Fatalf("Add() fields %#v error %v", fields, err)
	}

	expectExecError(t, ctx, db, `
		INSERT INTO transactions (merchant, amount_hundredths, date, category_id)
		VALUES (?, ?, ?, ?)
	`, "Orphan", int64(100), "2026-08-14", int64(999999))
	expectExecError(t, ctx, db, `
		INSERT INTO transactions (merchant, amount_hundredths, date, category_id)
		VALUES (?, ?, ?, ?)
	`, " Metro ", int64(100), "2026-08-14", groceries.ID)
	expectExecError(t, ctx, db, `
		INSERT INTO transactions (merchant, amount_hundredths, date, category_id)
		VALUES (?, ?, ?, ?)
	`, "Zero", int64(0), "2026-08-14", groceries.ID)
	expectExecError(t, ctx, db, `
		INSERT INTO transactions (merchant, amount_hundredths, date, category_id)
		VALUES (?, ?, ?, ?)
	`, "BadDate", int64(100), "2026-8-14", groceries.ID)
	expectExecError(t, ctx, db, `
		INSERT INTO known_merchants (merchant, category_id)
		VALUES (?, ?)
	`, "metro", groceries.ID)

	if got := countTransactions(t, ctx, db); got != 1 {
		t.Fatalf("transaction count after constraint probes = %d, want 1", got)
	}
	if got := countMappings(t, ctx, db); got != 1 {
		t.Fatalf("mapping count after constraint probes = %d, want 1", got)
	}
}
