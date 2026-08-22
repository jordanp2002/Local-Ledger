package server_test

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

func TestListTopMerchantsToolSchema(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	topMerchants := toolByName(t, result.Tools, "list_top_merchants")
	schema := schemaObject(t, topMerchants.InputSchema)
	if schema["type"] != "object" {
		t.Fatalf("list_top_merchants input schema type = %v, want object", schema["type"])
	}
	required, _ := schema["required"].([]any)
	if len(required) != 0 {
		t.Fatalf("list_top_merchants required = %v, want no required fields", required)
	}
	properties, _ := schema["properties"].(map[string]any)
	for _, field := range []string{"start_date", "end_date", "category"} {
		property, _ := properties[field].(map[string]any)
		if property == nil || !schemaTypeContains(property["type"], "string") {
			t.Fatalf("list_top_merchants %s schema = %#v, want string", field, properties[field])
		}
	}
	limit, _ := properties["limit"].(map[string]any)
	if limit == nil || !schemaTypeContains(limit["type"], "integer") {
		t.Fatalf("list_top_merchants limit schema = %#v, want integer", properties["limit"])
	}
	if topMerchants.Annotations == nil {
		t.Fatal("list_top_merchants annotations are nil")
	}
	if !topMerchants.Annotations.ReadOnlyHint {
		t.Fatal("list_top_merchants readOnlyHint = false, want true")
	}
	if topMerchants.Annotations.OpenWorldHint == nil || *topMerchants.Annotations.OpenWorldHint {
		t.Fatalf("list_top_merchants openWorldHint = %v, want explicit false", topMerchants.Annotations.OpenWorldHint)
	}
	if topMerchants.Annotations.DestructiveHint != nil {
		t.Fatalf("list_top_merchants destructiveHint = %v, want omitted", *topMerchants.Annotations.DestructiveHint)
	}
}

