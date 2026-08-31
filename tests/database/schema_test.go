package database_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/database"
)

func TestSchemaTablesIndexesAndForeignKeys(t *testing.T) {
	ctx := context.Background()
	db, _ := openSchemaDatabase(t)

	tables, err := schemaTables(ctx, db)
	if err != nil {
		t.Fatalf("query schema tables: %v", err)
	}
	wantTables := []string{"budgets", "categories", "known_merchants", "recurring_transaction_runs", "recurring_transactions", "transaction_allocations", "transaction_idempotency", "transaction_import_items", "transaction_imports", "transactions"}
	if strings.Join(tables, ",") != strings.Join(wantTables, ",") {
		t.Fatalf("schema tables = %v, want exactly %v", tables, wantTables)
	}
	for _, table := range wantTables {
		var rowCount int
		query := fmt.Sprintf("SELECT count(*) FROM %s", table)
		if err := db.QueryRowContext(ctx, query).Scan(&rowCount); err != nil {
			t.Fatalf("query initial %s row count: %v", table, err)
		}
		if rowCount != 0 {
			t.Fatalf("initial %s row count = %d, want no seed rows", table, rowCount)
		}
	}

	var foreignKeysEnabled int64
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeysEnabled); err != nil {
		t.Fatalf("query foreign_keys pragma: %v", err)
	}
	if foreignKeysEnabled != 1 {
		t.Fatalf("foreign_keys pragma = %d, want 1", foreignKeysEnabled)
	}

	indexes := make(map[string][]schemaIndex, len(wantTables))
	for _, table := range wantTables {
		indexes[table], err = schemaIndexes(ctx, db, table)
		if err != nil {
			t.Fatalf("query indexes for %s: %v", table, err)
		}
	}

	if countNonConstraintIndexes(indexes["categories"]) != 0 {
		t.Fatalf("categories has a speculative non-constraint index: %v", indexes["categories"])
	}
	if countNonConstraintIndexes(indexes["budgets"]) != 1 {
		t.Fatalf("budgets non-constraint index count = %d, want 1", countNonConstraintIndexes(indexes["budgets"]))
	}
	if !hasIndexSignature(indexes["budgets"], schemaIndexColumn{name: "month"}) {
		t.Fatalf("budgets is missing its month query index: %v", indexes["budgets"])
	}
	if countNonConstraintIndexes(indexes["transactions"]) != 1 {
		t.Fatalf("transactions non-constraint index count = %d, want 1", countNonConstraintIndexes(indexes["transactions"]))
	}
	if !hasIndexSignature(indexes["transactions"], schemaIndexColumn{name: "date", descending: true}, schemaIndexColumn{name: "id", descending: true}) {
		t.Fatalf("transactions is missing its date/id query index: %v", indexes["transactions"])
	}
	if countNonConstraintIndexes(indexes["transaction_allocations"]) != 1 || !hasIndexSignature(indexes["transaction_allocations"], schemaIndexColumn{name: "category_id"}, schemaIndexColumn{name: "transaction_id"}) {
		t.Fatalf("transaction_allocations is missing its category/transaction query index: %v", indexes["transaction_allocations"])
	}
	if countNonConstraintIndexes(indexes["known_merchants"]) != 0 {
		t.Fatalf("known_merchants has a speculative non-constraint index: %v", indexes["known_merchants"])
	}
	if countNonConstraintIndexes(indexes["recurring_transactions"]) != 0 {
		t.Fatalf("recurring_transactions has a speculative non-constraint index: %v", indexes["recurring_transactions"])
	}
	if countNonConstraintIndexes(indexes["recurring_transaction_runs"]) != 0 {
		t.Fatalf("recurring_transaction_runs has a speculative non-constraint index: %v", indexes["recurring_transaction_runs"])
	}
	if countNonConstraintIndexes(indexes["transaction_imports"]) != 0 {
		t.Fatalf("transaction_imports has a speculative non-constraint index: %v", indexes["transaction_imports"])
	}
	if countNonConstraintIndexes(indexes["transaction_import_items"]) != 0 {
		t.Fatalf("transaction_import_items has a speculative non-constraint index: %v", indexes["transaction_import_items"])
	}
	if countNonConstraintIndexes(indexes["transaction_idempotency"]) != 0 {
		t.Fatalf("transaction_idempotency has a speculative non-constraint index: %v", indexes["transaction_idempotency"])
	}
	for _, index := range indexes["categories"] {
		if indexHasColumn(index, "active") {
			t.Fatalf("categories has a separate active-category index: %q", index.name)
		}
	}
	for _, index := range indexes["known_merchants"] {
		if indexHasColumn(index, "category_id") {
			t.Fatalf("known_merchants has a separate category_id index: %q", index.name)
		}
	}

	wantForeignKeys := map[string]bool{
		"categories":                 false,
		"budgets":                    true,
		"transactions":               false,
		"known_merchants":            true,
		"recurring_transactions":     true,
		"recurring_transaction_runs": true,
		"transaction_allocations":    true,
		"transaction_imports":        false,
		"transaction_import_items":   true,
		"transaction_idempotency":    true,
	}
	for _, table := range wantTables {
		foreignKeys, err := schemaForeignKeys(ctx, db, table)
		if err != nil {
			t.Fatalf("query foreign keys for %s: %v", table, err)
		}
		if table == "transaction_import_items" {
			assertImportItemForeignKeys(t, foreignKeys)
			continue
		}
		if table == "transaction_idempotency" {
			assertIdempotencyForeignKeys(t, foreignKeys)
			continue
		}
		if table == "recurring_transaction_runs" {
			assertRecurringRunForeignKeys(t, foreignKeys)
			continue
		}
		if table == "transaction_allocations" {
			assertAllocationForeignKeys(t, foreignKeys)
			continue
		}
		if !wantForeignKeys[table] {
			if len(foreignKeys) != 0 {
				t.Fatalf("%s foreign keys = %v, want none", table, foreignKeys)
			}
			continue
		}
		if len(foreignKeys) != 1 {
			t.Fatalf("%s foreign keys = %v, want exactly one", table, foreignKeys)
		}
		foreignKey := foreignKeys[0]
		if foreignKey.table != "categories" || foreignKey.from != "category_id" || foreignKey.to != "id" {
			t.Fatalf("%s foreign key = %v, want category_id -> categories(id)", table, foreignKey)
		}
		if foreignKey.onDelete != "RESTRICT" && foreignKey.onDelete != "NO ACTION" {
			t.Fatalf("%s foreign-key delete action = %q, want restrictive RESTRICT or NO ACTION", table, foreignKey.onDelete)
		}
	}
}

