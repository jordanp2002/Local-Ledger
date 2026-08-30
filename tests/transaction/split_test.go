package transaction_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/jordanp2002/local-finance-mcp/internal/transaction"
)

func addSplitTransaction(t *testing.T, ctx context.Context, store *transaction.Store, in transaction.AddSplitInput) contract.Transaction {
	t.Helper()
	result, fields, err := store.AddSplit(ctx, in)
	if err != nil || len(fields) != 0 {
		t.Fatalf("AddSplit() = %#v fields %#v error %v", result, fields, err)
	}
	return result.Transaction
}

func TestAddSplitUsesOneCanonicalParentAndPreservesMapping(t *testing.T) {
	ctx := context.Background()
	store, categories, merchants, db := openTransactionStore(t, torontoTime(t, 2026, 8, 30, 12, 0))
	household := createCategory(t, ctx, categories, "Household")
	groceries := createCategory(t, ctx, categories, "Groceries")
	pharmacy := createCategory(t, ctx, categories, "Pharmacy")
	mapping := setMerchant(t, ctx, merchants, "Costco", "Household")
	beforeMapping := loadStoredMapping(t, ctx, db, mapping.Merchant)

	got := addSplitTransaction(t, ctx, store, transaction.AddSplitInput{
		Merchant: "Costco",
		Date:     stringPtr("2026-08-30"),
		Note:     stringPtr("Household trip"),
		Allocations: []transaction.AllocationInput{
			{Category: "Pharmacy", Amount: "5.00"},
			{Category: " groceries ", Amount: "65"},
			{Category: "Household", Amount: "20.00"},
		},
	})

	if got.Amount != "90.00" || got.Merchant != "Costco" || got.Date != "2026-08-30" {
		t.Fatalf("split parent = %#v", got)
	}
	if got.CategoryID != nil || got.Category != nil {
		t.Fatalf("split compatibility category fields = %#v/%#v, want nil", got.CategoryID, got.Category)
	}
	want := []contract.TransactionAllocation{
		{CategoryID: groceries.ID, Category: "Groceries", Amount: "65.00"},
		{CategoryID: household.ID, Category: "Household", Amount: "20.00"},
		{CategoryID: pharmacy.ID, Category: "Pharmacy", Amount: "5.00"},
	}
	if !reflect.DeepEqual(got.Allocations, want) {
		t.Fatalf("allocations = %#v, want %#v", got.Allocations, want)
	}
	if countTransactions(t, ctx, db) != 1 || countMappings(t, ctx, db) != 1 {
		t.Fatalf("rows = transactions %d mappings %d, want 1/1", countTransactions(t, ctx, db), countMappings(t, ctx, db))
	}
	if loadStoredMapping(t, ctx, db, mapping.Merchant) != beforeMapping {
		t.Fatal("split changed the existing merchant mapping")
	}
}

func TestAddSplitIdempotencyCanonicalizesCasingAndRejectsChangedOrder(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 30, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	createCategory(t, ctx, categories, "Household")

	first, fields, err := store.AddSplit(ctx, transaction.AddSplitInput{
		Merchant: "Costco",
		Date:     stringPtr("2026-08-30"),
		Allocations: []transaction.AllocationInput{
			{Category: "Household", Amount: "20.00"},
			{Category: "Groceries", Amount: "65.00"},
		},
		IdempotencyKey: stringPtr("costco-1"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("first AddSplit() = %#v fields %#v error %v", first, fields, err)
	}

	replay, fields, err := store.AddSplit(ctx, transaction.AddSplitInput{
		Merchant: "Costco",
		Date:     stringPtr("2026-08-30"),
		Allocations: []transaction.AllocationInput{
			{Category: "HOUSEHOLD", Amount: "20.0"},
			{Category: " groceries ", Amount: "65"},
		},
		IdempotencyKey: stringPtr("costco-1"),
	})
	if err != nil || len(fields) != 0 || !replay.IdempotentReplay {
		t.Fatalf("canonical replay = %#v fields %#v error %v", replay, fields, err)
	}
	if replay.Transaction.ID != first.Transaction.ID || countTransactions(t, ctx, db) != 1 || countIdempotency(t, ctx, db) != 1 {
		t.Fatalf("replay rows/id = %d/%d/%d, want %d/1/1", replay.Transaction.ID, countTransactions(t, ctx, db), countIdempotency(t, ctx, db), first.Transaction.ID)
	}

	_, fields, err = store.AddSplit(ctx, transaction.AddSplitInput{
		Merchant: "Costco",
		Date:     stringPtr("2026-08-30"),
		Allocations: []transaction.AllocationInput{
			{Category: "Groceries", Amount: "65.00"},
			{Category: "Household", Amount: "20.00"},
		},
		IdempotencyKey: stringPtr("costco-1"),
	})
	if len(fields) != 0 || !errors.Is(err, transaction.ErrIdempotencyConflict) {
		t.Fatalf("reordered replay fields %#v error %v, want idempotency conflict", fields, err)
	}

	_, fields, err = store.AddSplit(ctx, transaction.AddSplitInput{
		Merchant: "Costco",
		Date:     stringPtr("2026-08-30"),
		Note:     stringPtr("changed"),
		Allocations: []transaction.AllocationInput{
			{Category: "Groceries", Amount: "65.00"},
			{Category: "Household", Amount: "20.00"},
		},
		IdempotencyKey: stringPtr("costco-1"),
	})
	if len(fields) != 0 {
		t.Fatalf("conflict fields = %#v, want none", fields)
	}
	var conflict *transaction.IdempotencyConflictError
	if !errors.As(err, &conflict) || !errors.Is(err, transaction.ErrIdempotencyConflict) {
		t.Fatalf("conflict error = %v, want idempotency conflict", err)
	}
	if countTransactions(t, ctx, db) != 1 {
		t.Fatalf("transaction rows after conflict = %d, want 1", countTransactions(t, ctx, db))
	}
}

func TestUpdateAllocationsRejectsCaseInsensitiveDuplicate(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 30, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	created := addTransaction(t, ctx, store, transaction.AddInput{
		Merchant: "Metro",
		Amount:   "20.00",
		Category: stringPtr("Groceries"),
		Date:     stringPtr("2026-08-30"),
	})
	allocations := []transaction.AllocationInput{
		{Category: "Groceries", Amount: "10.00"},
		{Category: " groceries ", Amount: "10.00"},
	}
	_, fields, err := store.Update(ctx, transaction.UpdateInput{ID: created.ID, Allocations: &allocations})
	duplicateIssue := false
	for _, issue := range fields {
		if issue.Field == "allocations[1].category" {
			duplicateIssue = true
		}
	}
	if err != nil || !duplicateIssue {
		t.Fatalf("Update() fields %#v error %v, want duplicate category issue", fields, err)
	}
}

func TestAddSplitResolvesBeforeWriting(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 30, 12, 0))
	createCategory(t, ctx, categories, "Groceries")

	_, fields, err := store.AddSplit(ctx, transaction.AddSplitInput{
		Merchant: "Costco",
		Allocations: []transaction.AllocationInput{
			{Category: "Groceries", Amount: "65.00"},
			{Category: "Missing", Amount: "20.00"},
		},
	})
	var missing *transaction.CategoryNotFoundError
	if len(fields) != 0 || !errors.As(err, &missing) {
		t.Fatalf("missing category fields %#v error %v", fields, err)
	}
	if countTransactions(t, ctx, db) != 0 || countMappings(t, ctx, db) != 0 || countIdempotency(t, ctx, db) != 0 {
		t.Fatalf("rows after failed split = transactions %d mappings %d idempotency %d", countTransactions(t, ctx, db), countMappings(t, ctx, db), countIdempotency(t, ctx, db))
	}
}

