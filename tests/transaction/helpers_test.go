package transaction_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/category"
	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/jordanp2002/local-finance-mcp/internal/database"
	"github.com/jordanp2002/local-finance-mcp/internal/merchant"
	"github.com/jordanp2002/local-finance-mcp/internal/transaction"
)

func openTransactionStore(t *testing.T, now time.Time) (*transaction.Store, *category.Store, *merchant.Store, *sql.DB) {
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
	clock := func() time.Time { return now }
	return &transaction.Store{DB: db, Now: clock}, &category.Store{DB: db, Now: clock}, &merchant.Store{DB: db}, db
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

func setMerchant(t *testing.T, ctx context.Context, store *merchant.Store, merchantName, categoryName string) contract.KnownMerchant {
	t.Helper()
	result, err := store.Set(ctx, merchantName, categoryName)
	if err != nil {
		t.Fatalf("Set(%q, %q): %v", merchantName, categoryName, err)
	}
	return result.KnownMerchant
}

func stringPtr(value string) *string {
	return &value
}

func int64Ptr(value int64) *int64 {
	return &value
}

func noteClear() transaction.NotePatch {
	return transaction.NotePatch{Present: true}
}

func noteValue(value string) transaction.NotePatch {
	return transaction.NotePatch{Present: true, Value: stringPtr(value)}
}

func addTransaction(t *testing.T, ctx context.Context, store *transaction.Store, in transaction.AddInput) contract.Transaction {
	t.Helper()
	result, fields, err := store.Add(ctx, in)
	if err != nil || len(fields) != 0 {
		t.Fatalf("Add() = %#v fields %#v error %v", result, fields, err)
	}
	return result.Transaction
}

func mustUpdate(t *testing.T, ctx context.Context, store *transaction.Store, in transaction.UpdateInput) transaction.UpdateResult {
	t.Helper()
	result, fields, err := store.Update(ctx, in)
	if err != nil || len(fields) != 0 {
		t.Fatalf("Update() = %#v fields %#v error %v", result, fields, err)
	}
	return result
}

func seedGroceryTransaction(t *testing.T, ctx context.Context, store *transaction.Store) contract.Transaction {
	t.Helper()
	return addTransaction(t, ctx, store, transaction.AddInput{
		Amount:   "20.00",
		Merchant: "Metro",
		Category: stringPtr("Groceries"),
		Date:     stringPtr("2026-08-01"),
		Note:     stringPtr("weekly"),
	})
}

func assertStoredUnchanged(t *testing.T, ctx context.Context, db *sql.DB, before storedTransaction) {
	t.Helper()
	after := loadStoredTransaction(t, ctx, db, before.ID)
	if after != before {
		t.Fatalf("stored transaction changed: %#v vs %#v", after, before)
	}
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

func countRows(t *testing.T, ctx context.Context, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return count
}

func countTransactions(t *testing.T, ctx context.Context, db *sql.DB) int64 {
	t.Helper()
	return countRows(t, ctx, db, `SELECT count(*) FROM transactions`)
}

func countMappings(t *testing.T, ctx context.Context, db *sql.DB) int64 {
	t.Helper()
	return countRows(t, ctx, db, `SELECT count(*) FROM known_merchants`)
}

func countBudgets(t *testing.T, ctx context.Context, db *sql.DB) int64 {
	t.Helper()
	return countRows(t, ctx, db, `SELECT count(*) FROM budgets`)
}

func countCategories(t *testing.T, ctx context.Context, db *sql.DB) int64 {
	t.Helper()
	return countRows(t, ctx, db, `SELECT count(*) FROM categories`)
}

func countImports(t *testing.T, ctx context.Context, db *sql.DB) int64 {
	t.Helper()
	return countRows(t, ctx, db, `SELECT count(*) FROM transaction_imports`)
}

func countImportItems(t *testing.T, ctx context.Context, db *sql.DB) int64 {
	t.Helper()
	return countRows(t, ctx, db, `SELECT count(*) FROM transaction_import_items`)
}

func countIdempotency(t *testing.T, ctx context.Context, db *sql.DB) int64 {
	t.Helper()
	return countRows(t, ctx, db, `SELECT count(*) FROM transaction_idempotency`)
}

func addBatch(t *testing.T, ctx context.Context, store *transaction.Store, in transaction.AddBatchInput) transaction.AddBatchResult {
	t.Helper()
	result, fields, err := store.AddBatch(ctx, in)
	if err != nil || len(fields) != 0 {
		t.Fatalf("AddBatch() = %#v fields %#v error %v", result, fields, err)
	}
	return result
}

func assertNoBatchWrites(t *testing.T, ctx context.Context, db *sql.DB, wantMappings int64) {
	t.Helper()
	if got := countTransactions(t, ctx, db); got != 0 {
		t.Fatalf("transaction rows = %d, want 0", got)
	}
	if got := countMappings(t, ctx, db); got != wantMappings {
		t.Fatalf("known_merchant rows = %d, want %d", got, wantMappings)
	}
	if got := countImports(t, ctx, db); got != 0 {
		t.Fatalf("transaction_import rows = %d, want 0", got)
	}
	if got := countImportItems(t, ctx, db); got != 0 {
		t.Fatalf("transaction_import_item rows = %d, want 0", got)
	}
}

func assertNoWrites(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	if got := countTransactions(t, ctx, db); got != 0 {
		t.Fatalf("transaction rows = %d, want 0", got)
	}
	if got := countMappings(t, ctx, db); got != 0 {
		t.Fatalf("known_merchant rows = %d, want 0", got)
	}
}

type storedMapping struct {
	ID         int64
	Merchant   string
	CategoryID int64
	CreatedAt  string
	UpdatedAt  string
}

func loadStoredMapping(t *testing.T, ctx context.Context, db *sql.DB, merchantName string) storedMapping {
	t.Helper()
	var row storedMapping
	if err := db.QueryRowContext(ctx, `
		SELECT id, merchant, category_id, created_at, updated_at
		FROM known_merchants
		WHERE merchant = ? COLLATE NOCASE
	`, merchantName).Scan(&row.ID, &row.Merchant, &row.CategoryID, &row.CreatedAt, &row.UpdatedAt); err != nil {
		t.Fatalf("load mapping %q: %v", merchantName, err)
	}
	return row
}

func freezeTransactionTimestamps(t *testing.T, ctx context.Context, db *sql.DB, id int64, timestamp string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		UPDATE transactions
		SET created_at = ?, updated_at = ?
		WHERE id = ?
	`, timestamp, timestamp, id); err != nil {
		t.Fatalf("freeze transaction timestamps: %v", err)
	}
}

func loadStoredTransaction(t *testing.T, ctx context.Context, db *sql.DB, id int64) storedTransaction {
	t.Helper()
	for _, row := range listStoredTransactions(t, ctx, db) {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("stored transaction %d not found", id)
	return storedTransaction{}
}

func freezeMappingTimestamps(t *testing.T, ctx context.Context, db *sql.DB, id int64, timestamp string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		UPDATE known_merchants
		SET created_at = ?, updated_at = ?
		WHERE id = ?
	`, timestamp, timestamp, id); err != nil {
		t.Fatalf("freeze mapping timestamps: %v", err)
	}
}

type storedBudgetRow struct {
	ID               int64
	CategoryID       int64
	Month            string
	AmountHundredths int64
	CreatedAt        string
	UpdatedAt        string
}

func listStoredBudgets(t *testing.T, ctx context.Context, db *sql.DB) []storedBudgetRow {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
		SELECT id, category_id, month, amount_hundredths, created_at, updated_at
		FROM budgets
		ORDER BY id ASC
	`)
	if err != nil {
		t.Fatalf("query stored budgets: %v", err)
	}
	defer rows.Close()

	stored := make([]storedBudgetRow, 0)
	for rows.Next() {
		var row storedBudgetRow
		if err := rows.Scan(&row.ID, &row.CategoryID, &row.Month, &row.AmountHundredths, &row.CreatedAt, &row.UpdatedAt); err != nil {
			t.Fatalf("scan stored budget: %v", err)
		}
		stored = append(stored, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate stored budgets: %v", err)
	}
	return stored
}

func freezeBudgetTimestamps(t *testing.T, ctx context.Context, db *sql.DB, timestamp string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		UPDATE budgets
		SET created_at = ?, updated_at = ?
	`, timestamp, timestamp); err != nil {
		t.Fatalf("freeze budget timestamps: %v", err)
	}
}

