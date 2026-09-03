package database_test

import (
	"context"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/database"
)

func TestMigrateAccountsUpgradePreservesRows(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	enableForeignKeys(t, db)

	throughSeven := migrationSet(map[string]string{
		"001_initial.sql":                 readRepoMigration(t, "001_initial.sql"),
		"002_transaction_imports.sql":     readRepoMigration(t, "002_transaction_imports.sql"),
		"003_transaction_idempotency.sql": readRepoMigration(t, "003_transaction_idempotency.sql"),
		"004_recurring_transactions.sql":  readRepoMigration(t, "004_recurring_transactions.sql"),
		"005_split_transactions.sql":      readRepoMigration(t, "005_split_transactions.sql"),
		"006_budget_rollovers.sql":        readRepoMigration(t, "006_budget_rollovers.sql"),
		"007_sinking_fund_periods.sql":    readRepoMigration(t, "007_sinking_fund_periods.sql"),
	})
	if err := database.MigrateFS(ctx, db, throughSeven); err != nil {
		t.Fatalf("MigrateFS through 007: %v", err)
	}
	if got := migrationVersion(t, db); got != 7 {
		t.Fatalf("version after 007 = %d, want 7", got)
	}

	categoryID := insertCategory(t, ctx, db, "Groceries")
	insertBudget(t, ctx, db, categoryID, "2026-08", 50000)
	transactionID := insertTransaction(t, ctx, db, "Metro", 1250, "2026-08-14", categoryID)

	throughEight := migrationSet(map[string]string{
		"001_initial.sql":                 readRepoMigration(t, "001_initial.sql"),
		"002_transaction_imports.sql":     readRepoMigration(t, "002_transaction_imports.sql"),
		"003_transaction_idempotency.sql": readRepoMigration(t, "003_transaction_idempotency.sql"),
		"004_recurring_transactions.sql":  readRepoMigration(t, "004_recurring_transactions.sql"),
		"005_split_transactions.sql":      readRepoMigration(t, "005_split_transactions.sql"),
		"006_budget_rollovers.sql":        readRepoMigration(t, "006_budget_rollovers.sql"),
		"007_sinking_fund_periods.sql":    readRepoMigration(t, "007_sinking_fund_periods.sql"),
		"008_accounts.sql":                readRepoMigration(t, "008_accounts.sql"),
	})
	if err := database.MigrateFS(ctx, db, throughEight); err != nil {
		t.Fatalf("MigrateFS through 008: %v", err)
	}
	if got := migrationVersion(t, db); got != 8 {
		t.Fatalf("version after 008 = %d, want 8", got)
	}
	if !tableExists(t, db, "accounts") {
		t.Fatal("accounts was not created")
	}

	var categoryName string
	if err := db.QueryRowContext(ctx, "SELECT name FROM categories WHERE id = ?", categoryID).Scan(&categoryName); err != nil {
		t.Fatalf("preserved category: %v", err)
	}
	if categoryName != "Groceries" {
		t.Fatalf("preserved category = %q", categoryName)
	}
	var merchant string
	if err := db.QueryRowContext(ctx, "SELECT merchant FROM transactions WHERE id = ?", transactionID).Scan(&merchant); err != nil {
		t.Fatalf("preserved transaction: %v", err)
	}
	if merchant != "Metro" {
		t.Fatalf("preserved transaction = %q", merchant)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO accounts (name, type, opening_balance_hundredths) VALUES (?, ?, ?)`, "Checking", "checking", 250000); err != nil {
		t.Fatalf("insert account after upgrade: %v", err)
	}
}
