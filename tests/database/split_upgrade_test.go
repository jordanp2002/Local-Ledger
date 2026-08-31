package database_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/database"
)

func TestMigrateSplitTransactionsPreservesDependentIDsAndForeignKeys(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	enableForeignKeys(t, db)

	throughRecurring := migrationSet(map[string]string{
		"001_initial.sql":                 readRepoMigration(t, "001_initial.sql"),
		"002_transaction_imports.sql":     readRepoMigration(t, "002_transaction_imports.sql"),
		"003_transaction_idempotency.sql": readRepoMigration(t, "003_transaction_idempotency.sql"),
		"004_recurring_transactions.sql":  readRepoMigration(t, "004_recurring_transactions.sql"),
	})
	if err := database.MigrateFS(ctx, db, throughRecurring); err != nil {
		t.Fatalf("MigrateFS through 004: %v", err)
	}
	groceries := insertCategory(t, ctx, db, "Groceries")
	household := insertCategory(t, ctx, db, "Household")
	transactionID := insertTransaction(t, ctx, db, "Costco", 9000, "2026-08-30", groceries)
	if _, err := db.ExecContext(ctx, "INSERT INTO transaction_allocations (transaction_id, category_id, amount_hundredths) VALUES (?, ?, ?)", 999, household, 1); err == nil {
		t.Fatal("transaction_allocations unexpectedly exists before split migration")
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO transaction_idempotency (idempotency_key, request_fingerprint, transaction_id, category_source, merchant_mapping_action)
		VALUES ('split-key', 'fingerprint', ?, 'provided', 'created')
	`, transactionID); err != nil {
		t.Fatalf("insert idempotency record: %v", err)
	}
	importRecord, err := db.ExecContext(ctx, "INSERT INTO transaction_imports (idempotency_key, request_fingerprint) VALUES ('import-key', 'fingerprint')")
	if err != nil {
		t.Fatalf("insert import: %v", err)
	}
	importID, err := importRecord.LastInsertId()
	if err != nil {
		t.Fatalf("import row id: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO transaction_import_items (import_id, item_index, transaction_id, category_source, merchant_mapping_action)
		VALUES (?, 0, ?, 'provided', 'created')
	`, importID, transactionID); err != nil {
		t.Fatalf("insert import item: %v", err)
	}
	recurring, err := db.ExecContext(ctx, `
		INSERT INTO recurring_transactions (merchant, amount_hundredths, category_id, day_of_month)
		VALUES ('Costco', 9000, ?, 30)
	`, household)
	if err != nil {
		t.Fatalf("insert recurring template: %v", err)
	}
	recurringID, err := recurring.LastInsertId()
	if err != nil {
		t.Fatalf("recurring row id: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO recurring_transaction_runs (recurring_transaction_id, month, transaction_id)
		VALUES (?, '2026-08', ?)
	`, recurringID, transactionID); err != nil {
		t.Fatalf("insert recurring run: %v", err)
	}

	allMigrations := migrationSet(map[string]string{
		"001_initial.sql":                 readRepoMigration(t, "001_initial.sql"),
		"002_transaction_imports.sql":     readRepoMigration(t, "002_transaction_imports.sql"),
		"003_transaction_idempotency.sql": readRepoMigration(t, "003_transaction_idempotency.sql"),
		"004_recurring_transactions.sql":  readRepoMigration(t, "004_recurring_transactions.sql"),
		"005_split_transactions.sql":      readRepoMigration(t, "005_split_transactions.sql"),
	})
	if err := database.MigrateFS(ctx, db, allMigrations); err != nil {
		t.Fatalf("MigrateFS through 005: %v", err)
	}
	if got := migrationVersion(t, db); got != 5 {
		t.Fatalf("version = %d, want 5", got)
	}
	var merchant, date string
	if err := db.QueryRowContext(ctx, "SELECT merchant, date FROM transactions WHERE id = ?", transactionID).Scan(&merchant, &date); err != nil {
		t.Fatalf("load preserved transaction: %v", err)
	}
	if merchant != "Costco" || date != "2026-08-30" {
		t.Fatalf("preserved transaction = %q/%q", merchant, date)
	}
	var allocationAmount int64
	if err := db.QueryRowContext(ctx, "SELECT amount_hundredths FROM transaction_allocations WHERE transaction_id = ? AND category_id = ?", transactionID, groceries).Scan(&allocationAmount); err != nil {
		t.Fatalf("load migrated allocation: %v", err)
	}
	if allocationAmount != 9000 {
		t.Fatalf("migrated allocation amount = %d, want 9000", allocationAmount)
	}
	var gotID int64
	if err := db.QueryRowContext(ctx, "SELECT transaction_id FROM transaction_idempotency WHERE idempotency_key = 'split-key'").Scan(&gotID); err != nil {
		t.Fatalf("load preserved idempotency record: %v", err)
	}
	if gotID != transactionID {
		t.Fatalf("idempotency transaction id = %d, want %d", gotID, transactionID)
	}
	if err := db.QueryRowContext(ctx, "SELECT transaction_id FROM transaction_import_items WHERE import_id = ? AND item_index = 0", importID).Scan(&gotID); err != nil {
		t.Fatalf("load preserved import item: %v", err)
	}
	if gotID != transactionID {
		t.Fatalf("import item transaction id = %d, want %d", gotID, transactionID)
	}
	if err := db.QueryRowContext(ctx, "SELECT transaction_id FROM recurring_transaction_runs WHERE recurring_transaction_id = ? AND month = '2026-08'", recurringID).Scan(&gotID); err != nil {
		t.Fatalf("load preserved recurring run: %v", err)
	}
	if gotID != transactionID {
		t.Fatalf("recurring run transaction id = %d, want %d", gotID, transactionID)
	}
	var violations int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM pragma_foreign_key_check").Scan(&violations); err != nil {
		t.Fatalf("foreign key check: %v", err)
	}
	if violations != 0 {
		t.Fatalf("foreign key violations = %d, want 0", violations)
	}
	columns, err := db.QueryContext(ctx, "PRAGMA table_info(transactions)")
	if err != nil {
		t.Fatalf("transactions columns: %v", err)
	}
	defer columns.Close()
	for columns.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := columns.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan transactions column: %v", err)
		}
		if name == "amount_hundredths" || name == "category_id" {
			t.Fatalf("legacy transaction column %q remains", name)
		}
	}
	if err := columns.Err(); err != nil {
		t.Fatalf("iterate transactions columns: %v", err)
	}
}

func TestMigrateSplitTransactionsRollsBackWholeSchemaRebuild(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	enableForeignKeys(t, db)
	throughRecurring := migrationSet(map[string]string{
		"001_initial.sql":                 readRepoMigration(t, "001_initial.sql"),
		"002_transaction_imports.sql":     readRepoMigration(t, "002_transaction_imports.sql"),
		"003_transaction_idempotency.sql": readRepoMigration(t, "003_transaction_idempotency.sql"),
		"004_recurring_transactions.sql":  readRepoMigration(t, "004_recurring_transactions.sql"),
	})
	if err := database.MigrateFS(ctx, db, throughRecurring); err != nil {
		t.Fatalf("MigrateFS through 004: %v", err)
	}
	categoryID := insertCategory(t, ctx, db, "Groceries")
	transactionID := insertTransaction(t, ctx, db, "Metro", 1250, "2026-08-30", categoryID)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO transaction_idempotency (idempotency_key, request_fingerprint, transaction_id, category_source, merchant_mapping_action)
		VALUES ('rollback-key', 'fingerprint', ?, 'provided', 'created')
	`, transactionID); err != nil {
		t.Fatalf("insert rollback idempotency record: %v", err)
	}
	importRecord, err := db.ExecContext(ctx, `
		INSERT INTO transaction_imports (idempotency_key, request_fingerprint)
		VALUES ('rollback-import', 'fingerprint')
	`)
	if err != nil {
		t.Fatalf("insert rollback import: %v", err)
	}
	importID, err := importRecord.LastInsertId()
	if err != nil {
		t.Fatalf("rollback import id: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO transaction_import_items (import_id, item_index, transaction_id, category_source, merchant_mapping_action)
		VALUES (?, 0, ?, 'provided', 'created')
	`, importID, transactionID); err != nil {
		t.Fatalf("insert rollback import item: %v", err)
	}
	recurringRecord, err := db.ExecContext(ctx, `
		INSERT INTO recurring_transactions (merchant, amount_hundredths, category_id, day_of_month)
		VALUES ('Metro', 1250, ?, 30)
	`, categoryID)
	if err != nil {
		t.Fatalf("insert rollback recurring template: %v", err)
	}
	recurringID, err := recurringRecord.LastInsertId()
	if err != nil {
		t.Fatalf("rollback recurring id: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO recurring_transaction_runs (recurring_transaction_id, month, transaction_id)
		VALUES (?, '2026-08', ?)
	`, recurringID, transactionID); err != nil {
		t.Fatalf("insert rollback recurring run: %v", err)
	}

	brokenSplit := readRepoMigration(t, "005_split_transactions.sql") + "\nSELECT no_such_phase5_function();\n"
	allMigrations := migrationSet(map[string]string{
		"001_initial.sql":                 readRepoMigration(t, "001_initial.sql"),
		"002_transaction_imports.sql":     readRepoMigration(t, "002_transaction_imports.sql"),
		"003_transaction_idempotency.sql": readRepoMigration(t, "003_transaction_idempotency.sql"),
		"004_recurring_transactions.sql":  readRepoMigration(t, "004_recurring_transactions.sql"),
		"005_split_transactions.sql":      brokenSplit,
	})
	if err := database.MigrateFS(ctx, db, allMigrations); err == nil {
		t.Fatal("MigrateFS with broken 005 error = nil")
	}
	if got := migrationVersion(t, db); got != 4 {
		t.Fatalf("version after rollback = %d, want 4", got)
	}
	var amount, gotCategoryID int64
	if err := db.QueryRowContext(ctx, `
		SELECT amount_hundredths, category_id
		FROM transactions
		WHERE id = ?
	`, transactionID).Scan(&amount, &gotCategoryID); err != nil {
		t.Fatalf("load legacy transaction after rollback: %v", err)
	}
	if amount != 1250 || gotCategoryID != categoryID {
		t.Fatalf("legacy transaction after rollback = %d/%d, want 1250/%d", amount, gotCategoryID, categoryID)
	}
	var referencedID int64
	if err := db.QueryRowContext(ctx, `SELECT transaction_id FROM transaction_idempotency WHERE idempotency_key = 'rollback-key'`).Scan(&referencedID); err != nil || referencedID != transactionID {
		t.Fatalf("idempotency reference after rollback = %d, error %v, want %d", referencedID, err, transactionID)
	}
	if err := db.QueryRowContext(ctx, `SELECT transaction_id FROM transaction_import_items WHERE import_id = ? AND item_index = 0`, importID).Scan(&referencedID); err != nil || referencedID != transactionID {
		t.Fatalf("import reference after rollback = %d, error %v, want %d", referencedID, err, transactionID)
	}
	if err := db.QueryRowContext(ctx, `SELECT transaction_id FROM recurring_transaction_runs WHERE recurring_transaction_id = ? AND month = '2026-08'`, recurringID).Scan(&referencedID); err != nil || referencedID != transactionID {
		t.Fatalf("recurring reference after rollback = %d, error %v, want %d", referencedID, err, transactionID)
	}
	var marker int
	if err := db.QueryRowContext(ctx, `SELECT 1 FROM transaction_allocations LIMIT 1`).Scan(&marker); err == nil {
		t.Fatal("transaction_allocations exists after rollback")
	}
}
