package database_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/database"
)

func TestMigrateTransactionIdempotencyUpgradePreservesRowsAndConstraints(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	enableForeignKeys(t, db)

	throughImports := migrationSet(map[string]string{
		"001_initial.sql":             readRepoMigration(t, "001_initial.sql"),
		"002_transaction_imports.sql": readRepoMigration(t, "002_transaction_imports.sql"),
	})
	if err := database.MigrateFS(ctx, db, throughImports); err != nil {
		t.Fatalf("MigrateFS through 002: %v", err)
	}

	categoryID := insertCategory(t, ctx, db, "Groceries")
	budgetID := insertBudget(t, ctx, db, categoryID, "2026-08", 50000)
	transactionID := insertTransaction(t, ctx, db, "Metro", 2000, "2026-08-14", categoryID)
	merchantID := insertKnownMerchant(t, ctx, db, "Metro", categoryID)

	withIdempotency := migrationSet(map[string]string{
		"001_initial.sql":                 readRepoMigration(t, "001_initial.sql"),
		"002_transaction_imports.sql":     readRepoMigration(t, "002_transaction_imports.sql"),
		"003_transaction_idempotency.sql": readRepoMigration(t, "003_transaction_idempotency.sql"),
	})
	if err := database.MigrateFS(ctx, db, withIdempotency); err != nil {
		t.Fatalf("MigrateFS(003): %v", err)
	}
	if got := migrationVersion(t, db); got != 3 {
		t.Fatalf("version after 003 = %d, want 3", got)
	}
	if !tableExists(t, db, "transaction_idempotency") {
		t.Fatal("transaction_idempotency was not created")
	}

	var categoryName, merchant string
	if err := db.QueryRowContext(ctx, "SELECT name FROM categories WHERE id = ?", categoryID).Scan(&categoryName); err != nil {
		t.Fatalf("preserved category: %v", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT merchant FROM transactions WHERE id = ?", transactionID).Scan(&merchant); err != nil {
		t.Fatalf("preserved transaction: %v", err)
	}
	if categoryName != "Groceries" || merchant != "Metro" {
		t.Fatalf("preserved rows = (%q, %q)", categoryName, merchant)
	}
	var budgetAmount int64
	if err := db.QueryRowContext(ctx, "SELECT amount_hundredths FROM budgets WHERE id = ?", budgetID).Scan(&budgetAmount); err != nil {
		t.Fatalf("preserved budget: %v", err)
	}
	if budgetAmount != 50000 {
		t.Fatalf("preserved budget = %d, want 50000", budgetAmount)
	}
	var knownMerchant string
	if err := db.QueryRowContext(ctx, "SELECT merchant FROM known_merchants WHERE id = ?", merchantID).Scan(&knownMerchant); err != nil {
		t.Fatalf("preserved known merchant: %v", err)
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO transaction_idempotency (idempotency_key, request_fingerprint, transaction_id, category_source, merchant_mapping_action)
		VALUES (?, ?, ?, ?, ?)
	`, "expense-2026-08-19-001", "fingerprint-a", transactionID, "provided", "created"); err != nil {
		t.Fatalf("insert idempotency row: %v", err)
	}

	expectExecError(t, ctx, db, `
		INSERT INTO transaction_idempotency (idempotency_key, request_fingerprint, transaction_id, category_source, merchant_mapping_action)
		VALUES (?, ?, ?, ?, ?)
	`, "", "fingerprint-b", transactionID, "provided", "created")
	expectExecError(t, ctx, db, `
		INSERT INTO transaction_idempotency (idempotency_key, request_fingerprint, transaction_id, category_source, merchant_mapping_action)
		VALUES (?, ?, ?, ?, ?)
	`, " padded ", "fingerprint-b", transactionID, "provided", "created")
	expectExecError(t, ctx, db, `
		INSERT INTO transaction_idempotency (idempotency_key, request_fingerprint, transaction_id, category_source, merchant_mapping_action)
		VALUES (?, ?, ?, ?, ?)
	`, "expense-2026-08-19-001", "fingerprint-b", transactionID, "provided", "created")
	if _, err := db.ExecContext(ctx, `
		INSERT INTO transaction_idempotency (idempotency_key, request_fingerprint, transaction_id, category_source, merchant_mapping_action)
		VALUES (?, ?, ?, ?, ?)
	`, "Expense-2026-08-19-001", "fingerprint-case", sql.NullInt64{}, "provided", "created"); err != nil {
		t.Fatalf("case-sensitive key insert: %v", err)
	}

	secondID := insertTransaction(t, ctx, db, "No Frills", 500, "2026-08-13", categoryID)
	expectExecError(t, ctx, db, `
		INSERT INTO transaction_idempotency (idempotency_key, request_fingerprint, transaction_id, category_source, merchant_mapping_action)
		VALUES (?, ?, ?, ?, ?)
	`, "expense-other", "fingerprint-c", transactionID, "provided", "matched")
	if _, err := db.ExecContext(ctx, `
		INSERT INTO transaction_idempotency (idempotency_key, request_fingerprint, transaction_id, category_source, merchant_mapping_action)
		VALUES (?, ?, ?, ?, ?)
	`, "expense-second", "fingerprint-d", secondID, "provided", "created"); err != nil {
		t.Fatalf("second idempotency row: %v", err)
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM transactions WHERE id = ?`, transactionID); err != nil {
		t.Fatalf("delete transaction: %v", err)
	}
	var retired sql.NullInt64
	if err := db.QueryRowContext(ctx, `
		SELECT transaction_id FROM transaction_idempotency WHERE idempotency_key = ?
	`, "expense-2026-08-19-001").Scan(&retired); err != nil {
		t.Fatalf("load retired key: %v", err)
	}
	if retired.Valid {
		t.Fatalf("transaction_id after delete = %d, want NULL", retired.Int64)
	}
}