func TestSchemaAcceptsValidRowsAndAppliesDefaults(t *testing.T) {
	ctx := context.Background()
	db, _ := openSchemaDatabase(t)

	categoryID := insertCategory(t, ctx, db, "Groceries")
	budgetID := insertBudget(t, ctx, db, categoryID, "2026-08", 50000)
	transactionID := insertTransaction(t, ctx, db, "Metro", 1250, "2026-08-14", categoryID)
	merchantID := insertKnownMerchant(t, ctx, db, "Metro", categoryID)

	for _, record := range []struct {
		table string
		id    int64
	}{
		{table: "categories", id: categoryID},
		{table: "budgets", id: budgetID},
		{table: "transactions", id: transactionID},
		{table: "known_merchants", id: merchantID},
	} {
		assertPositiveIDAndUTCDefaults(t, ctx, db, record.table, record.id)
	}

	var active int64
	if err := db.QueryRowContext(ctx, "SELECT active FROM categories WHERE id = ?", categoryID).Scan(&active); err != nil {
		t.Fatalf("query category active default: %v", err)
	}
	if active != 1 {
		t.Fatalf("category active default = %d, want 1", active)
	}

	for _, record := range []struct {
		table string
		id    int64
	}{
		{table: "budgets", id: budgetID},
		{table: "transaction_allocations", id: transactionID},
	} {
		var storageType string
		query := "SELECT typeof(amount_hundredths) FROM " + record.table
		var queryID int64
		if record.table == "transaction_allocations" {
			query += " WHERE transaction_id = ?"
			queryID = record.id
		} else {
			query += " WHERE id = ?"
			queryID = record.id
		}
		if err := db.QueryRowContext(ctx, query, queryID).Scan(&storageType); err != nil {
			t.Fatalf("query %s amount storage type: %v", record.table, err)
		}
		if storageType != "integer" {
			t.Fatalf("%s amount storage type = %q, want integer", record.table, storageType)
		}
	}
}

