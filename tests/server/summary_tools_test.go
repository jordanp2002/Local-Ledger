package server_test

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSummaryToolDiscovery(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if got := listedToolNames(result.Tools); strings.Join(got, ",") != strings.Join(categoryToolNames, ",") {
		t.Fatalf("tools = %v, want %v", got, categoryToolNames)
	}
	if len(result.Tools) != 13 {
		t.Fatalf("tool count = %d, want 13", len(result.Tools))
	}

	monthly := toolByName(t, result.Tools, "get_monthly_summary")
	monthlySchema := schemaObject(t, monthly.InputSchema)
	if monthlySchema["type"] != "object" {
		t.Fatalf("get_monthly_summary input schema type = %v, want object", monthlySchema["type"])
	}
	monthlyRequired, _ := monthlySchema["required"].([]any)
	if !containsValue(monthlyRequired, "month") || len(monthlyRequired) != 1 {
		t.Fatalf("get_monthly_summary required = %v, want only month", monthlyRequired)
	}
	monthlyProperties, _ := monthlySchema["properties"].(map[string]any)
	if monthSchema, _ := monthlyProperties["month"].(map[string]any); monthSchema["type"] != "string" {
		t.Fatalf("get_monthly_summary month schema = %#v, want string", monthlyProperties["month"])
	}

	categoryTool := toolByName(t, result.Tools, "get_category_summary")
	categorySchema := schemaObject(t, categoryTool.InputSchema)
	categoryRequired, _ := categorySchema["required"].([]any)
	if !containsValue(categoryRequired, "category") || !containsValue(categoryRequired, "month") || len(categoryRequired) != 2 {
		t.Fatalf("get_category_summary required = %v, want category and month", categoryRequired)
	}
}

func TestGetMonthlySummarySuccess(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)
	dining := createCategoryForMerchantTest(t, session, "Dining")
	groceries := createCategoryForMerchantTest(t, session, "Groceries")
	createAugustBudget(t, session, []map[string]any{
		{"category": "Groceries", "amount": "500.00"},
		{"category": "Dining", "amount": "150.00"},
	})
	addTestTransaction(t, session, map[string]any{
		"amount": "90.00", "merchant": "Metro", "category": "Groceries", "date": "2026-08-14",
	})
	addTestTransaction(t, session, map[string]any{
		"amount": "30.00", "merchant": "Cafe", "category": "Dining", "date": "2026-08-15",
	})

	result := callTool(t, session, "get_monthly_summary", map[string]any{"month": "2026-08"})
	if result.IsError {
		t.Fatalf("get_monthly_summary failed: %s", structuredJSON(t, result))
	}
	got := structuredObject(t, result)
	if keys := objectKeys(got); strings.Join(keys, ",") != "categories,month,ok,remaining,total_budget,total_spending" {
		t.Fatalf("keys = %v", keys)
	}
	if got["ok"] != true || got["month"] != "2026-08" || got["total_budget"] != "650.00" || got["total_spending"] != "120.00" || got["remaining"] != "530.00" {
		t.Fatalf("totals = %s", structuredJSON(t, result))
	}
	rows := monthlyCategories(t, got)
	if len(rows) != 2 {
		t.Fatalf("categories = %#v", rows)
	}
	if rows[0]["category"] != "Dining" || rows[0]["budget"] != "150.00" || rows[0]["category_id"] != float64(dining.ID) {
		t.Fatalf("Dining = %#v", rows[0])
	}
	if rows[1]["category"] != "Groceries" || rows[1]["spending"] != "90.00" || rows[1]["category_id"] != float64(groceries.ID) {
		t.Fatalf("Groceries = %#v", rows[1])
	}

	categoryResult := callTool(t, session, "get_category_summary", map[string]any{"category": "Groceries", "month": "2026-08"})
	if categoryResult.IsError {
		t.Fatalf("get_category_summary failed: %s", structuredJSON(t, categoryResult))
	}
	categoryGot := structuredObject(t, categoryResult)
	if categoryGot["ok"] != true || categoryGot["category"] != "Groceries" || categoryGot["budget"] != "500.00" || categoryGot["total_spending"] != "90.00" || categoryGot["remaining"] != "410.00" || categoryGot["transaction_count"] != float64(1) {
		t.Fatalf("category summary = %s", structuredJSON(t, categoryResult))
	}
}

func TestGetMonthlySummaryInvalidInputAndMissingMonth(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)
	groceries := createCategoryForMerchantTest(t, session, "Groceries")
	createAugustBudget(t, session, []map[string]any{
		{"category": "Groceries", "amount": "500.00"},
	})

	invalid := callTool(t, session, "get_monthly_summary", map[string]any{"month": "2026-8"})
	if !invalid.IsError {
		t.Fatal("invalid month IsError = false, want true")
	}
	requireStructuredEqual(t, invalid, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{
			"fields": []contract.FieldIssue{{Field: "month", Reason: "must be a valid YYYY-MM month"}},
		},
	)))

	missing := callTool(t, session, "get_monthly_summary", map[string]any{"month": "2026-10"})
	if !missing.IsError {
		t.Fatal("missing month IsError = false, want true")
	}
	requireStructuredEqual(t, missing, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeMonthlyBudgetNotFound,
		"No monthly budget exists for 2026-10.",
		false,
		map[string]any{
			"month":                "2026-10",
			"latest_earlier_month": "2026-08",
		},
	)))
	_ = groceries
}

