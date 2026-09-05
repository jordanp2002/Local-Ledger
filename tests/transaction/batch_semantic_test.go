package transaction_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/transaction"
)

func TestAddBatchSemanticValidation(t *testing.T) {
	ctx := context.Background()
	now := torontoTime(t, 2026, 8, 15, 12, 0)

	validRow := transaction.BatchRow{
		Amount:   "20.00",
		Merchant: "Metro",
		Category: stringPtr("Groceries"),
		Date:     "2026-08-14",
	}

	tests := []struct {
		name  string
		input transaction.AddBatchInput
		want  []contract.FieldIssue
	}{
		{
			name: "empty key",
			input: transaction.AddBatchInput{
				IdempotencyKey: "",
				Transactions:   []transaction.BatchRow{validRow},
			},
			want: []contract.FieldIssue{{Field: "idempotency_key", Reason: "must not be empty"}},
		},
		{
			name: "whitespace key",
			input: transaction.AddBatchInput{
				IdempotencyKey: " \t\n ",
				Transactions:   []transaction.BatchRow{validRow},
			},
			want: []contract.FieldIssue{{Field: "idempotency_key", Reason: "must not be empty"}},
		},
		{
			name: "NUL key",
			input: transaction.AddBatchInput{
				IdempotencyKey: "key\x00",
				Transactions:   []transaction.BatchRow{validRow},
			},
			want: []contract.FieldIssue{{Field: "idempotency_key", Reason: "must not contain NUL characters"}},
		},
		{
			name: "nil transactions",
			input: transaction.AddBatchInput{
				IdempotencyKey: "statement-1",
			},
			want: []contract.FieldIssue{{Field: "transactions", Reason: "must contain between 1 and 100 items"}},
		},
		{
			name: "empty transactions",
			input: transaction.AddBatchInput{
				IdempotencyKey: "statement-1",
				Transactions:   []transaction.BatchRow{},
			},
			want: []contract.FieldIssue{{Field: "transactions", Reason: "must contain between 1 and 100 items"}},
		},
		{
			name: "too many transactions",
			input: transaction.AddBatchInput{
				IdempotencyKey: "statement-1",
				Transactions:   makeBatchRows(transaction.MaxBatchTransactions+1, validRow),
			},
			want: []contract.FieldIssue{{Field: "transactions", Reason: "must contain between 1 and 100 items"}},
		},
		{
			name: "omitted date is not today",
			input: transaction.AddBatchInput{
				IdempotencyKey: "statement-1",
				Transactions: []transaction.BatchRow{{
					Amount:   "20.00",
					Merchant: "Metro",
					Category: stringPtr("Groceries"),
				}},
			},
			want: []contract.FieldIssue{{Field: "transactions[0].date", Reason: "must be a valid YYYY-MM-DD date"}},
		},
		{
			name: "indexed amount path",
			input: transaction.AddBatchInput{
				IdempotencyKey: "statement-1",
				Transactions: []transaction.BatchRow{{
					Amount:   "0",
					Merchant: "Metro",
					Category: stringPtr("Groceries"),
					Date:     "2026-08-14",
				}},
			},
			want: []contract.FieldIssue{{Field: "transactions[0].amount", Reason: "must be greater than zero"}},
		},
		{
			name: "collects row issues in array order",
			input: transaction.AddBatchInput{
				IdempotencyKey: "statement-1",
				Transactions: []transaction.BatchRow{
					{Amount: "0", Merchant: "Metro", Category: stringPtr("Groceries"), Date: "2026-08-14"},
					{Amount: "1.00", Merchant: "Netflix", Category: stringPtr("Entertainment"), Date: "not-a-date"},
				},
			},
			want: []contract.FieldIssue{
				{Field: "transactions[0].amount", Reason: "must be greater than zero"},
				{Field: "transactions[1].date", Reason: "must be a valid YYYY-MM-DD date"},
			},
		},
		{
			name: "key before transactions bounds",
			input: transaction.AddBatchInput{
				IdempotencyKey: "",
			},
			want: []contract.FieldIssue{
				{Field: "idempotency_key", Reason: "must not be empty"},
				{Field: "transactions", Reason: "must contain between 1 and 100 items"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, _, _, db := openTransactionStore(t, now)
			_, fields, err := store.AddBatch(ctx, tt.input)
			if err != nil {
				t.Fatalf("AddBatch() error = %v, want semantic issues", err)
			}
			if !reflect.DeepEqual(fields, tt.want) {
				t.Fatalf("fields = %#v, want %#v", fields, tt.want)
			}
			assertNoBatchWrites(t, ctx, db, 0)
		})
	}
}

func TestAddBatchFailedSemanticValidationDoesNotConsumeKey(t *testing.T) {
	ctx := context.Background()
	store, categories, _, db := openTransactionStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")

	_, fields, err := store.AddBatch(ctx, transaction.AddBatchInput{
		IdempotencyKey: "statement-1",
		Transactions:   []transaction.BatchRow{},
	})
	if err != nil || len(fields) == 0 {
		t.Fatalf("invalid AddBatch() fields %#v error %v", fields, err)
	}
	if got := countImports(t, ctx, db); got != 0 {
		t.Fatalf("import rows after semantic failure = %d, want 0", got)
	}

	result := addBatch(t, ctx, store, transaction.AddBatchInput{
		IdempotencyKey: "statement-1",
		Transactions: []transaction.BatchRow{{
			Amount:   "20.00",
			Merchant: "Metro",
			Category: stringPtr("Groceries"),
			Date:     "2026-08-14",
		}},
	})
	if result.IdempotentReplay || result.IdempotencyKey != "statement-1" {
		t.Fatalf("retry after semantic failure = %#v, want a first write", result)
	}
}

func makeBatchRows(n int, row transaction.BatchRow) []transaction.BatchRow {
	rows := make([]transaction.BatchRow, n)
	for i := range rows {
		rows[i] = row
	}
	return rows
}