func TestSchemaRejectsNormalizedDuplicates(t *testing.T) {
	ctx := context.Background()
	db, _ := openSchemaDatabase(t)

	categoryID := insertCategory(t, ctx, db, "Groceries")
	expectExecError(t, ctx, db, "INSERT INTO categories (name) VALUES (?)", "gROCERIES")

	insertKnownMerchant(t, ctx, db, "Metro", categoryID)
	expectExecError(t, ctx, db, "INSERT INTO known_merchants (merchant, category_id) VALUES (?, ?)", "metro", categoryID)

	insertBudget(t, ctx, db, categoryID, "2026-08", 50000)
	expectExecError(t, ctx, db, "INSERT INTO budgets (category_id, month, amount_hundredths) VALUES (?, ?, ?)", categoryID, "2026-08", 75000)
}

func TestSchemaRejectsNonCanonicalCalendarKeys(t *testing.T) {
	ctx := context.Background()
	db, _ := openSchemaDatabase(t)
	categoryID := insertCategory(t, ctx, db, "Groceries")

	for _, month := range []string{"2026-8", "2026/08", "2026-00", "2026-13"} {
		expectExecError(t, ctx, db, "INSERT INTO budgets (category_id, month, amount_hundredths) VALUES (?, ?, ?)", categoryID, month, 50000)
	}

	insertBudget(t, ctx, db, categoryID, "2026-08", 50000)
	expectExecError(t, ctx, db, "INSERT INTO budgets (category_id, month, amount_hundredths) VALUES (?, ?, ?)", categoryID, "2026-8", 75000)

	for _, date := range []string{"08/14/2026", "2026-8-14", "2026-02-29", "2026-04-31", "2026-00-01", "2026-01-00"} {
		expectExecError(t, ctx, db, "INSERT INTO transactions (merchant, amount_hundredths, date, category_id) VALUES (?, ?, ?, ?)", "Metro", 100, date, categoryID)
	}

	insertTransaction(t, ctx, db, "Leap Day", 100, "2024-02-29", categoryID)
}

func TestSchemaRejectsInvalidCategoryReferences(t *testing.T) {
	ctx := context.Background()
	db, _ := openSchemaDatabase(t)

	const missingCategoryID int64 = 999999
	expectExecError(t, ctx, db, "INSERT INTO budgets (category_id, month, amount_hundredths) VALUES (?, ?, ?)", missingCategoryID, "2026-08", 100)
	expectExecError(t, ctx, db, "INSERT INTO transactions (merchant, amount_hundredths, date, category_id) VALUES (?, ?, ?, ?)", "Metro", 100, "2026-08-14", missingCategoryID)
	expectExecError(t, ctx, db, "INSERT INTO known_merchants (merchant, category_id) VALUES (?, ?)", "Metro", missingCategoryID)
}

func TestSchemaRejectsInvalidNamesAndActiveFlags(t *testing.T) {
	ctx := context.Background()
	db, _ := openSchemaDatabase(t)

	for _, name := range []string{"", " ", "   ", " Groceries", "Groceries ", " Dining "} {
		expectExecError(t, ctx, db, "INSERT INTO categories (name) VALUES (?)", name)
	}
	for active, name := range map[int64]string{2: "InvalidActiveTwo", -1: "InvalidActiveNegative"} {
		expectExecError(t, ctx, db, "INSERT INTO categories (name, active) VALUES (?, ?)", name, active)
	}

	categoryID := insertCategory(t, ctx, db, "Groceries")
	for _, merchant := range []string{"", " ", "   ", " Metro", "Metro ", " Main Street "} {
		expectExecError(t, ctx, db, "INSERT INTO transactions (merchant, amount_hundredths, date, category_id) VALUES (?, ?, ?, ?)", merchant, 100, "2026-08-14", categoryID)
		expectExecError(t, ctx, db, "INSERT INTO known_merchants (merchant, category_id) VALUES (?, ?)", merchant, categoryID)
	}
}

