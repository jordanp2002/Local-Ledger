package category_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jordanp2002/Local-Ledger/internal/category"
	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/database"
)

func openStore(t *testing.T) *category.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "finance.db")
	db, err := database.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", path, err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			t.Errorf("close database: %v", err)
		}
	})
	return &category.Store{DB: db}
}

func storeWithNow(t *testing.T, now time.Time) *category.Store {
	t.Helper()
	store := openStore(t)
	store.Now = func() time.Time { return now }
	return store
}

func torontoLocation(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatalf("LoadLocation(America/Toronto): %v", err)
	}
	return loc
}

func torontoClock(t *testing.T, year int, month time.Month, day, hour, minute int) time.Time {
	t.Helper()
	return time.Date(year, month, day, hour, minute, 0, 0, torontoLocation(t))
}

func insertBudget(t *testing.T, ctx context.Context, db *sql.DB, categoryID int64, month, amount string) int64 {
	t.Helper()
	hundredths, err := contract.ParseAmount(amount)
	if err != nil {
		t.Fatalf("ParseAmount(%q): %v", amount, err)
	}
	result, err := db.ExecContext(ctx, `
		INSERT INTO budgets (category_id, month, amount_hundredths)
		VALUES (?, ?, ?)
	`, categoryID, month, hundredths)
	if err != nil {
		t.Fatalf("insert budget %s %s: %v", month, amount, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("budget LastInsertId: %v", err)
	}
	return id
}

func insertTransaction(t *testing.T, ctx context.Context, db *sql.DB, merchant, amount, date string, categoryID int64) int64 {
	t.Helper()
	hundredths, err := contract.ParseAmount(amount)
	if err != nil {
		t.Fatalf("ParseAmount(%q): %v", amount, err)
	}
	result, err := db.ExecContext(ctx, `
		INSERT INTO transactions (merchant, date)
		VALUES (?, ?)
	`, merchant, date)
	if err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("transaction LastInsertId: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO transaction_allocations (transaction_id, category_id, amount_hundredths)
		VALUES (?, ?, ?)
	`, id, categoryID, hundredths); err != nil {
		t.Fatalf("insert transaction allocation: %v", err)
	}
	return id
}

func insertKnownMerchant(t *testing.T, ctx context.Context, db *sql.DB, merchant string, categoryID int64) int64 {
	t.Helper()
	result, err := db.ExecContext(ctx, `
		INSERT INTO known_merchants (merchant, category_id)
		VALUES (?, ?)
	`, merchant, categoryID)
	if err != nil {
		t.Fatalf("insert known merchant: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("known merchant LastInsertId: %v", err)
	}
	return id
}

func countRows(t *testing.T, ctx context.Context, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return n
}

func budgetExists(t *testing.T, ctx context.Context, db *sql.DB, categoryID int64, month string) bool {
	t.Helper()
	return countRows(t, ctx, db, `
		SELECT count(*) FROM budgets WHERE category_id = ? AND month = ?
	`, categoryID, month) == 1
}

func loadBudget(t *testing.T, ctx context.Context, db *sql.DB, categoryID int64, month string) (id int64, amount string, createdAt, updatedAt string) {
	t.Helper()
	var hundredths int64
	if err := db.QueryRowContext(ctx, `
		SELECT id, amount_hundredths, created_at, updated_at
		FROM budgets
		WHERE category_id = ? AND month = ?
	`, categoryID, month).Scan(&id, &hundredths, &createdAt, &updatedAt); err != nil {
		t.Fatalf("load budget %s: %v", month, err)
	}
	formatted, err := contract.FormatAmount(hundredths)
	if err != nil {
		t.Fatalf("FormatAmount(%d): %v", hundredths, err)
	}
	return id, formatted, createdAt, updatedAt
}

func setUpdatedAt(t *testing.T, ctx context.Context, db *sql.DB, id int64, value string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `UPDATE categories SET updated_at = ? WHERE id = ?`, value, id); err != nil {
		t.Fatalf("set updated_at: %v", err)
	}
}

func mustCreate(t *testing.T, ctx context.Context, store *category.Store, name string) contract.Category {
	t.Helper()
	cat, created, reactivated, err := store.Create(ctx, name)
	if err != nil {
		t.Fatalf("Create(%q) error = %v", name, err)
	}
	if !created || reactivated {
		t.Fatalf("Create(%q) created=%v reactivated=%v, want created=true reactivated=false", name, created, reactivated)
	}
	return cat
}
