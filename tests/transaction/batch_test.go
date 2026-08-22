package transaction_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/jordanp2002/local-finance-mcp/internal/transaction"
)

func TestAddBatchRecordsMixedRowsInInputOrder(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 19, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	entertainment := createCategory(t, ctx, categories, "Entertainment")

	result := addBatch(t, ctx, store, transaction.AddBatchInput{
		IdempotencyKey: "  statement-2026-08-19-page-1  ",
		Transactions: []transaction.BatchRow{
			{
				Amount:   "24.18",
				Merchant: "Metro",
				Category: stringPtr("Groceries"),
				Date:     "2026-08-18",
				Note:     stringPtr("Imported from statement screenshot"),
			},
			{
				Amount:   "15.99",
				Merchant: "Netflix",
				Category: stringPtr("Entertainment"),
				Date:     "2026-08-17",
			},
			{
				Amount:   "8.50",
				Merchant: "metro",
				Date:     "2026-08-16",
			},
		},
	})

	if result.IdempotencyKey != "statement-2026-08-19-page-1" {
		t.Fatalf("idempotency key = %q, want trimmed key", result.IdempotencyKey)
	}
	if result.IdempotentReplay {
		t.Fatal("first execution reported a replay")
	}
	if result.TotalHundredths != 4867 {
		t.Fatalf("total hundredths = %d, want 4867", result.TotalHundredths)
	}
	if len(result.Transactions) != 3 {
		t.Fatalf("result count = %d, want 3", len(result.Transactions))
	}

	first := result.Transactions[0]
	if first.CategorySource != transaction.CategorySourceProvided || first.MerchantMappingAction != transaction.MappingActionCreated {
		t.Fatalf("row 0 flags = (%q, %q)", first.CategorySource, first.MerchantMappingAction)
	}
	if first.Transaction.Merchant != "Metro" || first.Transaction.Amount != "24.18" || first.Transaction.Date != "2026-08-18" {
		t.Fatalf("row 0 transaction = %#v", first.Transaction)
	}
	if first.Transaction.CategoryID != groceries.ID || first.Transaction.Category != "Groceries" {
		t.Fatalf("row 0 category = %#v, want Groceries", first.Transaction)
	}
	if first.Transaction.Note == nil || *first.Transaction.Note != "Imported from statement screenshot" {
		t.Fatalf("row 0 note = %#v", first.Transaction.Note)
	}

	second := result.Transactions[1]
	if second.CategorySource != transaction.CategorySourceProvided || second.MerchantMappingAction != transaction.MappingActionCreated {
		t.Fatalf("row 1 flags = (%q, %q)", second.CategorySource, second.MerchantMappingAction)
	}
	if second.Transaction.Merchant != "Netflix" || second.Transaction.CategoryID != entertainment.ID {
		t.Fatalf("row 1 transaction = %#v", second.Transaction)
	}

	third := result.Transactions[2]
	if third.CategorySource != transaction.CategorySourceKnownMerchant || third.MerchantMappingAction != transaction.MappingActionMatched {
		t.Fatalf("row 2 flags = (%q, %q), want later row to use earlier Metro mapping", third.CategorySource, third.MerchantMappingAction)
	}
	if third.Transaction.Merchant != "metro" || third.Transaction.CategoryID != groceries.ID || third.Transaction.Date != "2026-08-16" {
		t.Fatalf("row 2 transaction = %#v", third.Transaction)
	}

	if first.Transaction.ID == second.Transaction.ID || second.Transaction.ID == third.Transaction.ID {
		t.Fatal("batch rows reused transaction IDs")
	}
	if got := countTransactions(t, ctx, db); got != 3 {
		t.Fatalf("transaction rows = %d, want 3", got)
	}
	if got := countMappings(t, ctx, db); got != 2 {
		t.Fatalf("mapping rows = %d, want Metro and Netflix", got)
	}
	if got := countImports(t, ctx, db); got != 1 {
		t.Fatalf("import rows = %d, want 1", got)
	}
	if got := countImportItems(t, ctx, db); got != 3 {
		t.Fatalf("import item rows = %d, want 3", got)
	}
}