type storedTransaction struct {
	ID               int64
	Merchant         string
	AmountHundredths int64
	Date             string
	CategoryID       int64
	Note             sql.NullString
	CreatedAt        string
	UpdatedAt        string
}

func listStoredTransactions(t *testing.T, ctx context.Context, db *sql.DB) []storedTransaction {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
		SELECT id, merchant, amount_hundredths, date, category_id, note, created_at, updated_at
		FROM transactions
		ORDER BY id ASC
	`)
	if err != nil {
		t.Fatalf("query stored transactions: %v", err)
	}
	defer rows.Close()

	stored := make([]storedTransaction, 0)
	for rows.Next() {
		var row storedTransaction
		if err := rows.Scan(&row.ID, &row.Merchant, &row.AmountHundredths, &row.Date, &row.CategoryID, &row.Note, &row.CreatedAt, &row.UpdatedAt); err != nil {
			t.Fatalf("scan stored transaction: %v", err)
		}
		stored = append(stored, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate stored transactions: %v", err)
	}
	return stored
}

func listStoredMappings(t *testing.T, ctx context.Context, db *sql.DB) []storedMapping {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
		SELECT id, merchant, category_id, created_at, updated_at
		FROM known_merchants
		ORDER BY id ASC
	`)
	if err != nil {
		t.Fatalf("query stored mappings: %v", err)
	}
	defer rows.Close()

	stored := make([]storedMapping, 0)
	for rows.Next() {
		var row storedMapping
		if err := rows.Scan(&row.ID, &row.Merchant, &row.CategoryID, &row.CreatedAt, &row.UpdatedAt); err != nil {
			t.Fatalf("scan stored mapping: %v", err)
		}
		stored = append(stored, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate stored mappings: %v", err)
	}
	return stored
}

func expectExecError(t *testing.T, ctx context.Context, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(ctx, query, args...); err == nil {
		t.Fatalf("expected SQL operation to fail: %s", query)
	}
}
