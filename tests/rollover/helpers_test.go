package rollover_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/budget"
	"github.com/jordanp2002/local-finance-mcp/internal/category"
	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/jordanp2002/local-finance-mcp/internal/database"
	"github.com/jordanp2002/local-finance-mcp/internal/rollover"
	"github.com/jordanp2002/local-finance-mcp/internal/transaction"
)

type fixture struct {
	rollovers    *rollover.Store
	budgets      *budget.Store
	categories   *category.Store
	transactions *transaction.Store
	db           *sql.DB
}

func openFixture(t *testing.T) fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "finance.db")
	db, err := database.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("database.Open(%q): %v", path, err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			t.Errorf("close database: %v", err)
		}
	})
	now := func() time.Time {
		return time.Date(2026, time.August, 31, 23, 30, 0, 0, time.FixedZone("EDT", -4*60*60))
	}
	return fixture{
		rollovers:    &rollover.Store{DB: db, Now: now},
		budgets:      &budget.Store{DB: db, Now: now},
		categories:   &category.Store{DB: db, Now: now},
		transactions: &transaction.Store{DB: db, Now: now},
		db:           db,
	}
}

func createCategory(t *testing.T, ctx context.Context, store *category.Store, name string) contract.Category {
	t.Helper()
	created, wasCreated, reactivated, err := store.Create(ctx, name)
	if err != nil {
		t.Fatalf("create category %q: %v", name, err)
	}
	if !wasCreated || reactivated {
		t.Fatalf("create category %q result created=%v reactivated=%v", name, wasCreated, reactivated)
	}
	return created
}

func createBudget(t *testing.T, ctx context.Context, store *budget.Store, month, categoryName, amount string) budget.CreateResult {
	t.Helper()
	result, fields, err := store.CreateExplicit(ctx, month, []budget.Allocation{{Category: categoryName, Amount: amount}})
	if err != nil || len(fields) != 0 {
		t.Fatalf("create budget %s: result=%#v fields=%#v err=%v", month, result, fields, err)
	}
	return result
}

func addTransaction(t *testing.T, ctx context.Context, store *transaction.Store, amount, categoryName, date string, idempotencyKey *string) transaction.AddResult {
	t.Helper()
	result, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:         amount,
		Merchant:       "Merchant " + categoryName,
		Category:       stringPtr(categoryName),
		Date:           stringPtr(date),
		IdempotencyKey: idempotencyKey,
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("add transaction %s: result=%#v fields=%#v err=%v", amount, result, fields, err)
	}
	return result
}

func createRollover(t *testing.T, ctx context.Context, store *rollover.Store, sourceMonth, categoryName, amount string, sourceTransactionID *int64) rollover.CreateResult {
	t.Helper()
	result, fields, err := store.Create(ctx, rollover.CreateInput{
		SourceMonth:         sourceMonth,
		Category:            categoryName,
		Amount:              amount,
		SourceTransactionID: sourceTransactionID,
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("create rollover %s: result=%#v fields=%#v err=%v", amount, result, fields, err)
	}
	return result
}

func stringPtr(value string) *string {
	return &value
}

func int64Ptr(value int64) *int64 {
	return &value
}

func countRollovers(t *testing.T, ctx context.Context, db *sql.DB) int64 {
	t.Helper()
	var count int64
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM budget_rollovers`).Scan(&count); err != nil {
		t.Fatalf("count rollovers: %v", err)
	}
	return count
}
