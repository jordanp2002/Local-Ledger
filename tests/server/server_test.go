package server_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/database"
	"github.com/jordanp2002/local-finance-mcp/internal/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStdioLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	databasePath := filepath.Join(t.TempDir(), "finance.db")
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
	cmd.Env = testEnvironment(
		"LOCAL_FINANCE_MCP_TEST_HELPER=1",
		"LOCAL_FINANCE_DB_PATH="+databasePath,
	)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("initialize stdio session: %v", err)
	}
	sessionClosed := false
	defer func() {
		if !sessionClosed {
			closeSession(t, session)
		}
	}()
	if result := session.InitializeResult(); result == nil || result.ServerInfo == nil || result.ServerInfo.Name != "local-finance-mcp" || result.ServerInfo.Version != "0.2.0" {
		t.Fatalf("unexpected initialize result: %#v", result)
	}

	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if got := listedToolNames(result.Tools); strings.Join(got, ",") != strings.Join(categoryToolNames, ",") {
		t.Fatalf("tools = %v, want %v", got, categoryToolNames)
	}

	created, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "create_category",
		Arguments: map[string]any{"name": "Groceries"},
	})
	if err != nil {
		t.Fatalf("create_category: %v", err)
	}
	if created.IsError {
		t.Fatalf("create_category IsError = true, want success: %#v", created)
	}
	set, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "set_known_merchant",
		Arguments: map[string]any{"merchant": "Metro", "category": "Groceries"},
	})
	if err != nil {
		t.Fatalf("set_known_merchant: %v", err)
	}
	if set.IsError {
		t.Fatalf("set_known_merchant IsError = true, want success: %#v", set)
	}
	month := time.Now().Format("2006-01")
	budgetResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "create_monthly_budget",
		Arguments: map[string]any{
			"month":   month,
			"budgets": []map[string]any{{"category": "Groceries", "amount": "500.00"}},
		},
	})
	if err != nil {
		t.Fatalf("create_monthly_budget: %v", err)
	}
	if budgetResult.IsError {
		t.Fatalf("create_monthly_budget IsError = true, want success: %#v", budgetResult)
	}
	setBudgets, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "set_budgets",
		Arguments: map[string]any{
			"month":   month,
			"budgets": []map[string]any{{"category": "Groceries", "amount": "300.00"}},
		},
	})
	if err != nil {
		t.Fatalf("set_budgets: %v", err)
	}
	if setBudgets.IsError {
		t.Fatalf("set_budgets IsError = true, want success: %#v", setBudgets)
	}
	added, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "add_transaction",
		Arguments: map[string]any{
			"amount":   "20.00",
			"merchant": "No Frills",
			"category": "Groceries",
			"date":     "2026-08-14",
			"note":     "weekly groceries",
		},
	})
	if err != nil {
		t.Fatalf("add_transaction: %v", err)
	}
	if added.IsError {
		t.Fatalf("add_transaction IsError = true, want success: %#v", added)
	}
	first := decodeTransaction(t, structuredObject(t, added)["transaction"])
	if first.ID == 0 {
		t.Fatal("added No Frills transaction id = 0")
	}

	updated, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "update_transaction",
		Arguments: map[string]any{
			"id":     first.ID,
			"amount": "23.50",
			"note":   nil,
		},
	})
	if err != nil {
		t.Fatalf("update_transaction: %v", err)
	}
	if updated.IsError {
		t.Fatalf("update_transaction IsError = true, want success: %#v", updated)
	}

	secondAdded, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "add_transaction",
		Arguments: map[string]any{
			"amount":   "15.00",
			"merchant": "Metro",
			"category": "Groceries",
			"date":     "2026-08-14",
		},
	})
	if err != nil {
		t.Fatalf("add_transaction second: %v", err)
	}
	if secondAdded.IsError {
		t.Fatalf("add_transaction second IsError = true, want success: %#v", secondAdded)
	}
	second := decodeTransaction(t, structuredObject(t, secondAdded)["transaction"])
	if second.ID == 0 || second.ID == first.ID {
		t.Fatalf("second transaction id = %d, want a new id", second.ID)
	}

	removed, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "remove_transaction",
		Arguments: map[string]any{"id": second.ID},
	})
	if err != nil {
		t.Fatalf("remove_transaction: %v", err)
	}
	if removed.IsError {
		t.Fatalf("remove_transaction IsError = true, want success: %#v", removed)
	}

	listed, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "list_transactions",
		Arguments: map[string]any{
			"start_date": "2026-08-01",
			"end_date":   "2026-08-31",
			"category":   "Groceries",
		},
	})
	if err != nil {
		t.Fatalf("list_transactions: %v", err)
	}
	if listed.IsError {
		t.Fatalf("list_transactions IsError = true, want success: %#v", listed)
	}
	listedPayload := structuredObject(t, listed)
	if keys := objectKeys(listedPayload); strings.Join(keys, ",") != "ok,page,transactions" {
		t.Fatalf("list_transactions keys = %v, want [ok page transactions]", keys)
	}
	if listedPayload["ok"] != true {
		t.Fatalf("list_transactions ok = %v, want true", listedPayload["ok"])
	}
	listedRows, ok := listedPayload["transactions"].([]any)
	if !ok || len(listedRows) != 1 {
		t.Fatalf("list_transactions transactions = %#v, want the remaining No Frills purchase", listedPayload["transactions"])
	}
	listedTxn := decodeTransaction(t, listedRows[0])
	if listedTxn.ID != first.ID || listedTxn.Merchant != "No Frills" || listedTxn.Amount != "23.50" || listedTxn.Date != "2026-08-14" || transactionCategory(listedTxn) != "Groceries" {
		t.Fatalf("listed transaction = %#v, want updated No Frills 23.50", listedTxn)
	}
	if listedTxn.Note != nil || asObject(t, listedRows[0])["note"] != nil {
		t.Fatalf("listed note = %#v, want null", listedTxn.Note)
	}

	monthly, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_monthly_summary",
		Arguments: map[string]any{"month": month},
	})
	if err != nil {
		t.Fatalf("get_monthly_summary: %v", err)
	}
	if monthly.IsError {
		t.Fatalf("get_monthly_summary IsError = true, want success: %#v", monthly)
	}
	monthlyPayload := structuredObject(t, monthly)
	if keys := objectKeys(monthlyPayload); strings.Join(keys, ",") != "categories,month,ok,remaining,spent_of_budget,total_base_budget,total_budget,total_rollover_adjustment,total_sinking_fund_opening_balance,total_spending" {
		t.Fatalf("get_monthly_summary keys = %v", keys)
	}
	if monthlyPayload["ok"] != true || monthlyPayload["month"] != month || monthlyPayload["total_budget"] != "300.00" {
		t.Fatalf("get_monthly_summary = %s, want current-month Groceries 300.00", structuredJSON(t, monthly))
	}
	monthlyCategories, ok := monthlyPayload["categories"].([]any)
	if !ok || len(monthlyCategories) != 1 {
		t.Fatalf("get_monthly_summary categories = %#v, want Groceries", monthlyPayload["categories"])
	}
	monthlyCategory := asObject(t, monthlyCategories[0])
	if monthlyCategory["category"] != "Groceries" || monthlyCategory["budget"] != "300.00" {
		t.Fatalf("monthly Groceries row = %#v", monthlyCategory)
	}

	categorySummary, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_category_summary",
		Arguments: map[string]any{"category": "Groceries", "month": month},
	})
	if err != nil {
		t.Fatalf("get_category_summary: %v", err)
	}
	if categorySummary.IsError {
		t.Fatalf("get_category_summary IsError = true, want success: %#v", categorySummary)
	}
	categoryPayload := structuredObject(t, categorySummary)
	if keys := objectKeys(categoryPayload); strings.Join(keys, ",") != "base_budget,budget,category,category_id,month,ok,remaining,rollover_adjustment,sinking_fund,sinking_fund_opening_balance,spent_of_budget,total_spending,transaction_count" {
		t.Fatalf("get_category_summary keys = %v", keys)
	}
	if categoryPayload["ok"] != true || categoryPayload["month"] != month || categoryPayload["category"] != "Groceries" || categoryPayload["budget"] != "300.00" {
		t.Fatalf("get_category_summary = %s, want current-month Groceries 300.00", structuredJSON(t, categorySummary))
	}

	closeSession(t, session)
	sessionClosed = true
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("helper process has not exited after closing the client session")
	}

	if _, err := os.Stat(databasePath); err != nil {
		t.Fatalf("stat migrated database: %v", err)
	}
	db, err := database.Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("open migrated database for verification: %v", err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("query migration version: %v", err)
	}
	if version != 8 {
		t.Fatalf("migration version = %d, want 8", version)
	}

	var name string
	var active int
	if err := db.QueryRowContext(ctx, "SELECT name, active FROM categories WHERE name = ?", "Groceries").Scan(&name, &active); err != nil {
		t.Fatalf("select persisted Groceries category: %v", err)
	}
	if name != "Groceries" || active != 1 {
		t.Fatalf("persisted category = (%q, %d), want (\"Groceries\", 1)", name, active)
	}

	var merchantName string
	var categoryID int64
	if err := db.QueryRowContext(ctx, `
		SELECT m.merchant, m.category_id
		FROM known_merchants AS m
		INNER JOIN categories AS c ON c.id = m.category_id
		WHERE m.merchant = ? COLLATE NOCASE
	`, "metro").Scan(&merchantName, &categoryID); err != nil {
		t.Fatalf("select persisted Metro mapping: %v", err)
	}
	if merchantName != "Metro" || categoryID == 0 {
		t.Fatalf("persisted merchant mapping = (%q, %d), want (\"Metro\", nonzero)", merchantName, categoryID)
	}

	var budgetMonth string
	var amount int64
	if err := db.QueryRowContext(ctx, `
		SELECT b.month, b.amount_hundredths
		FROM budgets AS b
		INNER JOIN categories AS c ON c.id = b.category_id
		WHERE c.name = ? COLLATE NOCASE
	`, "groceries").Scan(&budgetMonth, &amount); err != nil {
		t.Fatalf("select persisted Groceries budget: %v", err)
	}
	if budgetMonth != month || amount != 30000 {
		t.Fatalf("persisted Groceries budget = (%q, %d), want (%q, 30000)", budgetMonth, amount, month)
	}

	var transactionMerchant, transactionDate, transactionCategory string
	var transactionAmount int64
	var transactionNote sql.NullString
	if err := db.QueryRowContext(ctx, `
		SELECT t.merchant, a.amount_hundredths, t.date, t.note, c.name
		FROM transactions AS t
		INNER JOIN transaction_allocations AS a ON a.transaction_id = t.id
		INNER JOIN categories AS c ON c.id = a.category_id
		WHERE t.id = ?
	`, first.ID).Scan(&transactionMerchant, &transactionAmount, &transactionDate, &transactionNote, &transactionCategory); err != nil {
		t.Fatalf("select persisted No Frills transaction: %v", err)
	}
	if transactionMerchant != "No Frills" || transactionAmount != 2350 || transactionDate != "2026-08-14" || transactionCategory != "Groceries" {
		t.Fatalf("persisted transaction = (%q, %d, %q, %q), want (\"No Frills\", 2350, \"2026-08-14\", \"Groceries\")", transactionMerchant, transactionAmount, transactionDate, transactionCategory)
	}
	if transactionNote.Valid {
		t.Fatalf("persisted note = %q, want NULL after update_transaction note:null", transactionNote.String)
	}

	var remaining int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM transactions`).Scan(&remaining); err != nil {
		t.Fatalf("count persisted transactions: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("persisted transaction count = %d, want 1", remaining)
	}
	var removedCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM transactions WHERE id = ?`, second.ID).Scan(&removedCount); err != nil {
		t.Fatalf("count removed transaction: %v", err)
	}
	if removedCount != 0 {
		t.Fatalf("removed transaction %d is still present", second.ID)
	}

	var createdMerchant, createdCategory string
	if err := db.QueryRowContext(ctx, `
		SELECT m.merchant, c.name
		FROM known_merchants AS m
		INNER JOIN categories AS c ON c.id = m.category_id
		WHERE m.merchant = ? COLLATE NOCASE
	`, "No Frills").Scan(&createdMerchant, &createdCategory); err != nil {
		t.Fatalf("select persisted No Frills mapping: %v", err)
	}
	if createdMerchant != "No Frills" || createdCategory != "Groceries" {
		t.Fatalf("persisted created mapping = (%q, %q), want (\"No Frills\", \"Groceries\")", createdMerchant, createdCategory)
	}
}

func closeSession(t *testing.T, session *mcp.ClientSession) {
	t.Helper()
	if err := session.Close(); err != nil && !errors.Is(err, mcp.ErrConnectionClosed) {
		t.Errorf("close stdio session: %v", err)
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("LOCAL_FINANCE_MCP_TEST_HELPER") != "1" {
		return
	}

	if err := server.Run(context.Background(), server.Config{DatabasePath: os.Getenv("LOCAL_FINANCE_DB_PATH")}); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func testEnvironment(values ...string) []string {
	env := os.Environ()
	for _, value := range values {
		name, _, ok := strings.Cut(value, "=")
		if !ok {
			continue
		}
		env = appendWithoutEnvironmentVariable(env, name)
		env = append(env, value)
	}
	return env
}

func appendWithoutEnvironmentVariable(env []string, name string) []string {
	filtered := env[:0]
	prefix := name + "="
	for _, value := range env {
		if !strings.HasPrefix(value, prefix) {
			filtered = append(filtered, value)
		}
	}
	return filtered
}