func TestSchemaEnforcesAmountStorageAndSign(t *testing.T) {
	ctx := context.Background()
	db, _ := openSchemaDatabase(t)
	categoryID := insertCategory(t, ctx, db, "Groceries")

	zeroBudgetID := insertBudget(t, ctx, db, categoryID, "2026-08", 0)
	assertAmountStorageType(t, ctx, db, "budgets", zeroBudgetID, "integer")
	expectExecError(t, ctx, db, "INSERT INTO budgets (category_id, month, amount_hundredths) VALUES (?, ?, ?)", categoryID, "2026-09", -1)
	expectExecError(t, ctx, db, "INSERT INTO budgets (category_id, month, amount_hundredths) VALUES (?, ?, CAST(? AS REAL))", categoryID, "2026-10", 12.5)
	expectExecError(t, ctx, db, "INSERT INTO budgets (category_id, month, amount_hundredths) VALUES (?, ?, CAST(? AS TEXT))", categoryID, "2026-11", "12.5")
	expectExecError(t, ctx, db, "INSERT INTO budgets (category_id, month, amount_hundredths) VALUES (?, ?, CAST(? AS TEXT))", categoryID, "2026-12", "not-an-integer")

	positiveTransactionID := insertTransaction(t, ctx, db, "Metro", 1, "2026-08-14", categoryID)
	assertAmountStorageType(t, ctx, db, "transaction_allocations", positiveTransactionID, "integer")
	for _, value := range []struct {
		name  string
		query string
		arg   any
	}{
		{name: "zero", query: "?", arg: 0},
		{name: "negative", query: "?", arg: -1},
		{name: "real", query: "CAST(? AS REAL)", arg: 12.5},
		{name: "text decimal", query: "CAST(? AS TEXT)", arg: "12.5"},
		{name: "text non-integer", query: "CAST(? AS TEXT)", arg: "not-an-integer"},
	} {
		result, err := db.ExecContext(ctx, "INSERT INTO transactions (merchant, date) VALUES (?, ?)", value.name, "2026-08-14")
		if err != nil {
			t.Fatalf("insert %s transaction: %v", value.name, err)
		}
		id := lastInsertID(t, result, value.name+" transaction")
		expectExecError(t, ctx, db, "INSERT INTO transaction_allocations (transaction_id, category_id, amount_hundredths) VALUES (?, ?, "+value.query+")", id, categoryID, value.arg)
	}
}

func TestSchemaReopenPreservesRowsAndMigrationVersion(t *testing.T) {
	ctx := context.Background()
	db, path := openSchemaDatabase(t)
	categoryID := insertCategory(t, ctx, db, "Groceries")
	budgetID := insertBudget(t, ctx, db, categoryID, "2026-08", 50000)
	transactionID := insertTransaction(t, ctx, db, "Metro", 1250, "2026-08-14", categoryID)
	merchantID := insertKnownMerchant(t, ctx, db, "Metro", categoryID)

	if err := db.Close(); err != nil {
		t.Fatalf("close original database: %v", err)
	}

	reopened, err := database.Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("close reopened database: %v", err)
		}
	})

	var version int64
	if err := reopened.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("query reopened user_version: %v", err)
	}
	if version != 5 {
		t.Fatalf("reopened user_version = %d, want 5", version)
	}

	var categoryName string
	if err := reopened.QueryRowContext(ctx, "SELECT name FROM categories WHERE id = ?", categoryID).Scan(&categoryName); err != nil {
		t.Fatalf("query reopened category: %v", err)
	}
	if categoryName != "Groceries" {
		t.Fatalf("reopened category name = %q, want Groceries", categoryName)
	}

	var budgetMonth string
	var budgetAmount int64
	if err := reopened.QueryRowContext(ctx, "SELECT month, amount_hundredths FROM budgets WHERE id = ?", budgetID).Scan(&budgetMonth, &budgetAmount); err != nil {
		t.Fatalf("query reopened budget: %v", err)
	}
	if budgetMonth != "2026-08" || budgetAmount != 50000 {
		t.Fatalf("reopened budget = (%q, %d), want (2026-08, 50000)", budgetMonth, budgetAmount)
	}

	var transactionMerchant, transactionDate string
	var transactionAmount, transactionCategoryID int64
	if err := reopened.QueryRowContext(ctx, `
		SELECT t.merchant, a.amount_hundredths, t.date, a.category_id
		FROM transactions AS t
		INNER JOIN transaction_allocations AS a ON a.transaction_id = t.id
		WHERE t.id = ?
	`, transactionID).Scan(&transactionMerchant, &transactionAmount, &transactionDate, &transactionCategoryID); err != nil {
		t.Fatalf("query reopened transaction: %v", err)
	}
	if transactionMerchant != "Metro" || transactionAmount != 1250 || transactionDate != "2026-08-14" || transactionCategoryID != categoryID {
		t.Fatalf("reopened transaction = (%q, %d, %q, %d), want (Metro, 1250, 2026-08-14, %d)", transactionMerchant, transactionAmount, transactionDate, transactionCategoryID, categoryID)
	}

	var knownMerchant string
	var knownCategoryID int64
	if err := reopened.QueryRowContext(ctx, "SELECT merchant, category_id FROM known_merchants WHERE id = ?", merchantID).Scan(&knownMerchant, &knownCategoryID); err != nil {
		t.Fatalf("query reopened known merchant: %v", err)
	}
	if knownMerchant != "Metro" || knownCategoryID != categoryID {
		t.Fatalf("reopened known merchant = (%q, %d), want (Metro, %d)", knownMerchant, knownCategoryID, categoryID)
	}
}

