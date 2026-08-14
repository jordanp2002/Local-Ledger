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
	wantTables := []string{"budgets", "categories", "known_merchants", "transactions"}
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
	if countNonConstraintIndexes(indexes["transactions"]) != 2 {
		t.Fatalf("transactions non-constraint index count = %d, want 2", countNonConstraintIndexes(indexes["transactions"]))
	}
	if !hasIndexSignature(indexes["transactions"], schemaIndexColumn{name: "date", descending: true}, schemaIndexColumn{name: "id", descending: true}) {
		t.Fatalf("transactions is missing its date/id query index: %v", indexes["transactions"])
	}
	if !hasIndexSignature(indexes["transactions"], schemaIndexColumn{name: "category_id"}, schemaIndexColumn{name: "date", descending: true}, schemaIndexColumn{name: "id", descending: true}) {
		t.Fatalf("transactions is missing its category/date/id query index: %v", indexes["transactions"])
	}
	if countNonConstraintIndexes(indexes["known_merchants"]) != 0 {
		t.Fatalf("known_merchants has a speculative non-constraint index: %v", indexes["known_merchants"])
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
		"categories":      false,
		"budgets":         true,
		"transactions":    true,
		"known_merchants": true,
	}
	for _, table := range wantTables {
		foreignKeys, err := schemaForeignKeys(ctx, db, table)
		if err != nil {
			t.Fatalf("query foreign keys for %s: %v", table, err)
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
		{table: "transactions", id: transactionID},
	} {
		var storageType string
		query := fmt.Sprintf("SELECT typeof(amount_hundredths) FROM %s WHERE id = ?", record.table)
		if err := db.QueryRowContext(ctx, query, record.id).Scan(&storageType); err != nil {
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
	assertAmountStorageType(t, ctx, db, "transactions", positiveTransactionID, "integer")
	expectExecError(t, ctx, db, "INSERT INTO transactions (merchant, amount_hundredths, date, category_id) VALUES (?, ?, ?, ?)", "Zero", 0, "2026-08-14", categoryID)
	expectExecError(t, ctx, db, "INSERT INTO transactions (merchant, amount_hundredths, date, category_id) VALUES (?, ?, ?, ?)", "Negative", -1, "2026-08-14", categoryID)
	expectExecError(t, ctx, db, "INSERT INTO transactions (merchant, amount_hundredths, date, category_id) VALUES (?, CAST(? AS REAL), ?, ?)", "Real", 12.5, "2026-08-14", categoryID)
	expectExecError(t, ctx, db, "INSERT INTO transactions (merchant, amount_hundredths, date, category_id) VALUES (?, CAST(? AS TEXT), ?, ?)", "TextDecimal", "12.5", "2026-08-14", categoryID)
	expectExecError(t, ctx, db, "INSERT INTO transactions (merchant, amount_hundredths, date, category_id) VALUES (?, CAST(? AS TEXT), ?, ?)", "TextNonInteger", "not-an-integer", "2026-08-14", categoryID)
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
	if version != 1 {
		t.Fatalf("reopened user_version = %d, want 1", version)
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
	if err := reopened.QueryRowContext(ctx, "SELECT merchant, amount_hundredths, date, category_id FROM transactions WHERE id = ?", transactionID).Scan(&transactionMerchant, &transactionAmount, &transactionDate, &transactionCategoryID); err != nil {
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
	result, err := db.ExecContext(ctx, "INSERT INTO transactions (merchant, amount_hundredths, date, category_id) VALUES (?, ?, ?, ?)", merchant, amount, date, categoryID)
	if err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	return lastInsertID(t, result, "transaction")
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
	query := fmt.Sprintf("SELECT typeof(amount_hundredths) FROM %s WHERE id = ?", table)
	if err := db.QueryRowContext(ctx, query, id).Scan(&got); err != nil {
		t.Fatalf("query %s amount storage type for row %d: %v", table, id, err)
	}
	if got != want {
		t.Fatalf("%s amount storage type = %q, want %q", table, got, want)
	}
}
