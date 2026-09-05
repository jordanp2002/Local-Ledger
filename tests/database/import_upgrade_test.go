package database_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/jordanp2002/Local-Ledger/internal/database"
)

func TestMigrateTransactionImportsUpgradePreservesRowsAndConstraints(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	enableForeignKeys(t, db)

	initial := migrationSet(map[string]string{
		"001_initial.sql": readRepoMigration(t, "001_initial.sql"),
	})
	if err := database.MigrateFS(ctx, db, initial); err != nil {
		t.Fatalf("MigrateFS(001) error = %v", err)
	}
	if got := migrationVersion(t, db); got != 1 {
		t.Fatalf("version after 001 = %d, want 1", got)
	}

	categoryID := insertCategory(t, ctx, db, "Groceries")
	budgetID := insertBudget(t, ctx, db, categoryID, "2026-08", 50000)
	transactionID := insertTransaction(t, ctx, db, "Metro", 2418, "2026-08-18", categoryID)
	merchantID := insertKnownMerchant(t, ctx, db, "Metro", categoryID)

	upgraded := migrationSet(map[string]string{
		"001_initial.sql":             readRepoMigration(t, "001_initial.sql"),
		"002_transaction_imports.sql": readRepoMigration(t, "002_transaction_imports.sql"),
	})
	if err := database.MigrateFS(ctx, db, upgraded); err != nil {
		t.Fatalf("MigrateFS(002) error = %v", err)
	}
	if got := migrationVersion(t, db); got != 2 {
		t.Fatalf("version after 002 = %d, want 2", got)
	}

	var categoryName string
	if err := db.QueryRowContext(ctx, "SELECT name FROM categories WHERE id = ?", categoryID).Scan(&categoryName); err != nil {
		t.Fatalf("preserved category: %v", err)
	}
	if categoryName != "Groceries" {
		t.Fatalf("preserved category = %q, want Groceries", categoryName)
	}

	var budgetAmount int64
	if err := db.QueryRowContext(ctx, "SELECT amount_hundredths FROM budgets WHERE id = ?", budgetID).Scan(&budgetAmount); err != nil {
		t.Fatalf("preserved budget: %v", err)
	}
	if budgetAmount != 50000 {
		t.Fatalf("preserved budget amount = %d, want 50000", budgetAmount)
	}

	var merchant string
	if err := db.QueryRowContext(ctx, "SELECT merchant FROM transactions WHERE id = ?", transactionID).Scan(&merchant); err != nil {
		t.Fatalf("preserved transaction: %v", err)
	}
	if merchant != "Metro" {
		t.Fatalf("preserved transaction merchant = %q, want Metro", merchant)
	}

	var knownMerchant string
	if err := db.QueryRowContext(ctx, "SELECT merchant FROM known_merchants WHERE id = ?", merchantID).Scan(&knownMerchant); err != nil {
		t.Fatalf("preserved known merchant: %v", err)
	}
	if knownMerchant != "Metro" {
		t.Fatalf("preserved known merchant = %q, want Metro", knownMerchant)
	}

	if !tableExists(t, db, "transaction_imports") || !tableExists(t, db, "transaction_import_items") {
		t.Fatal("import tables were not created")
	}

	result, err := db.ExecContext(ctx, `
		INSERT INTO transaction_imports (idempotency_key, request_fingerprint)
		VALUES (?, ?)
	`, "statement-2026-08-19-page-1", "fingerprint-a")
	if err != nil {
		t.Fatalf("insert import: %v", err)
	}
	importID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("import id: %v", err)
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO transaction_import_items (import_id, item_index, transaction_id, category_source, merchant_mapping_action)
		VALUES (?, ?, ?, ?, ?)
	`, importID, 0, transactionID, "provided", "created"); err != nil {
		t.Fatalf("insert import item: %v", err)
	}

	expectExecError(t, ctx, db, `
		INSERT INTO transaction_imports (idempotency_key, request_fingerprint)
		VALUES (?, ?)
	`, "", "fingerprint-b")
	expectExecError(t, ctx, db, `
		INSERT INTO transaction_imports (idempotency_key, request_fingerprint)
		VALUES (?, ?)
	`, " padded ", "fingerprint-b")
	expectExecError(t, ctx, db, `
		INSERT INTO transaction_imports (idempotency_key, request_fingerprint)
		VALUES (?, ?)
	`, "statement-2026-08-19-page-1", "fingerprint-b")
	if _, err := db.ExecContext(ctx, `
		INSERT INTO transaction_imports (idempotency_key, request_fingerprint)
		VALUES (?, ?)
	`, "Statement-2026-08-19-page-1", "fingerprint-case"); err != nil {
		t.Fatalf("case-sensitive key insert: %v", err)
	}

	expectExecError(t, ctx, db, `
		INSERT INTO transaction_import_items (import_id, item_index, transaction_id, category_source, merchant_mapping_action)
		VALUES (?, ?, ?, ?, ?)
	`, importID, 0, transactionID, "provided", "created")
	expectExecError(t, ctx, db, `
		INSERT INTO transaction_import_items (import_id, item_index, transaction_id, category_source, merchant_mapping_action)
		VALUES (?, ?, ?, ?, ?)
	`, importID, -1, transactionID, "provided", "created")
	expectExecError(t, ctx, db, `
		INSERT INTO transaction_import_items (import_id, item_index, transaction_id, category_source, merchant_mapping_action)
		VALUES (?, ?, ?, ?, ?)
	`, int64(999999), 1, transactionID, "provided", "created")
	expectExecError(t, ctx, db, `
		INSERT INTO transaction_import_items (import_id, item_index, transaction_id, category_source, merchant_mapping_action)
		VALUES (?, ?, ?, ?, ?)
	`, importID, 1, int64(999999), "provided", "created")
	expectExecError(t, ctx, db, `
		INSERT INTO transaction_import_items (import_id, item_index, transaction_id, category_source, merchant_mapping_action)
		VALUES (?, ?, ?, ?, ?)
	`, importID, 2, transactionID, "provided", "matched")

	if _, err := db.ExecContext(ctx, `DELETE FROM transactions WHERE id = ?`, transactionID); err != nil {
		t.Fatalf("delete imported transaction: %v", err)
	}
	var retired sql.NullInt64
	if err := db.QueryRowContext(ctx, `
		SELECT transaction_id
		FROM transaction_import_items
		WHERE import_id = ? AND item_index = 0
	`, importID).Scan(&retired); err != nil {
		t.Fatalf("load retired item: %v", err)
	}
	if retired.Valid {
		t.Fatalf("transaction_id after delete = %v, want NULL", retired.Int64)
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO transaction_import_items (import_id, item_index, transaction_id, category_source, merchant_mapping_action)
		VALUES (?, ?, ?, ?, ?)
	`, importID, 3, nil, "provided", "created"); err != nil {
		t.Fatalf("second null transaction_id item: %v", err)
	}
}

func readRepoMigration(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "internal", "database", "migrations", name)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %q: %v", name, err)
	}
	return string(contents)
}

func enableForeignKeys(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign_keys: %v", err)
	}
	var enabled int64
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if enabled != 1 {
		t.Fatalf("foreign_keys = %d, want 1", enabled)
	}
}
