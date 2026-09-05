package summary_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/jordanp2002/Local-Ledger/internal/category"
	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/summary"
	"github.com/jordanp2002/Local-Ledger/internal/transaction"
	"github.com/jordanp2002/Local-Ledger/tests/testutil"
)

type fixture struct {
	summary      *summary.Store
	categories   *category.Store
	transactions *transaction.Store
	db           *sql.DB
}

func openSummaryStore(t *testing.T, now time.Time) fixture {
	t.Helper()
	db := testutil.OpenDB(t)
	clock := func() time.Time { return now }
	return fixture{
		summary:      &summary.Store{DB: db},
		categories:   &category.Store{DB: db, Now: clock},
		transactions: &transaction.Store{DB: db, Now: clock},
		db:           db,
	}
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

func addTransaction(t *testing.T, ctx context.Context, store *transaction.Store, amount, merchant, categoryName, date string) contract.Transaction {
	t.Helper()
	result, fields, err := store.Add(ctx, transaction.AddInput{
		Amount:   amount,
		Merchant: merchant,
		Category: stringPtr(categoryName),
		Date:     stringPtr(date),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add(%s %s) = %#v fields %#v error %v", amount, merchant, result, fields, err)
	}
	return result.Transaction
}

func insertRawTransaction(t *testing.T, ctx context.Context, db *sql.DB, merchant string, amountHundredths int64, date string, categoryID int64) {
	t.Helper()
	result, err := db.ExecContext(ctx, `INSERT INTO transactions (merchant, date) VALUES (?, ?)`, merchant, date)
	if err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read transaction id: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO transaction_allocations (transaction_id, category_id, amount_hundredths)
		VALUES (?, ?, ?)
	`, id, categoryID, amountHundredths); err != nil {
		t.Fatalf("insert transaction allocation: %v", err)
	}
}

func mustMonthly(t *testing.T, ctx context.Context, store *summary.Store, month string) summary.MonthlyResult {
	t.Helper()
	result, fields, err := store.Monthly(ctx, month)
	if err != nil || len(fields) != 0 {
		t.Fatalf("Monthly(%q) = %#v fields %#v error %v", month, result, fields, err)
	}
	if result.Categories == nil {
		t.Fatal("Monthly() categories is nil, want non-nil slice")
	}
	return result
}

func mustCategory(t *testing.T, ctx context.Context, store *summary.Store, categoryName, month string) contract.CategorySummary {
	t.Helper()
	result, fields, err := store.Category(ctx, categoryName, month)
	if err != nil || len(fields) != 0 {
		t.Fatalf("Category(%q, %q) = %#v fields %#v error %v", categoryName, month, result, fields, err)
	}
	return result
}

func mustSpending(t *testing.T, ctx context.Context, store *summary.Store, in summary.SpendingInput) summary.SpendingResult {
	t.Helper()
	result, fields, err := store.Spending(ctx, in)
	if err != nil || len(fields) != 0 {
		t.Fatalf("Spending() = %#v fields %#v error %v", result, fields, err)
	}
	if result.Categories == nil {
		t.Fatal("Spending() categories is nil, want non-nil slice")
	}
	return result
}

func stringPtr(value string) *string {
	return &value
}

type storedSnapshot struct {
	budgets      []storedRow
	transactions []storedRow
	mappings     []storedRow
	categories   []storedRow
}

type storedRow struct {
	ID        int64
	CreatedAt string
	UpdatedAt string
}

func loadSnapshot(t *testing.T, ctx context.Context, db *sql.DB) storedSnapshot {
	t.Helper()
	return storedSnapshot{
		budgets:      loadRows(t, ctx, db, `SELECT id, created_at, updated_at FROM budgets ORDER BY id`),
		transactions: loadRows(t, ctx, db, `SELECT id, created_at, updated_at FROM transactions ORDER BY id`),
		mappings:     loadRows(t, ctx, db, `SELECT id, created_at, updated_at FROM known_merchants ORDER BY id`),
		categories:   loadRows(t, ctx, db, `SELECT id, created_at, updated_at FROM categories ORDER BY id`),
	}
}

func loadRows(t *testing.T, ctx context.Context, db *sql.DB, query string) []storedRow {
	t.Helper()
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		t.Fatalf("query snapshot: %v", err)
	}
	defer rows.Close()
	stored := make([]storedRow, 0)
	for rows.Next() {
		var row storedRow
		if err := rows.Scan(&row.ID, &row.CreatedAt, &row.UpdatedAt); err != nil {
			t.Fatalf("scan snapshot: %v", err)
		}
		stored = append(stored, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate snapshot: %v", err)
	}
	return stored
}

func assertUnchanged(t *testing.T, ctx context.Context, db *sql.DB, before storedSnapshot) {
	t.Helper()
	after := loadSnapshot(t, ctx, db)
	if !snapshotEqual(before, after) {
		t.Fatalf("database changed after summary read:\nbefore %#v\nafter %#v", before, after)
	}
}

func snapshotEqual(left, right storedSnapshot) bool {
	return rowsEqual(left.budgets, right.budgets) &&
		rowsEqual(left.transactions, right.transactions) &&
		rowsEqual(left.mappings, right.mappings) &&
		rowsEqual(left.categories, right.categories)
}

func rowsEqual(left, right []storedRow) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
