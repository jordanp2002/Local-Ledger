package database_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/database"
)

func TestMigrateRecurringTransactionsUpgradePreservesRowsAndConstraints(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	enableForeignKeys(t, db)

	throughIdempotency := migrationSet(map[string]string{
		"001_initial.sql":                 readRepoMigration(t, "001_initial.sql"),
		"002_transaction_imports.sql":     readRepoMigration(t, "002_transaction_imports.sql"),
		"003_transaction_idempotency.sql": readRepoMigration(t, "003_transaction_idempotency.sql"),
	})
	if err := database.MigrateFS(ctx, db, throughIdempotency); err != nil {
		t.Fatalf("MigrateFS through 003: %v", err)
	}
	if got := migrationVersion(t, db); got != 3 {
		t.Fatalf("version after 003 = %d, want 3", got)
	}

	categoryID := insertCategory(t, ctx, db, "Entertainment")
	budgetID := insertBudget(t, ctx, db, categoryID, "2026-08", 5000)
	transactionID := insertTransaction(t, ctx, db, "Netflix", 2299, "2026-08-15", categoryID)
	merchantID := insertKnownMerchant(t, ctx, db, "Netflix", categoryID)

	withRecurring := migrationSet(map[string]string{
		"001_initial.sql":                 readRepoMigration(t, "001_initial.sql"),
		"002_transaction_imports.sql":     readRepoMigration(t, "002_transaction_imports.sql"),
		"003_transaction_idempotency.sql": readRepoMigration(t, "003_transaction_idempotency.sql"),
		"004_recurring_transactions.sql":  readRepoMigration(t, "004_recurring_transactions.sql"),
	})
	if err := database.MigrateFS(ctx, db, withRecurring); err != nil {
		t.Fatalf("MigrateFS(004): %v", err)
	}
	if got := migrationVersion(t, db); got != 4 {
		t.Fatalf("version after 004 = %d, want 4", got)
	}
	if !tableExists(t, db, "recurring_transactions") || !tableExists(t, db, "recurring_transaction_runs") {
		t.Fatal("recurring tables were not created")
	}

	// Verify preservation of existing records across upgrade
	var categoryName, merchant string
	if err := db.QueryRowContext(ctx, "SELECT name FROM categories WHERE id = ?", categoryID).Scan(&categoryName); err != nil {
		t.Fatalf("preserved category: %v", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT merchant FROM transactions WHERE id = ?", transactionID).Scan(&merchant); err != nil {
		t.Fatalf("preserved transaction: %v", err)
	}
	if categoryName != "Entertainment" || merchant != "Netflix" {
		t.Fatalf("preserved rows = (%q, %q)", categoryName, merchant)
	}
	var budgetAmount int64
	if err := db.QueryRowContext(ctx, "SELECT amount_hundredths FROM budgets WHERE id = ?", budgetID).Scan(&budgetAmount); err != nil {
		t.Fatalf("preserved budget: %v", err)
	}
	if budgetAmount != 5000 {
		t.Fatalf("preserved budget = %d, want 5000", budgetAmount)
	}
	var knownMerchant string
	if err := db.QueryRowContext(ctx, "SELECT merchant FROM known_merchants WHERE id = ?", merchantID).Scan(&knownMerchant); err != nil {
		t.Fatalf("preserved known merchant: %v", err)
	}
	if knownMerchant != "Netflix" {
		t.Fatalf("preserved known merchant = %q, want Netflix", knownMerchant)
	}

	// Valid recurring template creation and default checks
	result, err := db.ExecContext(ctx, `
		INSERT INTO recurring_transactions (merchant, amount_hundredths, category_id, day_of_month, note)
		VALUES (?, ?, ?, ?, ?)
	`, "Netflix", 2299, categoryID, 15, "Monthly subscription")
	if err != nil {
		t.Fatalf("insert recurring transaction: %v", err)
	}
	tmplID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	assertPositiveIDAndUTCDefaults(t, ctx, db, "recurring_transactions", tmplID)

	var active int64
	if err := db.QueryRowContext(ctx, "SELECT active FROM recurring_transactions WHERE id = ?", tmplID).Scan(&active); err != nil {
		t.Fatalf("query recurring active default: %v", err)
	}
	if active != 1 {
		t.Fatalf("recurring active default = %d, want 1", active)
	}

	assertAmountStorageType(t, ctx, db, "recurring_transactions", tmplID, "integer")

	// Table constraints on recurring_transactions
	// Empty or whitespace merchant
	expectExecError(t, ctx, db, `
		INSERT INTO recurring_transactions (merchant, amount_hundredths, category_id, day_of_month)
		VALUES (?, ?, ?, ?)
	`, "", 1000, categoryID, 1)
	expectExecError(t, ctx, db, `
		INSERT INTO recurring_transactions (merchant, amount_hundredths, category_id, day_of_month)
		VALUES (?, ?, ?, ?)
	`, "  ", 1000, categoryID, 1)
	expectExecError(t, ctx, db, `
		INSERT INTO recurring_transactions (merchant, amount_hundredths, category_id, day_of_month)
		VALUES (?, ?, ?, ?)
	`, " Netflix ", 1000, categoryID, 1)

	// Non-positive or non-integer amount
	expectExecError(t, ctx, db, `
		INSERT INTO recurring_transactions (merchant, amount_hundredths, category_id, day_of_month)
		VALUES (?, ?, ?, ?)
	`, "Spotify", 0, categoryID, 1)
	expectExecError(t, ctx, db, `
		INSERT INTO recurring_transactions (merchant, amount_hundredths, category_id, day_of_month)
		VALUES (?, ?, ?, ?)
	`, "Spotify", -100, categoryID, 1)
	expectExecError(t, ctx, db, `
		INSERT INTO recurring_transactions (merchant, amount_hundredths, category_id, day_of_month)
		VALUES (?, CAST(? AS REAL), ?, ?)
	`, "Spotify", 10.5, categoryID, 1)

	// Invalid day_of_month (< 1 or > 31)
	expectExecError(t, ctx, db, `
		INSERT INTO recurring_transactions (merchant, amount_hundredths, category_id, day_of_month)
		VALUES (?, ?, ?, ?)
	`, "Spotify", 1000, categoryID, 0)
	expectExecError(t, ctx, db, `
		INSERT INTO recurring_transactions (merchant, amount_hundredths, category_id, day_of_month)
		VALUES (?, ?, ?, ?)
	`, "Spotify", 1000, categoryID, 32)
	expectExecError(t, ctx, db, `
		INSERT INTO recurring_transactions (merchant, amount_hundredths, category_id, day_of_month)
		VALUES (?, ?, ?, ?)
	`, "Spotify", 1000, categoryID, -5)

	// Invalid foreign key for category_id
	expectExecError(t, ctx, db, `
		INSERT INTO recurring_transactions (merchant, amount_hundredths, category_id, day_of_month)
		VALUES (?, ?, ?, ?)
	`, "Spotify", 1000, int64(999999), 1)

	// Invalid active flag
	expectExecError(t, ctx, db, `
		INSERT INTO recurring_transactions (merchant, amount_hundredths, category_id, day_of_month, active)
		VALUES (?, ?, ?, ?, ?)
	`, "Spotify", 1000, categoryID, 1, 2)
	expectExecError(t, ctx, db, `
		INSERT INTO recurring_transactions (merchant, amount_hundredths, category_id, day_of_month, active)
		VALUES (?, ?, ?, ?, ?)
	`, "Spotify", 1000, categoryID, 1, -1)

	// Identical template is allowed
	if _, err := db.ExecContext(ctx, `
		INSERT INTO recurring_transactions (merchant, amount_hundredths, category_id, day_of_month, note)
		VALUES (?, ?, ?, ?, ?)
	`, "Netflix", 2299, categoryID, 15, "Monthly subscription"); err != nil {
		t.Fatalf("identical template should be allowed: %v", err)
	}

	// Constraints on recurring_transaction_runs
	// Valid insertion
	if _, err := db.ExecContext(ctx, `
		INSERT INTO recurring_transaction_runs (recurring_transaction_id, month, transaction_id)
		VALUES (?, ?, ?)
	`, tmplID, "2026-08", transactionID); err != nil {
		t.Fatalf("insert recurring run: %v", err)
	}

	// Duplicate run for same (recurring_transaction_id, month) is rejected
	expectExecError(t, ctx, db, `
		INSERT INTO recurring_transaction_runs (recurring_transaction_id, month, transaction_id)
		VALUES (?, ?, ?)
	`, tmplID, "2026-08", transactionID)

	// Invalid month format
	expectExecError(t, ctx, db, `
		INSERT INTO recurring_transaction_runs (recurring_transaction_id, month, transaction_id)
		VALUES (?, ?, ?)
	`, tmplID, "2026-8", transactionID)
	expectExecError(t, ctx, db, `
		INSERT INTO recurring_transaction_runs (recurring_transaction_id, month, transaction_id)
		VALUES (?, ?, ?)
	`, tmplID, "2026-13", transactionID)

	// Foreign key: non-existent recurring_transaction_id
	expectExecError(t, ctx, db, `
		INSERT INTO recurring_transaction_runs (recurring_transaction_id, month, transaction_id)
		VALUES (?, ?, ?)
	`, int64(999999), "2026-09", transactionID)

	// Foreign key: delete transaction sets transaction_id to NULL
	if _, err := db.ExecContext(ctx, `DELETE FROM transactions WHERE id = ?`, transactionID); err != nil {
		t.Fatalf("delete transaction: %v", err)
	}
	var runTxID sql.NullInt64
	if err := db.QueryRowContext(ctx, `
		SELECT transaction_id FROM recurring_transaction_runs WHERE recurring_transaction_id = ? AND month = ?
	`, tmplID, "2026-08").Scan(&runTxID); err != nil {
		t.Fatalf("load run after transaction deletion: %v", err)
	}
	if runTxID.Valid {
		t.Fatalf("transaction_id after delete = %d, want NULL", runTxID.Int64)
	}

	// Foreign key on recurring_transactions: cannot delete template with runs (RESTRICT)
	expectExecError(t, ctx, db, `DELETE FROM recurring_transactions WHERE id = ?`, tmplID)
}
