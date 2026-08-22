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

func TestAddIdempotencyKeySemanticValidation(t *testing.T) {
	ctx := context.Background()
	now := torontoTime(t, 2026, 8, 15, 12, 0)

	tests := []struct {
		name string
		key  string
		want []contract.FieldIssue
	}{
		{name: "empty", key: "", want: []contract.FieldIssue{{Field: "idempotency_key", Reason: "must not be empty"}}},
		{name: "whitespace", key: " \t\n ", want: []contract.FieldIssue{{Field: "idempotency_key", Reason: "must not be empty"}}},
		{name: "NUL", key: "key\x00", want: []contract.FieldIssue{{Field: "idempotency_key", Reason: "must not contain NUL characters"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, _, _, db := openTransactionStore(t, now)
			_, fields, err := store.Add(ctx, transaction.AddInput{
				Amount:         "20.00",
				Merchant:       "Metro",
				Category:       stringPtr("Groceries"),
				IdempotencyKey: stringPtr(tt.key),
			})
			if err != nil {
				t.Fatalf("Add() error = %v, want semantic issues", err)
			}
			if !reflect.DeepEqual(fields, tt.want) {
				t.Fatalf("fields = %#v, want %#v", fields, tt.want)
			}
			assertNoWrites(t, ctx, db)
			if got := countIdempotency(t, ctx, db); got != 0 {
				t.Fatalf("idempotency rows = %d, want 0", got)
			}
		})
	}
}

func TestAddTrimsIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")

	first, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:         "20.00",
		Merchant:       "Metro",
		Category:       stringPtr("Groceries"),
		IdempotencyKey: stringPtr("  expense-1  "),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add() = %#v fields %#v error %v", first, fields, err)
	}

	replay, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:         "20.00",
		Merchant:       "Metro",
		Category:       stringPtr("Groceries"),
		IdempotencyKey: stringPtr("expense-1"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("replay = %#v fields %#v error %v", replay, fields, err)
	}
	if !replay.IdempotentReplay || replay.Transaction.ID != first.Transaction.ID {
		t.Fatalf("trimmed-key replay = %#v, want original id %d", replay, first.Transaction.ID)
	}
	if got := countTransactions(t, ctx, db); got != 1 {
		t.Fatalf("transaction rows = %d, want 1", got)
	}
}

func TestAddIdempotentReplayDoesNotRepeatMappingWrites(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")

	first, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:         "20",
		Merchant:       "Metro",
		Category:       stringPtr("Groceries"),
		IdempotencyKey: stringPtr("expense-1"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add() = %#v fields %#v error %v", first, fields, err)
	}
	if first.IdempotentReplay || first.MerchantMappingAction != transaction.MappingActionCreated {
		t.Fatalf("first write flags = %#v", first)
	}
	beforeMapping := loadStoredMapping(t, ctx, db, "Metro")

	replay, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:         "20.00",
		Merchant:       "Metro",
		Category:       stringPtr("Groceries"),
		IdempotencyKey: stringPtr("expense-1"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("replay = %#v fields %#v error %v", replay, fields, err)
	}
	if !replay.IdempotentReplay || replay.Transaction.ID != first.Transaction.ID {
		t.Fatalf("replay = %#v, want original id", replay)
	}
	if replay.MerchantMappingAction != transaction.MappingActionCreated {
		t.Fatalf("replay mapping action = %q, want original created", replay.MerchantMappingAction)
	}
	if loadStoredMapping(t, ctx, db, "Metro") != beforeMapping {
		t.Fatal("replay rewrote the merchant mapping")
	}
	if got := countTransactions(t, ctx, db); got != 1 {
		t.Fatalf("transaction rows = %d, want 1", got)
	}
	if got := countMappings(t, ctx, db); got != 1 {
		t.Fatalf("mapping rows = %d, want 1", got)
	}
}