func TestAddBatchFirstDomainErrorRollsBackEntireBatch(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")
	beforeCategories := countCategories(t, ctx, db)

	_, fields, err := store.AddBatch(ctx, transaction.AddBatchInput{
		IdempotencyKey: "statement-1",
		Transactions: []transaction.BatchRow{
			{Amount: "20.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: "2026-08-14"},
			{Amount: "5.00", Merchant: "Shoppers", Category: stringPtr("Pharmacy"), Date: "2026-08-13"},
		},
	})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	var rowErr *transaction.BatchRowError
	if !errors.As(err, &rowErr) || rowErr.Index != 1 {
		t.Fatalf("error = %v, want BatchRowError index 1", err)
	}
	var missing *transaction.CategoryNotFoundError
	if !errors.As(err, &missing) || missing.Requested != "Pharmacy" {
		t.Fatalf("unwrapped error = %v, want CategoryNotFoundError Pharmacy", err)
	}
	assertNoBatchWrites(t, ctx, db, 0)
	if got := countCategories(t, ctx, db); got != beforeCategories {
		t.Fatalf("category rows = %d, want %d", got, beforeCategories)
	}

	result := addBatch(t, ctx, store, transaction.AddBatchInput{
		IdempotencyKey: "statement-1",
		Transactions: []transaction.BatchRow{
			{Amount: "20.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: "2026-08-14"},
		},
	})
	if result.IdempotentReplay {
		t.Fatal("domain failure consumed the idempotency key")
	}
}

func TestAddBatchFailsUnmappedMerchantBeforeLaterMappingWouldCreateIt(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")

	_, fields, err := store.AddBatch(ctx, transaction.AddBatchInput{
		IdempotencyKey: "statement-1",
		Transactions: []transaction.BatchRow{
			{Amount: "20.00", Merchant: "Metro", Date: "2026-08-14"},
			{Amount: "5.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: "2026-08-13"},
		},
	})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	var rowErr *transaction.BatchRowError
	if !errors.As(err, &rowErr) || rowErr.Index != 0 {
		t.Fatalf("error = %v, want BatchRowError index 0", err)
	}
	var required *transaction.MerchantCategoryRequiredError
	if !errors.As(err, &required) || required.Merchant != "Metro" {
		t.Fatalf("unwrapped error = %v, want MerchantCategoryRequiredError Metro", err)
	}
	assertNoBatchWrites(t, ctx, db, 0)
}

func TestAddBatchReplayAndConflict(t *testing.T) {
	ctx := context.Background()
	now := torontoTime(t, 2026, 8, 15, 12, 0)

	tests := []struct {
		name      string
		original  []transaction.BatchRow
		retry     []transaction.BatchRow
		replay    bool
		mismatch  bool
		wantTotal int64
	}{
		{
			name: "equivalent amount strings replay",
			original: []transaction.BatchRow{{
				Amount: "20", Merchant: "Metro", Category: stringPtr("Groceries"), Date: "2026-08-14",
			}},
			retry: []transaction.BatchRow{{
				Amount: "20.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: "2026-08-14",
			}},
			replay:    true,
			wantTotal: 2000,
		},
		{
			name: "changed amount is payload_mismatch",
			original: []transaction.BatchRow{{
				Amount: "20.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: "2026-08-14",
			}},
			retry: []transaction.BatchRow{{
				Amount: "21.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: "2026-08-14",
			}},
			mismatch: true,
		},
		{
			name: "changed order is payload_mismatch",
			original: []transaction.BatchRow{
				{Amount: "20.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: "2026-08-14"},
				{Amount: "5.00", Merchant: "Netflix", Category: stringPtr("Groceries"), Date: "2026-08-13"},
			},
			retry: []transaction.BatchRow{
				{Amount: "5.00", Merchant: "Netflix", Category: stringPtr("Groceries"), Date: "2026-08-13"},
				{Amount: "20.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: "2026-08-14"},
			},
			mismatch: true,
		},
		{
			name: "omitted versus supplied category is payload_mismatch",
			original: []transaction.BatchRow{{
				Amount: "20.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: "2026-08-14",
			}},
			retry: []transaction.BatchRow{{
				Amount: "20.00", Merchant: "Metro", Date: "2026-08-14",
			}},
			mismatch: true,
		},
		{
			name: "recased merchant is payload_mismatch",
			original: []transaction.BatchRow{{
				Amount: "20.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: "2026-08-14",
			}},
			retry: []transaction.BatchRow{{
				Amount: "20.00", Merchant: "metro", Category: stringPtr("Groceries"), Date: "2026-08-14",
			}},
			mismatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, categories, _, db := openTransactionStore(t, now)
			createCategory(t, ctx, categories, "Groceries")
			first := addBatch(t, ctx, store, transaction.AddBatchInput{
				IdempotencyKey: "statement-1",
				Transactions:   tt.original,
			})
			transactionCount := countTransactions(t, ctx, db)
			mappingCount := countMappings(t, ctx, db)

			retry, fields, err := store.AddBatch(ctx, transaction.AddBatchInput{
				IdempotencyKey: "statement-1",
				Transactions:   tt.retry,
			})
			if len(fields) != 0 {
				t.Fatalf("retry fields = %#v, want none", fields)
			}
			if tt.mismatch {
				var conflict *transaction.IdempotencyConflictError
				if !errors.As(err, &conflict) || !errors.Is(err, transaction.ErrIdempotencyConflict) {
					t.Fatalf("retry error = %v, want IdempotencyConflictError", err)
				}
				if conflict.IdempotencyKey != "statement-1" || conflict.Reason != transaction.IdempotencyReasonPayloadMismatch {
					t.Fatalf("conflict = %#v, want payload_mismatch", conflict)
				}
				if got := countTransactions(t, ctx, db); got != transactionCount {
					t.Fatalf("transaction rows after mismatch = %d, want %d", got, transactionCount)
				}
				if got := countMappings(t, ctx, db); got != mappingCount {
					t.Fatalf("mapping rows after mismatch = %d, want %d", got, mappingCount)
				}
				return
			}
			if err != nil {
				t.Fatalf("retry error = %v", err)
			}
			if !retry.IdempotentReplay {
				t.Fatal("equivalent retry was not a replay")
			}
			if len(retry.Transactions) != len(first.Transactions) {
				t.Fatalf("replay count = %d, want %d", len(retry.Transactions), len(first.Transactions))
			}
			for i := range first.Transactions {
				if retry.Transactions[i].Transaction.ID != first.Transactions[i].Transaction.ID {
					t.Fatalf("replay id[%d] = %d, want %d", i, retry.Transactions[i].Transaction.ID, first.Transactions[i].Transaction.ID)
				}
			}
			if retry.TotalHundredths != tt.wantTotal {
				t.Fatalf("replay total = %d, want %d", retry.TotalHundredths, tt.wantTotal)
			}
			if got := countTransactions(t, ctx, db); got != transactionCount {
				t.Fatalf("transaction rows after replay = %d, want %d", got, transactionCount)
			}
			if got := countMappings(t, ctx, db); got != mappingCount {
				t.Fatalf("mapping rows after replay = %d, want %d", got, mappingCount)
			}
		})
	}
}

func TestAddBatchLifecycleUpdateReplayAndRemovalRetirement(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")

	first := addBatch(t, ctx, store, transaction.AddBatchInput{
		IdempotencyKey: "statement-1",
		Transactions: []transaction.BatchRow{
			{Amount: "20.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: "2026-08-14"},
			{Amount: "5.00", Merchant: "No Frills", Category: stringPtr("Groceries"), Date: "2026-08-13"},
		},
	})
	updated := mustUpdate(t, ctx, store, transaction.UpdateInput{
		ID:     first.Transactions[0].Transaction.ID,
		Amount: stringPtr("10.00"),
	})
	if updated.Transaction.Amount != "10.00" {
		t.Fatalf("updated amount = %q, want 10.00", updated.Transaction.Amount)
	}

	replay, fields, err := store.AddBatch(ctx, transaction.AddBatchInput{
		IdempotencyKey: "statement-1",
		Transactions: []transaction.BatchRow{
			{Amount: "20.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: "2026-08-14"},
			{Amount: "5.00", Merchant: "No Frills", Category: stringPtr("Groceries"), Date: "2026-08-13"},
		},
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("replay after update = %#v fields %#v error %v", replay, fields, err)
	}
	if !replay.IdempotentReplay {
		t.Fatal("updated-record retry was not a replay")
	}
	if replay.Transactions[0].Transaction.ID != first.Transactions[0].Transaction.ID {
		t.Fatal("replay changed transaction IDs")
	}
	if replay.Transactions[0].Transaction.Amount != "10.00" {
		t.Fatalf("replay amount = %q, want current 10.00", replay.Transactions[0].Transaction.Amount)
	}
	if replay.TotalHundredths != 1500 {
		t.Fatalf("replay total = %d, want 1500 from current records", replay.TotalHundredths)
	}

	mustRemove(t, ctx, store, first.Transactions[1].Transaction.ID)
	_, fields, err = store.AddBatch(ctx, transaction.AddBatchInput{
		IdempotencyKey: "statement-1",
		Transactions: []transaction.BatchRow{
			{Amount: "20.00", Merchant: "Metro", Category: stringPtr("Groceries"), Date: "2026-08-14"},
			{Amount: "5.00", Merchant: "No Frills", Category: stringPtr("Groceries"), Date: "2026-08-13"},
		},
	})
	if len(fields) != 0 {
		t.Fatalf("retired replay fields = %#v, want none", fields)
	}
	var conflict *transaction.IdempotencyConflictError
	if !errors.As(err, &conflict) || conflict.Reason != transaction.IdempotencyReasonTransactionRemoved {
		t.Fatalf("error = %v, want transaction_removed", err)
	}
	if !reflect.DeepEqual(conflict.RemovedIndexes, []int{1}) {
		t.Fatalf("removed_indexes = %#v, want [1]", conflict.RemovedIndexes)
	}
	if got := countTransactions(t, ctx, db); got != 1 {
		t.Fatalf("transaction rows after retired replay = %d, want 1 (no recreation)", got)
	}
	if loadStoredTransaction(t, ctx, db, first.Transactions[0].Transaction.ID).AmountHundredths != 1000 {
		t.Fatal("surviving imported transaction was rewritten")
	}
}

func TestAddBatchCheckedTotalOverflowWritesNothing(t *testing.T) {
	ctx := context.Background()
	store, _, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.AddBatch(ctx, transaction.AddBatchInput{
		IdempotencyKey: "statement-1",
		Transactions: []transaction.BatchRow{
			{Amount: "92233720368547758.07", Merchant: "One", Date: "2026-08-14"},
			{Amount: "92233720368547758.07", Merchant: "Two", Date: "2026-08-14"},
		},
	})
	if err != nil {
		t.Fatalf("AddBatch() error = %v, want semantic issue", err)
	}
	want := []contract.FieldIssue{{Field: "transactions", Reason: "total must fit the supported amount range"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("overflow fields = %#v, want %#v", fields, want)
	}
	assertNoBatchWrites(t, ctx, db, 0)
}

func TestAddBatchDoesNotCreateCategoriesOrChangeBudgets(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	insertBudget(t, ctx, db, groceries.ID, "2026-08", "500.00")
	const frozen = "2020-01-01T00:00:00.000Z"
	freezeBudgetTimestamps(t, ctx, db, frozen)
	beforeBudgets := listStoredBudgets(t, ctx, db)
	beforeCategories := countCategories(t, ctx, db)

	row := transaction.BatchRow{
		Amount:   "20.00",
		Merchant: "Metro",
		Category: stringPtr("Groceries"),
		Date:     "2026-08-14",
	}
	addBatch(t, ctx, store, transaction.AddBatchInput{
		IdempotencyKey: "statement-1",
		Transactions:   []transaction.BatchRow{row},
	})
	addBatch(t, ctx, store, transaction.AddBatchInput{
		IdempotencyKey: "statement-2",
		Transactions:   []transaction.BatchRow{row},
	})

	if got := countCategories(t, ctx, db); got != beforeCategories {
		t.Fatalf("category rows = %d, want %d", got, beforeCategories)
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db), beforeBudgets) {
		t.Fatalf("budgets changed from %#v", beforeBudgets)
	}
	if got := countTransactions(t, ctx, db); got != 2 {
		t.Fatalf("identical-looking imports with distinct keys = %d, want 2", got)
	}
}
