package server_test

import (
	"bytes"
	"context"
	"database/sql"
	"log"
	"strings"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

func TestAddTransactionsToolDiscovery(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if got := listedToolNames(result.Tools); strings.Join(got, ",") != strings.Join(categoryToolNames, ",") {
		t.Fatalf("tools = %v, want %v", got, categoryToolNames)
	}
	if len(result.Tools) != 21 {
		t.Fatalf("tool count = %d, want 21", len(result.Tools))
	}

	tool := toolByName(t, result.Tools, "add_transactions")
	if tool.Description != addTransactionsDiscoveryDescription {
		t.Fatalf("description = %q, want the published batch contract", tool.Description)
	}
	if tool.Annotations == nil || tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint {
		t.Fatalf("annotations = %#v, want writable idempotent", tool.Annotations)
	}
	if tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint {
		t.Fatalf("destructiveHint = %v, want true", tool.Annotations.DestructiveHint)
	}
	if tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
		t.Fatalf("openWorldHint = %v, want false", tool.Annotations.OpenWorldHint)
	}

	schema := schemaObject(t, tool.InputSchema)
	if schema["type"] != "object" {
		t.Fatalf("input schema type = %v, want object", schema["type"])
	}
	required, _ := schema["required"].([]any)
	if !containsValue(required, "idempotency_key") || !containsValue(required, "transactions") {
		t.Fatalf("required = %v, want idempotency_key and transactions", required)
	}
	properties, _ := schema["properties"].(map[string]any)
	keySchema, _ := properties["idempotency_key"].(map[string]any)
	if keySchema == nil || !schemaTypeContains(keySchema["type"], "string") {
		t.Fatalf("idempotency_key schema = %#v, want string", properties["idempotency_key"])
	}
	arraySchema, _ := properties["transactions"].(map[string]any)
	if arraySchema == nil || !schemaTypeContains(arraySchema["type"], "array") {
		t.Fatalf("transactions schema = %#v, want array", properties["transactions"])
	}
	items := nestedSchemaObject(t, arraySchema["items"])
	if !schemaTypeContains(items["type"], "object") {
		t.Fatalf("transaction item schema = %#v, want object", arraySchema["items"])
	}
	itemRequired, _ := items["required"].([]any)
	if !containsValue(itemRequired, "amount") || !containsValue(itemRequired, "merchant") || !containsValue(itemRequired, "date") {
		t.Fatalf("item required = %v, want amount, merchant, and date", itemRequired)
	}
	if containsValue(itemRequired, "category") || containsValue(itemRequired, "note") {
		t.Fatalf("item required = %v, want category and note optional", itemRequired)
	}
	itemProperties, _ := items["properties"].(map[string]any)
	for _, field := range []string{"amount", "merchant", "category", "date", "note"} {
		property, _ := itemProperties[field].(map[string]any)
		if property == nil || !schemaTypeContains(property["type"], "string") {
			t.Fatalf("%s schema = %#v, want string", field, itemProperties[field])
		}
	}
}

func TestAddTransactionsSuccessAndReplay(t *testing.T) {
	db := openCategoryDB(t)
	session := connectCategorySession(t, db, fixedTransactionNow, nil)
	createCategoryForMerchantTest(t, session, "Groceries")
	createCategoryForMerchantTest(t, session, "Entertainment")

	args := map[string]any{
		"idempotency_key": "statement-2026-08-19-page-1",
		"transactions": []map[string]any{
			{
				"amount":   "24.18",
				"merchant": "Metro",
				"category": "Groceries",
				"date":     "2026-08-14",
				"note":     "Imported from statement screenshot",
			},
			{
				"amount":   "15.99",
				"merchant": "Netflix",
				"category": "Entertainment",
				"date":     "2026-08-13",
			},
		},
	}
	result := callTool(t, session, "add_transactions", args)
	if result.IsError {
		t.Fatalf("add_transactions failed: %s", structuredJSON(t, result))
	}
	got := structuredObject(t, result)
	if keys := objectKeys(got); strings.Join(keys, ",") != "count,idempotency_key,idempotent_replay,ok,total_amount,transactions" {
		t.Fatalf("add_transactions keys = %v", keys)
	}
	if got["ok"] != true || got["idempotency_key"] != "statement-2026-08-19-page-1" || got["idempotent_replay"] != false {
		t.Fatalf("success metadata = %s", structuredJSON(t, result))
	}
	if got["count"] != float64(2) || got["total_amount"] != "40.17" {
		t.Fatalf("count/total = %s", structuredJSON(t, result))
	}
	rows, ok := got["transactions"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("transactions = %#v, want 2 rows", got["transactions"])
	}
	first := asObject(t, rows[0])
	if first["category_source"] != "provided" || first["merchant_mapping_action"] != "created" {
		t.Fatalf("row 0 metadata = %#v", first)
	}
	firstTxn := decodeTransaction(t, first["transaction"])
	if firstTxn.Merchant != "Metro" || firstTxn.Amount != "24.18" || firstTxn.Date != "2026-08-14" {
		t.Fatalf("row 0 transaction = %#v", firstTxn)
	}
	second := asObject(t, rows[1])
	if second["category_source"] != "provided" || second["merchant_mapping_action"] != "created" {
		t.Fatalf("row 1 metadata = %#v", second)
	}

	if countTable(t, db, "transactions") != 2 {
		t.Fatalf("stored transactions = %d, want 2", countTable(t, db, "transactions"))
	}

	replay := callTool(t, session, "add_transactions", args)
	if replay.IsError {
		t.Fatalf("replay failed: %s", structuredJSON(t, replay))
	}
	replayed := structuredObject(t, replay)
	if replayed["idempotent_replay"] != true || replayed["total_amount"] != "40.17" {
		t.Fatalf("replay payload = %s", structuredJSON(t, replay))
	}
	replayRows, _ := replayed["transactions"].([]any)
	if decodeTransaction(t, asObject(t, replayRows[0])["transaction"]).ID != firstTxn.ID {
		t.Fatal("replay changed transaction IDs")
	}
	if countTable(t, db, "transactions") != 2 {
		t.Fatalf("replay wrote extra rows: %d", countTable(t, db, "transactions"))
	}
}