func TestAddOmittedDateRetryOnLaterDayReplays(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")

	first, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:         "20.00",
		Merchant:       "Metro",
		Category:       stringPtr("Groceries"),
		IdempotencyKey: stringPtr("expense-1"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add() = %#v fields %#v error %v", first, fields, err)
	}
	if first.Transaction.Date != "2026-08-15" {
		t.Fatalf("omitted date = %q, want 2026-08-15", first.Transaction.Date)
	}

	store.Now = func() time.Time { return torontoTime(t, 2026, 8, 16, 12, 0) }
	replay, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:         "20.00",
		Merchant:       "Metro",
		Category:       stringPtr("Groceries"),
		IdempotencyKey: stringPtr("expense-1"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("later-day replay = %#v fields %#v error %v", replay, fields, err)
	}
	if !replay.IdempotentReplay || replay.Transaction.ID != first.Transaction.ID || replay.Transaction.Date != "2026-08-15" {
		t.Fatalf("later-day replay = %#v, want original omitted-date transaction", replay)
	}
}

func TestAddSuppliedDateIsNotEquivalentToOmittedDate(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")

	if _, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:         "20.00",
		Merchant:       "Metro",
		Category:       stringPtr("Groceries"),
		IdempotencyKey: stringPtr("expense-1"),
	}); err != nil || len(fields) != 0 {
		t.Fatalf("omitted-date Add() fields %#v error %v", fields, err)
	}

	_, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:         "20.00",
		Merchant:       "Metro",
		Category:       stringPtr("Groceries"),
		Date:           stringPtr("2026-08-15"),
		IdempotencyKey: stringPtr("expense-1"),
	})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	var conflict *transaction.IdempotencyConflictError
	if !errors.As(err, &conflict) || conflict.Reason != transaction.IdempotencyReasonPayloadMismatch {
		t.Fatalf("error = %v, want payload_mismatch", err)
	}
	if got := countTransactions(t, ctx, db); got != 1 {
		t.Fatalf("transaction rows after mismatch = %d, want 1", got)
	}
}

func TestAddChangedPayloadConflictsWithoutWriting(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")

	if _, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:         "20.00",
		Merchant:       "Metro",
		Category:       stringPtr("Groceries"),
		IdempotencyKey: stringPtr("expense-1"),
	}); err != nil || len(fields) != 0 {
		t.Fatalf("first Add() fields %#v error %v", fields, err)
	}
	beforeMappings := countMappings(t, ctx, db)

	_, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:         "21.00",
		Merchant:       "Metro",
		Category:       stringPtr("Groceries"),
		IdempotencyKey: stringPtr("expense-1"),
	})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	var conflict *transaction.IdempotencyConflictError
	if !errors.As(err, &conflict) || !errors.Is(err, transaction.ErrIdempotencyConflict) {
		t.Fatalf("error = %v, want IdempotencyConflictError", err)
	}
	if conflict.IdempotencyKey != "expense-1" || conflict.Reason != transaction.IdempotencyReasonPayloadMismatch {
		t.Fatalf("conflict = %#v", conflict)
	}
	if got := countTransactions(t, ctx, db); got != 1 {
		t.Fatalf("transaction rows = %d, want 1", got)
	}
	if got := countMappings(t, ctx, db); got != beforeMappings {
		t.Fatalf("mapping rows = %d, want %d", got, beforeMappings)
	}
}

func TestAddFailedIdempotencyKeyDoesNotConsumeKey(t *testing.T) {
	ctx := context.Background()
	now := torontoTime(t, 2026, 8, 15, 12, 0)
	store, categories, _, db := openTransactionStore(t, now)

	_, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:         "20.00",
		Merchant:       "Metro",
		Category:       stringPtr("Groceries"),
		IdempotencyKey: stringPtr("expense-1"),
	})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	var missing *transaction.CategoryNotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("error = %v, want CategoryNotFoundError", err)
	}
	if got := countIdempotency(t, ctx, db); got != 0 {
		t.Fatalf("idempotency rows after failure = %d, want 0", got)
	}

	createCategory(t, ctx, categories, "Groceries")
	result, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:         "20.00",
		Merchant:       "Metro",
		Category:       stringPtr("Groceries"),
		IdempotencyKey: stringPtr("expense-1"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("retry after failure = %#v fields %#v error %v", result, fields, err)
	}
	if result.IdempotentReplay {
		t.Fatal("failed request consumed the key")
	}
}

