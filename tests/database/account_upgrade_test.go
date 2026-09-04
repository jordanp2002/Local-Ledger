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

	throughNine := migrationSet(map[string]string{
		"001_initial.sql":                 readRepoMigration(t, "001_initial.sql"),
		"002_transaction_imports.sql":     readRepoMigration(t, "002_transaction_imports.sql"),
		"003_transaction_idempotency.sql": readRepoMigration(t, "003_transaction_idempotency.sql"),
		"004_recurring_transactions.sql":  readRepoMigration(t, "004_recurring_transactions.sql"),
		"005_split_transactions.sql":      readRepoMigration(t, "005_split_transactions.sql"),
		"006_budget_rollovers.sql":        readRepoMigration(t, "006_budget_rollovers.sql"),
		"007_sinking_fund_periods.sql":    readRepoMigration(t, "007_sinking_fund_periods.sql"),
		"008_accounts.sql":                readRepoMigration(t, "008_accounts.sql"),
		"009_account_entries.sql":         readRepoMigration(t, "009_account_entries.sql"),
	})
	if err := database.MigrateFS(ctx, db, throughNine); err != nil {
		t.Fatalf("MigrateFS through 009: %v", err)
	}
	if got := migrationVersion(t, db); got != 9 {
		t.Fatalf("version after 009 = %d, want 9", got)
	}
	for _, table := range []string{"account_entries", "account_reconcile_noops"} {
		if !tableExists(t, db, table) {
			t.Fatalf("%s was not created", table)
		}
	}

	var preservedBalance int64
	if err := db.QueryRowContext(ctx, "SELECT opening_balance_hundredths FROM accounts WHERE name = ?", "Checking").Scan(&preservedBalance); err != nil {
		t.Fatalf("preserved account: %v", err)
	}
	if preservedBalance != 250000 {
		t.Fatalf("preserved account balance = %d, want 250000", preservedBalance)
	}

	var accountID int64
	if err := db.QueryRowContext(ctx, "SELECT id FROM accounts WHERE name = ?", "Checking").Scan(&accountID); err != nil {
		t.Fatalf("account id: %v", err)
	}
	res, err := db.ExecContext(ctx, `INSERT INTO account_entries (account_id, kind, delta_hundredths, date, idempotency_key, fingerprint) VALUES (?, 'deposit', 1000, '2026-08-14', 'upgrade-key', 'upgrade-fp')`, accountID)
	if err != nil {
		t.Fatalf("insert entry after upgrade: %v", err)
	}
	entryID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("entry id: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO account_entries (account_id, kind, delta_hundredths, date, idempotency_key, fingerprint, reversal_of_entry_id) VALUES (?, 'reversal', -1000, '2026-08-14', 'upgrade-rv', 'upgrade-rv-fp', ?)`, accountID, entryID); err != nil {
		t.Fatalf("insert reversal after upgrade: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO account_entries (account_id, kind, delta_hundredths, date, idempotency_key, fingerprint, reversal_of_entry_id) VALUES (?, 'reversal', -1000, '2026-08-14', 'upgrade-rv2', 'upgrade-rv2-fp', ?)`, accountID, entryID); err == nil {
		t.Fatal("second reversal for one entry was not rejected")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO account_entries (account_id, kind, delta_hundredths, date, idempotency_key, fingerprint) VALUES (?, 'deposit', 0, '2026-08-14', 'upgrade-zero', 'upgrade-zero-fp')`, accountID); err == nil {
		t.Fatal("zero delta was not rejected")
	}
}
