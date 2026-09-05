package transaction_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/transaction"
)

func mustRemove(t *testing.T, ctx context.Context, store *transaction.Store, id int64) contract.Transaction {
	t.Helper()
	got, fields, err := store.Remove(ctx, id)
	if err != nil || len(fields) != 0 {
		t.Fatalf("Remove(%d) = %#v fields %#v error %v", id, got, fields, err)
	}
	return got
}

func expectTransactionNotFound(t *testing.T, err error, wantID int64) {
	t.Helper()
	var notFound *transaction.TransactionNotFoundError
	if !errors.As(err, &notFound) || !errors.Is(err, transaction.ErrTransactionNotFound) {
		t.Fatalf("error = %v, want TransactionNotFoundError", err)
	}
	if notFound.ID != wantID {
		t.Fatalf("not-found id = %d, want %d", notFound.ID, wantID)
	}
}

type storedCategory struct {
	ID        int64
	Name      string
	Active    int
	CreatedAt string
	UpdatedAt string
}

func listStoredCategories(t *testing.T, ctx context.Context, db *sql.DB) []storedCategory {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
		SELECT id, name, active, created_at, updated_at
		FROM categories
		ORDER BY id ASC
	`)
	if err != nil {
		t.Fatalf("query stored categories: %v", err)
	}
	defer rows.Close()

	stored := make([]storedCategory, 0)
	for rows.Next() {
		var row storedCategory
		if err := rows.Scan(&row.ID, &row.Name, &row.Active, &row.CreatedAt, &row.UpdatedAt); err != nil {
			t.Fatalf("scan stored category: %v", err)
		}
		stored = append(stored, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate stored categories: %v", err)
	}
	return stored
}

func TestRemoveDeletesRowAndReturnsPreDeleteCanonicalRecord(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	target := addTransaction(t, ctx, store, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Metro",
		Category: stringPtr("Groceries"),
		Date:     stringPtr("2026-08-01"),
	})
	other := addTransaction(t, ctx, store, transaction.AddInput{
		Amount:   "5.00",
		Merchant: "Old Metro",
		Category: stringPtr("Groceries"),
		Date:     stringPtr("2026-07-01"),
		Note:     stringPtr("historical"),
	})
	if target.Note != nil {
		t.Fatalf("fixture note = %#v, want unset", target.Note)
	}

	removed := mustRemove(t, ctx, store, target.ID)
	if !reflect.DeepEqual(removed, target) {
		t.Fatalf("removed = %#v, want pre-delete %#v", removed, target)
	}
	if removed.Note != nil {
		t.Fatalf("removed note = %#v, want nil", removed.Note)
	}
	if got := countTransactions(t, ctx, db); got != 1 {
		t.Fatalf("transaction count = %d, want 1", got)
	}
	if loadStoredTransaction(t, ctx, db, other.ID).ID != other.ID {
		t.Fatal("sibling transaction was deleted")
	}
	for _, row := range listStoredTransactions(t, ctx, db) {
		if row.ID == target.ID {
			t.Fatalf("target row %d still present", target.ID)
		}
	}
}

func TestRemoveMissingIDIsTransactionNotFound(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	before := loadStoredTransaction(t, ctx, db, seeded.ID)
	beforeMappings := listStoredMappings(t, ctx, db)
	beforeBudgets := listStoredBudgets(t, ctx, db)

	got, fields, err := store.Remove(ctx, 42)
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	if got.ID != 0 {
		t.Fatalf("removed = %#v, want zero value", got)
	}
	expectTransactionNotFound(t, err, 42)
	assertStoredUnchanged(t, ctx, db, before)
	if got := countTransactions(t, ctx, db); got != 1 {
		t.Fatalf("transaction count = %d, want 1", got)
	}
	if !reflect.DeepEqual(listStoredMappings(t, ctx, db), beforeMappings) {
		t.Fatal("mappings changed after missing-id remove")
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db), beforeBudgets) {
		t.Fatal("budgets changed after missing-id remove")
	}
}

func TestRemoveSameIDTwiceIsTransactionNotFound(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	beforeMappings := listStoredMappings(t, ctx, db)

	first := mustRemove(t, ctx, store, seeded.ID)
	if first.ID != seeded.ID {
		t.Fatalf("first remove = %#v, want id %d", first, seeded.ID)
	}

	got, fields, err := store.Remove(ctx, seeded.ID)
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	if got.ID != 0 {
		t.Fatalf("second remove = %#v, want zero value", got)
	}
	expectTransactionNotFound(t, err, seeded.ID)
	if got := countTransactions(t, ctx, db); got != 0 {
		t.Fatalf("transaction count = %d, want 0", got)
	}
	if !reflect.DeepEqual(listStoredMappings(t, ctx, db), beforeMappings) {
		t.Fatal("mappings changed after second remove")
	}
}

func TestRemoveOnlyMerchantTransactionLeavesMappingUnchanged(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	metro := loadStoredMapping(t, ctx, db, "Metro")
	freezeMappingTimestamps(t, ctx, db, metro.ID, frozenTimestamp)
	before := loadStoredMapping(t, ctx, db, "Metro")
	if before.Merchant != "Metro" || before.CategoryID != groceries.ID {
		t.Fatalf("fixture mapping = %#v", before)
	}
	if got := countTransactions(t, ctx, db); got != 1 {
		t.Fatalf("fixture transaction count = %d, want 1", got)
	}

	mustRemove(t, ctx, store, seeded.ID)
	after := loadStoredMapping(t, ctx, db, "Metro")
	if after != before {
		t.Fatalf("mapping changed: %#v vs %#v", after, before)
	}
	if after.Merchant != "Metro" || after.CategoryID != groceries.ID {
		t.Fatalf("mapping identity = %#v, want Metro/%d", after, groceries.ID)
	}
	if after.CreatedAt != frozenTimestamp || after.UpdatedAt != frozenTimestamp {
		t.Fatalf("mapping timestamps = %#v, want frozen", after)
	}
	if got := countMappings(t, ctx, db); got != 1 {
		t.Fatalf("mapping count = %d, want 1", got)
	}
}

func TestRemoveLeavesCategoryAndBudgetsUnchanged(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	createCategory(t, ctx, categories, "Dining")
	insertBudget(t, ctx, db, groceries.ID, "2026-07", "400.00")
	insertBudget(t, ctx, db, groceries.ID, "2026-08", "500.00")
	seeded := seedGroceryTransaction(t, ctx, store)
	freezeBudgetTimestamps(t, ctx, db, frozenTimestamp)
	beforeCategories := listStoredCategories(t, ctx, db)
	beforeBudgets := listStoredBudgets(t, ctx, db)
	if len(beforeBudgets) != 2 {
		t.Fatalf("fixture budgets = %#v, want 2", beforeBudgets)
	}

	mustRemove(t, ctx, store, seeded.ID)
	if !reflect.DeepEqual(listStoredCategories(t, ctx, db), beforeCategories) {
		t.Fatalf("categories changed from %#v", beforeCategories)
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db), beforeBudgets) {
		t.Fatalf("budgets changed from %#v", beforeBudgets)
	}
	if got := countBudgets(t, ctx, db); got != 2 {
		t.Fatalf("budget count = %d, want 2", got)
	}
}

func TestRemoveLeavesOtherTransactionsUnchanged(t *testing.T) {
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

	mustRemove(t, ctx, store, first.ID)
	assertStoredUnchanged(t, ctx, db, beforeSecond)
	if got := countTransactions(t, ctx, db); got != 1 {
		t.Fatalf("transaction count = %d, want 1", got)
	}
}

func TestRemoveRollsBackAfterDeleteFailure(t *testing.T) {
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
	beforeCategories := listStoredCategories(t, ctx, db)

	if _, err := db.ExecContext(ctx, `
		CREATE TRIGGER fail_after_transaction_delete
		AFTER DELETE ON transactions
		BEGIN
			SELECT RAISE(ABORT, 'test transaction delete failure');
		END
	`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	got, fields, err := store.Remove(ctx, seeded.ID)
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	if err == nil {
		t.Fatal("Remove() error = nil, want trigger failure")
	}
	if got.ID != 0 {
		t.Fatalf("removed = %#v, want zero value after abort", got)
	}
	assertStoredUnchanged(t, ctx, db, before)
	if got := countTransactions(t, ctx, db); got != 1 {
		t.Fatalf("transaction count = %d, want 1 after abort", got)
	}
	if !reflect.DeepEqual(listStoredMappings(t, ctx, db), beforeMappings) {
		t.Fatal("mappings changed after rolled-back remove")
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db), beforeBudgets) {
		t.Fatal("budgets changed after rolled-back remove")
	}
	if !reflect.DeepEqual(listStoredCategories(t, ctx, db), beforeCategories) {
		t.Fatal("categories changed after rolled-back remove")
	}
}

func TestRemoveFullRefundLeavesCreatedMapping(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")

	added, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Metro",
		Category: stringPtr("Groceries"),
		Date:     stringPtr("2026-08-14"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add() = %#v fields %#v error %v", added, fields, err)
	}
	if added.MerchantMappingAction != transaction.MappingActionCreated {
		t.Fatalf("mapping action = %q, want created", added.MerchantMappingAction)
	}
	metro := loadStoredMapping(t, ctx, db, "Metro")
	freezeMappingTimestamps(t, ctx, db, metro.ID, frozenTimestamp)
	before := loadStoredMapping(t, ctx, db, "Metro")
	if before.Merchant != "Metro" || before.CategoryID != groceries.ID {
		t.Fatalf("created mapping = %#v", before)
	}

	removed := mustRemove(t, ctx, store, added.Transaction.ID)
	if removed.ID != added.Transaction.ID || removed.Merchant != "Metro" || removed.Amount != "20.00" {
		t.Fatalf("removed refunded purchase = %#v", removed)
	}
	after := loadStoredMapping(t, ctx, db, "Metro")
	if after != before {
		t.Fatalf("mapping after refund = %#v, want %#v", after, before)
	}
	if got := countTransactions(t, ctx, db); got != 0 {
		t.Fatalf("transaction count = %d, want 0", got)
	}
	if got := countMappings(t, ctx, db); got != 1 {
		t.Fatalf("mapping count = %d, want 1", got)
	}
}

func TestRemoveRejectsZeroAndNegativeIDs(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	seeded := seedGroceryTransaction(t, ctx, store)
	before := loadStoredTransaction(t, ctx, db, seeded.ID)
	beforeMappings := listStoredMappings(t, ctx, db)
	want := []contract.FieldIssue{{
		Field:  "id",
		Reason: "must be a positive integer",
	}}

	for _, id := range []int64{0, -1, -42} {
		got, fields, err := store.Remove(ctx, id)
		if err != nil {
			t.Fatalf("Remove(id=%d) error = %v, want semantic issue", id, err)
		}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("Remove(id=%d) fields = %#v, want %#v", id, fields, want)
		}
		if got.ID != 0 {
			t.Fatalf("Remove(id=%d) transaction = %#v, want zero", id, got)
		}
		var notFound *transaction.TransactionNotFoundError
		if errors.As(err, &notFound) {
			t.Fatal("invalid id queried the database")
		}
	}
	assertStoredUnchanged(t, ctx, db, before)
	if !reflect.DeepEqual(listStoredMappings(t, ctx, db), beforeMappings) {
		t.Fatal("mappings changed after invalid-id remove")
	}

	_, fields, err := (*transaction.Store)(nil).Remove(ctx, 0)
	if err != nil {
		t.Fatalf("nil store Remove(0) error = %v, want semantic issue without DB access", err)
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("nil store Remove(0) fields = %#v, want %#v", fields, want)
	}
}

func TestRemoveReturnsCanonicalJoinedTransaction(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	seeded := addTransaction(t, ctx, store, transaction.AddInput{
		Amount:   "1",
		Merchant: "Metro",
		Category: stringPtr("groceries"),
		Date:     stringPtr("2026-08-14"),
	})
	formatted, err := contract.FormatAmount(100)
	if err != nil {
		t.Fatalf("FormatAmount(100): %v", err)
	}

	removed := mustRemove(t, ctx, store, seeded.ID)
	if removed.ID != seeded.ID {
		t.Fatalf("id = %d, want %d", removed.ID, seeded.ID)
	}
	if removed.Amount != formatted || removed.Amount != "1.00" {
		t.Fatalf("amount = %q, want FormatAmount %q", removed.Amount, formatted)
	}
	if removed.Merchant != "Metro" || removed.Date != "2026-08-14" {
		t.Fatalf("canonical identity = %#v", removed)
	}
	if transactionCategoryID(removed) != groceries.ID || transactionCategory(removed) != "Groceries" {
		t.Fatalf("canonical category = %#v, want stored Groceries join", removed)
	}
	if removed.Note != nil {
		t.Fatalf("canonical note = %#v, want nil", removed.Note)
	}
	if removed.CreatedAt == "" || removed.UpdatedAt == "" {
		t.Fatalf("canonical timestamps missing: %#v", removed)
	}
	if removed.CreatedAt != seeded.CreatedAt || removed.UpdatedAt != seeded.UpdatedAt {
		t.Fatalf("timestamps = created %q updated %q, want pre-delete %q / %q", removed.CreatedAt, removed.UpdatedAt, seeded.CreatedAt, seeded.UpdatedAt)
	}
	if got := countTransactions(t, ctx, db); got != 0 {
		t.Fatalf("transaction count = %d, want 0", got)
	}
}

func TestRemoveNilStoreIsInternalError(t *testing.T) {
	ctx := context.Background()

	got, fields, err := (*transaction.Store)(nil).Remove(ctx, 1)
	if len(fields) != 0 {
		t.Fatalf("nil store fields = %#v, want none", fields)
	}
	if got.ID != 0 {
		t.Fatalf("nil store removed = %#v, want zero", got)
	}
	if err == nil || err.Error() != "transaction store database is nil" {
		t.Fatalf("nil store error = %v, want transaction store database is nil", err)
	}

	got, fields, err = (&transaction.Store{}).Remove(ctx, 1)
	if len(fields) != 0 {
		t.Fatalf("nil DB fields = %#v, want none", fields)
	}
	if got.ID != 0 {
		t.Fatalf("nil DB removed = %#v, want zero", got)
	}
	if err == nil || err.Error() != "transaction store database is nil" {
		t.Fatalf("nil DB error = %v, want transaction store database is nil", err)
	}
}