func TestAddUpdatedRecordReplaysCurrentCanonicalRow(t *testing.T) {
	ctx := context.Background()
	store, categories, _, _ := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")

	first, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:         "20.00",
		Merchant:       "Metro",
		Category:       stringPtr("Groceries"),
		IdempotencyKey: stringPtr("expense-1"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add() = %#v fields %#v error %v", first, fields, err)
	}
	updated := mustUpdate(t, ctx, store, transaction.UpdateInput{
		ID:     first.Transaction.ID,
		Amount: stringPtr("10.00"),
	})
	if updated.Transaction.Amount != "10.00" {
		t.Fatalf("updated amount = %q, want 10.00", updated.Transaction.Amount)
	}

	replay, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:         "20.00",
		Merchant:       "Metro",
		Category:       stringPtr("Groceries"),
		IdempotencyKey: stringPtr("expense-1"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("replay = %#v fields %#v error %v", replay, fields, err)
	}
	if !replay.IdempotentReplay || replay.Transaction.ID != first.Transaction.ID {
		t.Fatalf("replay = %#v, want original id", replay)
	}
	if replay.Transaction.Amount != "10.00" {
		t.Fatalf("replay amount = %q, want current 10.00", replay.Transaction.Amount)
	}
}

func TestAddRemovedTransactionRetiresKey(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")

	first, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:         "20.00",
		Merchant:       "Metro",
		Category:       stringPtr("Groceries"),
		IdempotencyKey: stringPtr("expense-1"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add() = %#v fields %#v error %v", first, fields, err)
	}
	mustRemove(t, ctx, store, first.Transaction.ID)

	_, fields, err = store.Add(ctx, transaction.AddInput{
		Amount:         "20.00",
		Merchant:       "Metro",
		Category:       stringPtr("Groceries"),
		IdempotencyKey: stringPtr("expense-1"),
	})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	var conflict *transaction.IdempotencyConflictError
	if !errors.As(err, &conflict) || conflict.Reason != transaction.IdempotencyReasonTransactionRemoved {
		t.Fatalf("error = %v, want transaction_removed", err)
	}
	if got := countTransactions(t, ctx, db); got != 0 {
		t.Fatalf("transaction rows after retired replay = %d, want 0", got)
	}
}

func TestAddWithoutKeyStillCreatesSeparateTransactions(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")

	first := addTransaction(t, ctx, store, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Metro",
		Category: stringPtr("Groceries"),
	})
	second := addTransaction(t, ctx, store, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Metro",
		Category: stringPtr("Groceries"),
	})
	if first.ID == second.ID {
		t.Fatal("retries without an idempotency key reused a transaction id")
	}
	if got := countTransactions(t, ctx, db); got != 2 {
		t.Fatalf("transaction rows without an idempotency key = %d, want 2", got)
	}
	if got := countIdempotency(t, ctx, db); got != 0 {
		t.Fatalf("idempotency rows = %d, want 0", got)
	}
}

func TestAddAndAddBatchKeysAreIndependent(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")

	single, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:         "20.00",
		Merchant:       "Metro",
		Category:       stringPtr("Groceries"),
		Date:           stringPtr("2026-08-14"),
		IdempotencyKey: stringPtr("shared-key"),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add() = %#v fields %#v error %v", single, fields, err)
	}
	batch := addBatch(t, ctx, store, transaction.AddBatchInput{
		IdempotencyKey: "shared-key",
		Transactions: []transaction.BatchRow{{
			Amount:   "20.00",
			Merchant: "Metro",
			Category: stringPtr("Groceries"),
			Date:     "2026-08-14",
		}},
	})
	if batch.Transactions[0].Transaction.ID == single.Transaction.ID {
		t.Fatal("batch reused the single-add transaction under the same textual key")
	}
	if got := countTransactions(t, ctx, db); got != 2 {
		t.Fatalf("transaction rows = %d, want 2 independent writes", got)
	}
}