func TestAddTransactionsInvalidFieldsUseIndexedPaths(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
	result := callTool(t, session, "add_transactions", map[string]any{
		"idempotency_key": "statement-1",
		"transactions": []map[string]any{
			{"amount": "0", "merchant": "Metro", "date": "2026-08-14"},
			{"amount": "1.00", "merchant": "Netflix", "date": "not-a-date"},
		},
	})
	if !result.IsError {
		t.Fatal("invalid add_transactions IsError = false, want true")
	}
	requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{
			"fields": []contract.FieldIssue{
				{Field: "transactions[0].amount", Reason: "must be greater than zero"},
				{Field: "transactions[1].date", Reason: "must be a valid YYYY-MM-DD date"},
			},
		},
	)))
}

func TestAddTransactionsDomainErrorIncludesIndexAndWritesNothing(t *testing.T) {
	db := openCategoryDB(t)
	session := connectCategorySession(t, db, fixedTransactionNow, nil)
	groceries := createCategoryForMerchantTest(t, session, "Groceries")

	result := callTool(t, session, "add_transactions", map[string]any{
		"idempotency_key": "statement-1",
		"transactions": []map[string]any{
			{"amount": "20.00", "merchant": "Metro", "category": "Groceries", "date": "2026-08-14"},
			{"amount": "5.00", "merchant": "Shoppers", "category": "Pharmacy", "date": "2026-08-13"},
		},
	})
	if !result.IsError {
		t.Fatal("domain add_transactions IsError = false, want true")
	}
	requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeCategoryNotFound,
		"Category 'Pharmacy' does not exist.",
		false,
		map[string]any{
			"requested_category": "Pharmacy",
			"categories":         []contract.Category{groceries},
			"index":              1,
		},
	)))
	if countTable(t, db, "transactions") != 0 || countTable(t, db, "known_merchants") != 0 || countTable(t, db, "transaction_imports") != 0 {
		t.Fatal("failed batch persisted a partial import")
	}
}

func TestAddTransactionsIdempotencyConflicts(t *testing.T) {
	t.Run("payload_mismatch", func(t *testing.T) {
		session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
		createCategoryForMerchantTest(t, session, "Groceries")
		first := callTool(t, session, "add_transactions", map[string]any{
			"idempotency_key": "statement-1",
			"transactions": []map[string]any{
				{"amount": "20.00", "merchant": "Metro", "category": "Groceries", "date": "2026-08-14"},
			},
		})
		if first.IsError {
			t.Fatalf("first add_transactions: %s", structuredJSON(t, first))
		}
		result := callTool(t, session, "add_transactions", map[string]any{
			"idempotency_key": "statement-1",
			"transactions": []map[string]any{
				{"amount": "21.00", "merchant": "Metro", "category": "Groceries", "date": "2026-08-14"},
			},
		})
		if !result.IsError {
			t.Fatal("payload mismatch IsError = false, want true")
		}
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeIdempotencyConflict,
			"The idempotency key has already been used for a different transaction import.",
			false,
			map[string]any{
				"idempotency_key": "statement-1",
				"reason":          "payload_mismatch",
			},
		)))
	})

	t.Run("transaction_removed", func(t *testing.T) {
		session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
		createCategoryForMerchantTest(t, session, "Groceries")
		args := map[string]any{
			"idempotency_key": "statement-1",
			"transactions": []map[string]any{
				{"amount": "20.00", "merchant": "Metro", "category": "Groceries", "date": "2026-08-14"},
				{"amount": "5.00", "merchant": "No Frills", "category": "Groceries", "date": "2026-08-13"},
			},
		}
		first := callTool(t, session, "add_transactions", args)
		if first.IsError {
			t.Fatalf("first add_transactions: %s", structuredJSON(t, first))
		}
		rows, _ := structuredObject(t, first)["transactions"].([]any)
		removedID := decodeTransaction(t, asObject(t, rows[1])["transaction"]).ID
		if result := callTool(t, session, "remove_transaction", map[string]any{"id": removedID}); result.IsError {
			t.Fatalf("remove_transaction: %s", structuredJSON(t, result))
		}
		result := callTool(t, session, "add_transactions", args)
		if !result.IsError {
			t.Fatal("removed-key replay IsError = false, want true")
		}
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeIdempotencyConflict,
			"An imported transaction was removed; this idempotency key cannot be reused and the batch must not be resubmitted.",
			false,
			map[string]any{
				"idempotency_key": "statement-1",
				"reason":          "transaction_removed",
				"removed_indexes": []int{1},
			},
		)))
	})
}