type schemaIndex struct {
	name    string
	unique  bool
	origin  string
	partial bool
	columns []schemaIndexColumn
}

type schemaIndexColumn struct {
	name       string
	descending bool
}

type schemaForeignKey struct {
	id       int64
	seq      int64
	table    string
	from     string
	to       string
	onUpdate string
	onDelete string
	match    string
}

func openSchemaDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "finance.db")
	db, err := database.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	if db == nil {
		t.Fatalf("Open(%q) returned a nil database", path)
	}
	if _, err := os.Stat(path); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("close database after missing file: %v", closeErr)
		}
		t.Fatalf("stat on-disk database %q: %v", path, err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close schema test database: %v", err)
		}
	})
	return db, path
}

func schemaTables(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(tables)
	return tables, nil
}

func schemaIndexes(ctx context.Context, db *sql.DB, table string) ([]schemaIndex, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA index_list("+quotePragmaArgument(table)+")")
	if err != nil {
		return nil, err
	}

	type indexListEntry struct {
		name    string
		unique  bool
		origin  string
		partial bool
	}
	var entries []indexListEntry
	for rows.Next() {
		var seq, unique, partial int64
		var name, origin string
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			rows.Close()
			return nil, err
		}
		entries = append(entries, indexListEntry{
			name:    name,
			unique:  unique != 0,
			origin:  origin,
			partial: partial != 0,
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	indexes := make([]schemaIndex, 0, len(entries))
	for _, entry := range entries {
		columns, err := schemaIndexColumns(ctx, db, entry.name)
		if err != nil {
			return nil, fmt.Errorf("index %q: %w", entry.name, err)
		}
		indexes = append(indexes, schemaIndex{
			name:    entry.name,
			unique:  entry.unique,
			origin:  entry.origin,
			partial: entry.partial,
			columns: columns,
		})
	}
	return indexes, nil
}

func schemaIndexColumns(ctx context.Context, db *sql.DB, indexName string) ([]schemaIndexColumn, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA index_xinfo("+quotePragmaArgument(indexName)+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []schemaIndexColumn
	for rows.Next() {
		var seq, cid, descending, key int64
		var name sql.NullString
		var collation string
		if err := rows.Scan(&seq, &cid, &name, &descending, &collation, &key); err != nil {
			return nil, err
		}
		if key == 0 || !name.Valid {
			continue
		}
		columns = append(columns, schemaIndexColumn{name: name.String, descending: descending != 0})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}

func schemaForeignKeys(ctx context.Context, db *sql.DB, table string) ([]schemaForeignKey, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_list("+quotePragmaArgument(table)+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var foreignKeys []schemaForeignKey
	for rows.Next() {
		var foreignKey schemaForeignKey
		if err := rows.Scan(&foreignKey.id, &foreignKey.seq, &foreignKey.table, &foreignKey.from, &foreignKey.to, &foreignKey.onUpdate, &foreignKey.onDelete, &foreignKey.match); err != nil {
			return nil, err
		}
		foreignKeys = append(foreignKeys, foreignKey)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return foreignKeys, nil
}

func assertImportItemForeignKeys(t *testing.T, foreignKeys []schemaForeignKey) {
	t.Helper()
	if len(foreignKeys) != 2 {
		t.Fatalf("transaction_import_items foreign keys = %v, want 2", foreignKeys)
	}
	byFrom := make(map[string]schemaForeignKey, len(foreignKeys))
	for _, foreignKey := range foreignKeys {
		byFrom[foreignKey.from] = foreignKey
	}
	importFK, ok := byFrom["import_id"]
	if !ok || importFK.table != "transaction_imports" || importFK.to != "id" {
		t.Fatalf("import_id foreign key = %v, want transaction_imports(id)", importFK)
	}
	if importFK.onDelete != "RESTRICT" && importFK.onDelete != "NO ACTION" {
		t.Fatalf("import_id delete action = %q, want RESTRICT or NO ACTION", importFK.onDelete)
	}
	txnFK, ok := byFrom["transaction_id"]
	if !ok || txnFK.table != "transactions" || txnFK.to != "id" {
		t.Fatalf("transaction_id foreign key = %v, want transactions(id)", txnFK)
	}
	if txnFK.onDelete != "SET NULL" {
		t.Fatalf("transaction_id delete action = %q, want SET NULL", txnFK.onDelete)
	}
}

func assertRecurringRunForeignKeys(t *testing.T, foreignKeys []schemaForeignKey) {
	t.Helper()
	if len(foreignKeys) != 2 {
		t.Fatalf("recurring_transaction_runs foreign keys = %v, want 2", foreignKeys)
	}
	byFrom := make(map[string]schemaForeignKey, len(foreignKeys))
	for _, foreignKey := range foreignKeys {
		byFrom[foreignKey.from] = foreignKey
	}
	tmplFK, ok := byFrom["recurring_transaction_id"]
	if !ok || tmplFK.table != "recurring_transactions" || tmplFK.to != "id" {
		t.Fatalf("recurring_transaction_id foreign key = %v, want recurring_transactions(id)", tmplFK)
	}
	if tmplFK.onDelete != "RESTRICT" && tmplFK.onDelete != "NO ACTION" {
		t.Fatalf("recurring_transaction_id delete action = %q, want RESTRICT or NO ACTION", tmplFK.onDelete)
	}
	txnFK, ok := byFrom["transaction_id"]
	if !ok || txnFK.table != "transactions" || txnFK.to != "id" {
		t.Fatalf("transaction_id foreign key = %v, want transactions(id)", txnFK)
	}
	if txnFK.onDelete != "SET NULL" {
		t.Fatalf("transaction_id delete action = %q, want SET NULL", txnFK.onDelete)
	}
}

func assertIdempotencyForeignKeys(t *testing.T, foreignKeys []schemaForeignKey) {
	t.Helper()
	if len(foreignKeys) != 1 {
		t.Fatalf("transaction_idempotency foreign keys = %v, want 1", foreignKeys)
	}
	foreignKey := foreignKeys[0]
	if foreignKey.table != "transactions" || foreignKey.from != "transaction_id" || foreignKey.to != "id" {
		t.Fatalf("transaction_idempotency foreign key = %v, want transaction_id -> transactions(id)", foreignKey)
	}
	if foreignKey.onDelete != "SET NULL" {
		t.Fatalf("transaction_idempotency delete action = %q, want SET NULL", foreignKey.onDelete)
	}
}

func assertAllocationForeignKeys(t *testing.T, foreignKeys []schemaForeignKey) {
	t.Helper()
	if len(foreignKeys) != 2 {
		t.Fatalf("transaction_allocations foreign keys = %v, want 2", foreignKeys)
	}
	byFrom := make(map[string]schemaForeignKey, len(foreignKeys))
	for _, foreignKey := range foreignKeys {
		byFrom[foreignKey.from] = foreignKey
	}
	transactionFK, ok := byFrom["transaction_id"]
	if !ok || transactionFK.table != "transactions" || transactionFK.to != "id" || transactionFK.onDelete != "CASCADE" {
		t.Fatalf("transaction_id foreign key = %v, want transactions(id) ON DELETE CASCADE", transactionFK)
	}
	categoryFK, ok := byFrom["category_id"]
	if !ok || categoryFK.table != "categories" || categoryFK.to != "id" || (categoryFK.onDelete != "RESTRICT" && categoryFK.onDelete != "NO ACTION") {
		t.Fatalf("category_id foreign key = %v, want categories(id) with restrictive delete", categoryFK)
	}
}

func quotePragmaArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func countNonConstraintIndexes(indexes []schemaIndex) int {
	count := 0
	for _, index := range indexes {
		if !index.unique && index.origin == "c" && !index.partial {
			count++
		}
	}
	return count
}

func hasIndexSignature(indexes []schemaIndex, want ...schemaIndexColumn) bool {
	for _, index := range indexes {
		if index.unique || index.origin != "c" || index.partial || len(index.columns) != len(want) {
			continue
		}
		matches := true
		for i := range want {
			if index.columns[i] != want[i] {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func indexHasColumn(index schemaIndex, name string) bool {
	for _, column := range index.columns {
		if column.name == name {
			return true
		}
	}
	return false
}

func insertCategory(t *testing.T, ctx context.Context, db *sql.DB, name string) int64 {
	t.Helper()
	result, err := db.ExecContext(ctx, "INSERT INTO categories (name) VALUES (?)", name)
	if err != nil {
		t.Fatalf("insert category %q: %v", name, err)
	}
	return lastInsertID(t, result, "category")
}

func insertBudget(t *testing.T, ctx context.Context, db *sql.DB, categoryID int64, month string, amount int64) int64 {
	t.Helper()
	result, err := db.ExecContext(ctx, "INSERT INTO budgets (category_id, month, amount_hundredths) VALUES (?, ?, ?)", categoryID, month, amount)
	if err != nil {
		t.Fatalf("insert budget: %v", err)
	}
	return lastInsertID(t, result, "budget")
}

func insertTransaction(t *testing.T, ctx context.Context, db *sql.DB, merchant string, amount int64, date string, categoryID int64) int64 {
	t.Helper()
	var result sql.Result
	var err error
	if tableExists(t, db, "transaction_allocations") {
		result, err = db.ExecContext(ctx, "INSERT INTO transactions (merchant, date) VALUES (?, ?)", merchant, date)
	} else {
		result, err = db.ExecContext(ctx, "INSERT INTO transactions (merchant, amount_hundredths, date, category_id) VALUES (?, ?, ?, ?)", merchant, amount, date, categoryID)
	}
	if err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	id := lastInsertID(t, result, "transaction")
	if tableExists(t, db, "transaction_allocations") {
		if _, err := db.ExecContext(ctx, "INSERT INTO transaction_allocations (transaction_id, category_id, amount_hundredths) VALUES (?, ?, ?)", id, categoryID, amount); err != nil {
			t.Fatalf("insert transaction allocation: %v", err)
		}
	}
	return id
}

func insertKnownMerchant(t *testing.T, ctx context.Context, db *sql.DB, merchant string, categoryID int64) int64 {
	t.Helper()
	result, err := db.ExecContext(ctx, "INSERT INTO known_merchants (merchant, category_id) VALUES (?, ?)", merchant, categoryID)
	if err != nil {
		t.Fatalf("insert known merchant: %v", err)
	}
	return lastInsertID(t, result, "known merchant")
}

func lastInsertID(t *testing.T, result sql.Result, recordType string) int64 {
	t.Helper()
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read %s generated ID: %v", recordType, err)
	}
	if id <= 0 {
		t.Fatalf("%s generated ID = %d, want positive", recordType, id)
	}
	return id
}

func expectExecError(t *testing.T, ctx context.Context, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(ctx, query, args...); err == nil {
		t.Fatalf("expected SQL operation to fail: %s", query)
	}
}

func assertPositiveIDAndUTCDefaults(t *testing.T, ctx context.Context, db *sql.DB, table string, id int64) {
	t.Helper()
	var gotID int64
	var createdAt, updatedAt string
	query := fmt.Sprintf("SELECT id, created_at, updated_at FROM %s WHERE id = ?", table)
	if err := db.QueryRowContext(ctx, query, id).Scan(&gotID, &createdAt, &updatedAt); err != nil {
		t.Fatalf("query %s row %d: %v", table, id, err)
	}
	if gotID <= 0 {
		t.Fatalf("%s generated ID = %d, want positive", table, gotID)
	}
	assertUTCDefault(t, table+".created_at", createdAt)
	assertUTCDefault(t, table+".updated_at", updatedAt)
}

func assertUTCDefault(t *testing.T, field, value string) {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatalf("%s value %q does not parse as RFC3339Nano: %v", field, value, err)
	}
	if parsed.Location() != time.UTC {
		t.Fatalf("%s value %q has location %v, want UTC", field, value, parsed.Location())
	}
}

func assertAmountStorageType(t *testing.T, ctx context.Context, db *sql.DB, table string, id int64, want string) {
	t.Helper()
	var got string
	column := "id"
	if table == "transaction_allocations" {
		column = "transaction_id"
	}
	query := fmt.Sprintf("SELECT typeof(amount_hundredths) FROM %s WHERE %s = ?", table, column)
	if err := db.QueryRowContext(ctx, query, id).Scan(&got); err != nil {
		t.Fatalf("query %s amount storage type for row %d: %v", table, id, err)
	}
	if got != want {
		t.Fatalf("%s amount storage type = %q, want %q", table, got, want)
	}
}