func TestGetCategorySummaryMissingCategoryAndUnbudgetedSpending(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)
	groceries := createCategoryForMerchantTest(t, session, "Groceries")
	health := createCategoryForMerchantTest(t, session, "Health")
	createAugustBudget(t, session, []map[string]any{
		{"category": "Groceries", "amount": "500.00"},
	})
	addTestTransaction(t, session, map[string]any{
		"amount": "25.00", "merchant": "Shoppers", "category": "Health", "date": "2026-08-11",
	})

	missing := callTool(t, session, "get_category_summary", map[string]any{"category": "  Pharmacy  ", "month": "2026-08"})
	if !missing.IsError {
		t.Fatal("category_not_found IsError = false, want true")
	}
	requireStructuredEqual(t, missing, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeCategoryNotFound,
		"Category 'Pharmacy' does not exist.",
		false,
		map[string]any{
			"requested_category": "Pharmacy",
			"categories":         []contract.Category{groceries, health},
		},
	)))

	unbudgeted := callTool(t, session, "get_category_summary", map[string]any{"category": "health", "month": "2026-08"})
	if unbudgeted.IsError {
		t.Fatalf("unbudgeted Health failed: %s", structuredJSON(t, unbudgeted))
	}
	got := structuredObject(t, unbudgeted)
	if got["category"] != "Health" || got["budget"] != "0.00" || got["total_spending"] != "25.00" || got["remaining"] != "-25.00" || got["transaction_count"] != float64(1) {
		t.Fatalf("unbudgeted Health = %s", structuredJSON(t, unbudgeted))
	}
}

func TestGetCategorySummaryInactiveIsSuccess(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)
	createCategoryForMerchantTest(t, session, "Dining")
	createCategoryForMerchantTest(t, session, "Groceries")
	createAugustBudget(t, session, []map[string]any{
		{"category": "Groceries", "amount": "500.00"},
		{"category": "Dining", "amount": "150.00"},
	})
	addTestTransaction(t, session, map[string]any{
		"amount": "40.00", "merchant": "Cafe", "category": "Dining", "date": "2026-08-10",
	})
	disabled := callTool(t, session, "disable_category", map[string]any{"name": "Dining"})
	if disabled.IsError {
		t.Fatalf("disable_category failed: %s", structuredJSON(t, disabled))
	}

	result := callTool(t, session, "get_category_summary", map[string]any{"category": "Dining", "month": "2026-08"})
	if result.IsError {
		t.Fatalf("inactive Dining summary failed: %s", structuredJSON(t, result))
	}
	got := structuredObject(t, result)
	if got["category"] != "Dining" || got["budget"] != "0.00" || got["total_spending"] != "40.00" || got["remaining"] != "-40.00" {
		t.Fatalf("inactive Dining after current-month disable = %s", structuredJSON(t, result))
	}

	monthly := callTool(t, session, "get_monthly_summary", map[string]any{"month": "2026-08"})
	if monthly.IsError {
		t.Fatalf("monthly after disable failed: %s", structuredJSON(t, monthly))
	}
	rows := monthlyCategories(t, structuredObject(t, monthly))
	if len(rows) != 2 || rows[0]["category"] != "Dining" || rows[0]["budget"] != "0.00" {
		t.Fatalf("monthly after disable = %#v", rows)
	}
}

func TestGetMonthlySummaryInternalError(t *testing.T) {
	db := openCategoryDB(t)
	var logs bytes.Buffer
	session := connectCategorySession(t, db, fixedBudgetNow, log.New(&logs, "", 0))
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	result := callTool(t, session, "get_monthly_summary", map[string]any{"month": "2026-08"})
	if !result.IsError {
		t.Fatal("internal get_monthly_summary IsError = false, want true")
	}
	requireStructuredEqual(t, result, contract.NewInternalErrorEnvelope())
	if leakedInternalError(structuredJSON(t, result)) || leakedInternalError(toolText(result)) {
		t.Fatalf("public payload leaked internal details: %s", structuredJSON(t, result))
	}
	if logs.Len() == 0 {
		t.Fatal("logger did not record the private cause")
	}
}

func createAugustBudget(t *testing.T, session *mcp.ClientSession, allocations []map[string]any) {
	t.Helper()
	result := callTool(t, session, "create_monthly_budget", map[string]any{
		"month":   "2026-08",
		"budgets": allocations,
	})
	if result.IsError {
		t.Fatalf("create_monthly_budget failed: %s", structuredJSON(t, result))
	}
}

func monthlyCategories(t *testing.T, payload map[string]any) []map[string]any {
	t.Helper()
	raw, ok := payload["categories"].([]any)
	if !ok || raw == nil {
		t.Fatalf("categories = %#v, want array", payload["categories"])
	}
	rows := make([]map[string]any, len(raw))
	for i, value := range raw {
		rows[i] = asObject(t, value)
	}
	return rows
}