func TestAddTransactionsInternalError(t *testing.T) {
	db := openCategoryDB(t)
	var logs bytes.Buffer
	session := connectCategorySession(t, db, fixedTransactionNow, log.New(&logs, "", 0))
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	result := callTool(t, session, "add_transactions", map[string]any{
		"idempotency_key": "statement-1",
		"transactions": []map[string]any{
			{"amount": "20.00", "merchant": "Metro", "category": "Groceries", "date": "2026-08-14"},
		},
	})
	if !result.IsError {
		t.Fatal("internal add_transactions IsError = false, want true")
	}
	requireStructuredEqual(t, result, contract.NewInternalErrorEnvelope())
	if leakedInternalError(structuredJSON(t, result)) || leakedInternalError(toolText(result)) {
		t.Fatalf("public payload leaked internal details: %s", structuredJSON(t, result))
	}
	if logs.Len() == 0 {
		t.Fatal("logger did not record the private cause")
	}
	if !strings.Contains(logs.String(), "sql:") && !strings.Contains(logs.String(), "database is closed") {
		t.Fatalf("logger = %q, want private database cause", logs.String())
	}
}

func TestAddTransactionsAppearInExistingSummaries(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
	createCategoryForMerchantTest(t, session, "Groceries")
	if result := callTool(t, session, "create_monthly_budget", map[string]any{
		"month":   "2026-08",
		"budgets": []map[string]any{{"category": "Groceries", "amount": "500.00"}},
	}); result.IsError {
		t.Fatalf("create_monthly_budget: %s", structuredJSON(t, result))
	}

	if result := callTool(t, session, "add_transactions", map[string]any{
		"idempotency_key": "statement-1",
		"transactions": []map[string]any{
			{"amount": "20.00", "merchant": "Metro", "category": "Groceries", "date": "2026-08-14"},
			{"amount": "15.50", "merchant": "No Frills", "category": "Groceries", "date": "2026-08-13"},
		},
	}); result.IsError {
		t.Fatalf("add_transactions: %s", structuredJSON(t, result))
	}

	monthly := callTool(t, session, "get_monthly_summary", map[string]any{"month": "2026-08"})
	if monthly.IsError {
		t.Fatalf("get_monthly_summary: %s", structuredJSON(t, monthly))
	}
	monthlyPayload := structuredObject(t, monthly)
	if monthlyPayload["total_spending"] != "35.50" {
		t.Fatalf("monthly spending = %s, want 35.50 from imported rows", structuredJSON(t, monthly))
	}

	categorySummary := callTool(t, session, "get_category_summary", map[string]any{
		"category": "Groceries",
		"month":    "2026-08",
	})
	if categorySummary.IsError {
		t.Fatalf("get_category_summary: %s", structuredJSON(t, categorySummary))
	}
	categoryPayload := structuredObject(t, categorySummary)
	if categoryPayload["total_spending"] != "35.50" || categoryPayload["transaction_count"] != float64(2) {
		t.Fatalf("category summary = %s, want imported spending", structuredJSON(t, categorySummary))
	}
}

const addTransactionsDiscoveryDescription = "Atomically record a confirmed batch of structured expenses using exact merchant-default rules. Submit only user-confirmed expense rows — not images, files, credits, payments, pending transactions, or unreadable lines. Resolve every uncategorized merchant with the user before calling. Each row requires amount, merchant, and a YYYY-MM-DD date; dates are never defaulted to today. The first occurrence of a new merchant in the array must include category unless an exact mapping already exists. `idempotency_key` is required. Reuse the exact same key and payload if retrying this confirmed batch; do not mint a new key for a retry. The server does not detect duplicate purchases. The call is all-or-nothing: any invalid or uncategorized row writes nothing."

func nestedSchemaObject(t *testing.T, value any) map[string]any {
	t.Helper()
	if value == nil {
		t.Fatal("schema items is nil")
	}
	return asObject(t, value)
}

func countTable(t *testing.T, db *sql.DB, table string) int64 {
	t.Helper()
	var count int64
	query := "SELECT count(*) FROM " + table
	if err := db.QueryRowContext(context.Background(), query).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}