func TestListTopMerchantsSuccessFiltersAndPagination(t *testing.T) {
	ctx := context.Background()
	db := openCategoryDB(t)
	session := connectCategorySession(t, db, fixedTransactionNow, nil)
	createCategoryForMerchantTest(t, session, "Groceries")
	dining := createCategoryForMerchantTest(t, session, "Dining")

	addTestTransaction(t, session, map[string]any{
		"amount": "5.00", "merchant": "Old", "category": "Groceries", "date": "2026-07-31",
	})
	addTestTransaction(t, session, map[string]any{
		"amount": "20.00", "merchant": "Metro", "category": "Groceries", "date": "2026-08-10",
	})
	addTestTransaction(t, session, map[string]any{
		"amount": "15.00", "merchant": "metro", "category": "Groceries", "date": "2026-08-14",
	})
	addTestTransaction(t, session, map[string]any{
		"amount": "20.00", "merchant": "Cafe", "category": "Groceries", "date": "2026-08-13",
	})
	addTestTransaction(t, session, map[string]any{
		"amount": "20.00", "merchant": "Shoppers", "category": "Groceries", "date": "2026-08-12",
	})
	addTestTransaction(t, session, map[string]any{
		"amount": "50.00", "merchant": "Dining Place", "category": "Dining", "date": "2026-08-14",
	})
	if _, err := db.ExecContext(ctx, `UPDATE categories SET active = 0 WHERE id = ?`, dining.ID); err != nil {
		t.Fatalf("disable Dining: %v", err)
	}

	beforeTransactions := countTable(t, db, "transactions")
	all := callTool(t, session, "list_top_merchants", map[string]any{})
	if all.IsError {
		t.Fatalf("list_top_merchants failed: %s", structuredJSON(t, all))
	}
	payload := structuredObject(t, all)
	if keys := objectKeys(payload); strings.Join(keys, ",") != "category,end_date,limit,merchant_count,merchants,ok,returned,start_date,total_spending,transaction_count" {
		t.Fatalf("keys = %v", keys)
	}
	if payload["ok"] != true || payload["start_date"] != nil || payload["end_date"] != nil || payload["category"] != nil {
		t.Fatalf("all-time filters = %#v", payload)
	}
	if payload["total_spending"] != "130.00" || payload["transaction_count"] != float64(6) || payload["limit"] != float64(10) || payload["returned"] != float64(5) || payload["merchant_count"] != float64(5) {
		t.Fatalf("all-time totals = %s", structuredJSON(t, all))
	}
	rows := topMerchantRows(t, payload)
	if len(rows) != 5 {
		t.Fatalf("all-time merchants = %#v, want five groups", rows)
	}
	want := []struct {
		merchant string
		spending string
		count    float64
	}{
		{merchant: "Dining Place", spending: "50.00", count: 1},
		{merchant: "metro", spending: "35.00", count: 2},
		{merchant: "Cafe", spending: "20.00", count: 1},
		{merchant: "Shoppers", spending: "20.00", count: 1},
		{merchant: "Old", spending: "5.00", count: 1},
	}
	for i, expected := range want {
		if rows[i]["merchant"] != expected.merchant || rows[i]["spending"] != expected.spending || rows[i]["transaction_count"] != expected.count {
			t.Fatalf("merchant[%d] = %#v, want %#v", i, rows[i], expected)
		}
	}
	if got := countTable(t, db, "transactions"); got != beforeTransactions {
		t.Fatalf("list_top_merchants changed transaction count from %d to %d", beforeTransactions, got)
	}

	limited := callTool(t, session, "list_top_merchants", map[string]any{"limit": int64(1)})
	if limited.IsError {
		t.Fatalf("limited list_top_merchants failed: %s", structuredJSON(t, limited))
	}
	limitedPayload := structuredObject(t, limited)
	if limitedPayload["limit"] != float64(1) || limitedPayload["returned"] != float64(1) || limitedPayload["merchant_count"] != float64(5) || limitedPayload["total_spending"] != "130.00" || limitedPayload["transaction_count"] != float64(6) {
		t.Fatalf("limited totals = %s", structuredJSON(t, limited))
	}
	if limitedRows := topMerchantRows(t, limitedPayload); len(limitedRows) != 1 || limitedRows[0]["merchant"] != "Dining Place" {
		t.Fatalf("limited merchants = %#v", limitedRows)
	}

	maximum := callTool(t, session, "list_top_merchants", map[string]any{"limit": int64(50)})
	if maximum.IsError {
		t.Fatalf("maximum list_top_merchants failed: %s", structuredJSON(t, maximum))
	}
	maximumPayload := structuredObject(t, maximum)
	if maximumPayload["limit"] != float64(50) || maximumPayload["returned"] != float64(5) || len(topMerchantRows(t, maximumPayload)) != 5 {
		t.Fatalf("maximum page = %s", structuredJSON(t, maximum))
	}

	filtered := callTool(t, session, "list_top_merchants", map[string]any{
		"start_date": "2026-08-10",
		"end_date":   "2026-08-13",
		"category":   " groceries ",
		"limit":      int64(50),
	})
	if filtered.IsError {
		t.Fatalf("filtered list_top_merchants failed: %s", structuredJSON(t, filtered))
	}
	filteredPayload := structuredObject(t, filtered)
	if filteredPayload["start_date"] != "2026-08-10" || filteredPayload["end_date"] != "2026-08-13" || filteredPayload["category"] != "Groceries" || filteredPayload["total_spending"] != "60.00" || filteredPayload["transaction_count"] != float64(3) || filteredPayload["merchant_count"] != float64(3) || filteredPayload["returned"] != float64(3) {
		t.Fatalf("filtered result = %s", structuredJSON(t, filtered))
	}
	filteredRows := topMerchantRows(t, filteredPayload)
	if len(filteredRows) != 3 || filteredRows[0]["merchant"] != "Cafe" || filteredRows[1]["merchant"] != "Metro" || filteredRows[2]["merchant"] != "Shoppers" {
		t.Fatalf("filtered merchants = %#v", filteredRows)
	}

	inactive := callTool(t, session, "list_top_merchants", map[string]any{"category": "dining"})
	if inactive.IsError {
		t.Fatalf("inactive category filter failed: %s", structuredJSON(t, inactive))
	}
	inactivePayload := structuredObject(t, inactive)
	if inactivePayload["category"] != "Dining" || inactivePayload["total_spending"] != "50.00" || inactivePayload["transaction_count"] != float64(1) {
		t.Fatalf("inactive category result = %s", structuredJSON(t, inactive))
	}
}