func TestUpdateSplitAllocationsReplacesAtomicallyAndRejectsLegacyPatch(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 30, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	createCategory(t, ctx, categories, "Household")
	createCategory(t, ctx, categories, "Pharmacy")
	split := addSplitTransaction(t, ctx, store, transaction.AddSplitInput{
		Merchant: "Costco",
		Date:     stringPtr("2026-08-30"),
		Allocations: []transaction.AllocationInput{
			{Category: "Groceries", Amount: "65.00"},
			{Category: "Household", Amount: "20.00"},
		},
	})

	single := []transaction.AllocationInput{{Category: "Pharmacy", Amount: "85.00"}}
	updated := mustUpdate(t, ctx, store, transaction.UpdateInput{ID: split.ID, Allocations: &single})
	if updated.Transaction.Amount != "85.00" || transactionCategory(updated.Transaction) != "Pharmacy" || len(updated.Transaction.Allocations) != 1 {
		t.Fatalf("split-to-single = %#v", updated.Transaction)
	}

	multi := []transaction.AllocationInput{
		{Category: "Household", Amount: "25.00"},
		{Category: "Groceries", Amount: "60.00"},
	}
	updated = mustUpdate(t, ctx, store, transaction.UpdateInput{ID: split.ID, Allocations: &multi, Merchant: stringPtr("Costco Wholesale")})
	if updated.Transaction.Amount != "85.00" || updated.Transaction.Merchant != "Costco Wholesale" || updated.Transaction.Category != nil || len(updated.Transaction.Allocations) != 2 {
		t.Fatalf("single-to-split = %#v", updated.Transaction)
	}
	if countRows(t, ctx, db, "SELECT count(*) FROM transaction_allocations WHERE transaction_id = ?", split.ID) != 2 {
		t.Fatalf("allocation rows = %d, want 2", countRows(t, ctx, db, "SELECT count(*) FROM transaction_allocations WHERE transaction_id = ?", split.ID))
	}

	_, fields, err := store.Update(ctx, transaction.UpdateInput{ID: split.ID, Amount: stringPtr("84.00")})
	if len(fields) != 0 {
		t.Fatalf("legacy split patch fields = %#v, want none", fields)
	}
	var requires *transaction.SplitTransactionRequiresAllocationsError
	if !errors.As(err, &requires) || !errors.Is(err, transaction.ErrSplitTransactionRequiresAllocations) || requires.ID != split.ID {
		t.Fatalf("legacy split patch error = %v, want split allocation error", err)
	}
	if countRows(t, ctx, db, "SELECT count(*) FROM transaction_allocations WHERE transaction_id = ?", split.ID) != 2 {
		t.Fatal("legacy split patch changed allocations")
	}
}

func TestListSplitCategoryFilterReturnsOneCompleteParent(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 30, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	createCategory(t, ctx, categories, "Household")
	addSplitTransaction(t, ctx, store, transaction.AddSplitInput{
		Merchant: "Costco",
		Date:     stringPtr("2026-08-30"),
		Allocations: []transaction.AllocationInput{
			{Category: "Groceries", Amount: "65.00"},
			{Category: "Household", Amount: "20.00"},
		},
	})

	result := mustList(t, ctx, store, transaction.ListInput{Category: stringPtr("household")})
	if result.Page.Total != 1 || result.Page.Returned != 1 || len(result.Transactions) != 1 {
		t.Fatalf("filtered page = %#v, want one parent", result.Page)
	}
	if result.Transactions[0].Amount != "85.00" || len(result.Transactions[0].Allocations) != 2 || result.Transactions[0].CategoryID != nil {
		t.Fatalf("filtered transaction = %#v", result.Transactions[0])
	}
}
