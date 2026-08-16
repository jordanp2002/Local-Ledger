package budget_test

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
)

func openBudgetStore(t *testing.T, now time.Time) (*budget.Store, *category.Store, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "finance.db")
	db, err := database.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			t.Errorf("close database: %v", err)
		}
	})
	return &budget.Store{DB: db, Now: func() time.Time { return now }}, &category.Store{DB: db, Now: func() time.Time { return now }}, db
}

func torontoTime(t *testing.T, year int, month time.Month, day, hour, minute int) time.Time {
	t.Helper()
	location, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatalf("LoadLocation(America/Toronto): %v", err)
	}
	return time.Date(year, month, day, hour, minute, 0, 0, location)
}

func createCategory(t *testing.T, ctx context.Context, store *category.Store, name string) contract.Category {
	t.Helper()
	created, wasCreated, reactivated, err := store.Create(ctx, name)
	if err != nil {
		t.Fatalf("Create(%q): %v", name, err)
	}
	if !wasCreated || reactivated {
		t.Fatalf("Create(%q) created=%v reactivated=%v, want created=true reactivated=false", name, wasCreated, reactivated)
	}
	return created
}

func countBudgetRows(t *testing.T, ctx context.Context, db *sql.DB, month string) int64 {
	t.Helper()
	var count int64
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM budgets WHERE month = ?`, month).Scan(&count); err != nil {
		t.Fatalf("count budgets for %s: %v", month, err)
	}
	return count
}

func insertBudget(t *testing.T, ctx context.Context, db *sql.DB, categoryID int64, month, amount string) {
	t.Helper()
	hundredths, err := contract.ParseAmount(amount)
	if err != nil {
		t.Fatalf("ParseAmount(%q): %v", amount, err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO budgets (category_id, month, amount_hundredths)
		VALUES (?, ?, ?)
	`, categoryID, month, hundredths); err != nil {
		t.Fatalf("insert budget: %v", err)
	}
}

func budgetAmounts(t *testing.T, ctx context.Context, db *sql.DB, month string) []int64 {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
		SELECT amount_hundredths
		FROM budgets
		WHERE month = ?
		ORDER BY id ASC
	`, month)
	if err != nil {
		t.Fatalf("query budget amounts: %v", err)
	}
	defer rows.Close()

	amounts := make([]int64, 0)
	for rows.Next() {
		var amount int64
		if err := rows.Scan(&amount); err != nil {
			t.Fatalf("scan budget amount: %v", err)
		}
		amounts = append(amounts, amount)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate budget amounts: %v", err)
	}
	return amounts
}