func TestListTopMerchantsValidationAndCategoryNotFound(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
	groceries := createCategoryForMerchantTest(t, session, "Groceries")

	invalid := callTool(t, session, "list_top_merchants", map[string]any{
		"start_date": "2026-8-31",
		"end_date":   "2026/08/01",
		"category":   " ",
		"limit":      int64(0),
	})
	if !invalid.IsError {
		t.Fatal("invalid list_top_merchants IsError = false, want true")
	}
	requireStructuredEqual(t, invalid, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{
			"fields": []contract.FieldIssue{
				{Field: "start_date", Reason: "must be a valid YYYY-MM-DD date"},
				{Field: "end_date", Reason: "must be a valid YYYY-MM-DD date"},
				{Field: "category", Reason: "must not be empty"},
				{Field: "limit", Reason: "must be between 1 and 50"},
			},
		},
	)))

	reversed := callTool(t, session, "list_top_merchants", map[string]any{
		"start_date": "2026-08-31",
		"end_date":   "2026-08-01",
	})
	if !reversed.IsError {
		t.Fatal("reversed list_top_merchants IsError = false, want true")
	}
	requireStructuredEqual(t, reversed, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{"fields": []contract.FieldIssue{{Field: "end_date", Reason: "must be on or after start_date"}}},
	)))

	missing := callTool(t, session, "list_top_merchants", map[string]any{"category": " Pharmacy "})
	if !missing.IsError {
		t.Fatal("missing category IsError = false, want true")
	}
	requireStructuredEqual(t, missing, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeCategoryNotFound,
		"Category 'Pharmacy' does not exist.",
		false,
		map[string]any{
			"requested_category": "Pharmacy",
			"categories":         []contract.Category{groceries},
		},
	)))
}

func TestListTopMerchantsEmptyAndInternalError(t *testing.T) {
	emptyDB := openCategoryDB(t)
	emptySession := connectCategorySession(t, emptyDB, fixedTransactionNow, nil)
	empty := callTool(t, emptySession, "list_top_merchants", map[string]any{})
	if empty.IsError {
		t.Fatalf("empty list_top_merchants failed: %s", structuredJSON(t, empty))
	}
	emptyPayload := structuredObject(t, empty)
	if emptyPayload["total_spending"] != "0.00" || emptyPayload["transaction_count"] != float64(0) || emptyPayload["limit"] != float64(10) || emptyPayload["returned"] != float64(0) || emptyPayload["merchant_count"] != float64(0) {
		t.Fatalf("empty result = %s", structuredJSON(t, empty))
	}
	if merchants := topMerchantRows(t, emptyPayload); merchants == nil || len(merchants) != 0 {
		t.Fatalf("empty merchants = %#v, want []", merchants)
	}

	db := openCategoryDB(t)
	var logs bytes.Buffer
	session := connectCategorySession(t, db, fixedTransactionNow, log.New(&logs, "", 0))
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	result := callTool(t, session, "list_top_merchants", map[string]any{})
	if !result.IsError {
		t.Fatal("internal list_top_merchants IsError = false, want true")
	}
	requireStructuredEqual(t, result, contract.NewInternalErrorEnvelope())
	if leakedInternalError(structuredJSON(t, result)) || leakedInternalError(toolText(result)) {
		t.Fatalf("public payload leaked internal details: %s", structuredJSON(t, result))
	}
	if logs.Len() == 0 {
		t.Fatal("logger did not record the private cause")
	}
}

func topMerchantRows(t *testing.T, payload map[string]any) []map[string]any {
	t.Helper()
	raw, ok := payload["merchants"].([]any)
	if !ok || raw == nil {
		t.Fatalf("merchants = %#v, want array", payload["merchants"])
	}
	rows := make([]map[string]any, len(raw))
	for i, value := range raw {
		rows[i] = asObject(t, value)
	}
	return rows
}
