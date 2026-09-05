package transaction_test

import (
	"context"
	"testing"

	"github.com/jordanp2002/Local-Ledger/internal/transaction"
	"github.com/jordanp2002/Local-Ledger/tests/testutil"
)

func BenchmarkListFullPage(b *testing.B) {
	ctx := context.Background()
	db := testutil.OpenDB(b)
	// Populate outside the timer; measure listing, not expense creation.
	_, err := db.Exec(`
		INSERT INTO categories (id, name) VALUES (1, 'Groceries'), (2, 'Household');
		WITH RECURSIVE ids(id) AS (SELECT 1 UNION ALL SELECT id+1 FROM ids WHERE id<200)
		INSERT INTO transactions (id, merchant, date) SELECT id, 'Store', '2026-08-01' FROM ids;
		INSERT INTO transaction_allocations (transaction_id, category_id, amount_hundredths)
		SELECT t.id, c.id, t.id*100 FROM transactions t CROSS JOIN categories c;
	`)
	if err != nil {
		b.Fatal(err)
	}
	store := &transaction.Store{DB: db}
	limit := int64(200)
	b.ReportAllocs()
	for b.Loop() {
		result, fields, err := store.List(ctx, transaction.ListInput{Limit: &limit})
		if err != nil || len(fields) != 0 || len(result.Transactions) != 200 {
			b.Fatalf("list full page: %v, %v, %d rows", err, fields, len(result.Transactions))
		}
	}
}
