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
	if len(result.Tools) != 20 {
		t.Fatalf("tool count = %d, want 20", len(result.Tools))
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

	compareTool := toolByName(t, result.Tools, "compare_months")
	compareSchema := schemaObject(t, compareTool.InputSchema)
	compareRequired, _ := compareSchema["required"].([]any)
	if !containsValue(compareRequired, "from_month") || !containsValue(compareRequired, "to_month") || len(compareRequired) != 2 {
		t.Fatalf("compare_months required = %v, want from_month and to_month", compareRequired)
	}
	compareProperties, _ := compareSchema["properties"].(map[string]any)
	for _, field := range []string{"from_month", "to_month"} {
		fieldSchema, _ := compareProperties[field].(map[string]any)
		if fieldSchema["type"] != "string" {
			t.Fatalf("compare_months %s schema = %#v, want string", field, compareProperties[field])
		}
	}

	spending := toolByName(t, result.Tools, "get_spending_summary")
	spendingSchema := schemaObject(t, spending.InputSchema)
	if spendingSchema["type"] != "object" {
		t.Fatalf("get_spending_summary input schema type = %v, want object", spendingSchema["type"])
	}
	spendingRequired, _ := spendingSchema["required"].([]any)
	if len(spendingRequired) != 0 {
		t.Fatalf("get_spending_summary required = %v, want no required fields", spendingRequired)
	}
	spendingProperties, _ := spendingSchema["properties"].(map[string]any)
	for _, field := range []string{"start_date", "end_date", "category", "merchant"} {
		property, _ := spendingProperties[field].(map[string]any)
		if property == nil || !schemaTypeContains(property["type"], "string") {
			t.Fatalf("get_spending_summary %s schema = %#v, want string", field, spendingProperties[field])
		}
	}
}

func TestCompareMonthsSuccess(t *testing.T) {
	db := openCategoryDB(t)
	session := connectCategorySession(t, db, fixedBudgetNow, nil)
	dining := createCategoryForMerchantTest(t, session, "Dining")
	groceries := createCategoryForMerchantTest(t, session, "Groceries")
	health := createCategoryForMerchantTest(t, session, "Health")
	insertCurrentMonthBudget(t, db, groceries.ID, "2026-07", 50000)
	insertCurrentMonthBudget(t, db, dining.ID, "2026-07", 10000)
	createAugustBudget(t, session, []map[string]any{
		{"category": "Groceries", "amount": "450.00"},
	})
	addTestTransaction(t, session, map[string]any{
		"amount": "90.00", "merchant": "Metro", "category": "Groceries", "date": "2026-07-10",
	})
	addTestTransaction(t, session, map[string]any{
		"amount": "120.00", "merchant": "Metro", "category": "Groceries", "date": "2026-08-10",
	})
	addTestTransaction(t, session, map[string]any{
		"amount": "20.00", "merchant": "Shoppers", "category": "Health", "date": "2026-08-11",
	})
	addTestTransaction(t, session, map[string]any{
		"amount": "30.00", "merchant": "Cafe", "category": "Dining", "date": "2026-07-12",
	})
	if _, err := db.ExecContext(context.Background(), `UPDATE categories SET name = ? WHERE id = ?`, "Food & Dining", dining.ID); err != nil {
		t.Fatalf("rename Dining: %v", err)
	}

	result := callTool(t, session, "compare_months", map[string]any{
		"from_month": "2026-07",
		"to_month":   "2026-08",
	})
	if result.IsError {
		t.Fatalf("compare_months failed: %s", structuredJSON(t, result))
	}
	got := structuredObject(t, result)
	if keys := objectKeys(got); strings.Join(keys, ",") != "categories,change,from,ok,to" {
		t.Fatalf("keys = %v", keys)
	}
	if got["ok"] != true {
		t.Fatalf("ok = %v, want true", got["ok"])
	}
	from := objectField(t, got, "from")
	if from["month"] != "2026-07" || from["total_budget"] != "600.00" || from["total_spending"] != "120.00" || from["remaining"] != "480.00" {
		t.Fatalf("from = %#v", from)
	}
	to := objectField(t, got, "to")
	if to["month"] != "2026-08" || to["total_budget"] != "450.00" || to["total_spending"] != "140.00" || to["remaining"] != "310.00" {
		t.Fatalf("to = %#v", to)
	}
	change := objectField(t, got, "change")
	if change["total_budget"] != "-150.00" || change["total_spending"] != "20.00" || change["remaining"] != "-170.00" {
		t.Fatalf("change = %#v", change)
	}
	rows := comparisonCategories(t, got)
	if len(rows) != 3 {
		t.Fatalf("categories = %#v, want three rows", rows)
	}
	if rows[0]["category"] != "Food & Dining" || rows[0]["category_id"] != float64(dining.ID) || rows[0]["from_budget"] != "100.00" || rows[0]["to_budget"] != "0.00" || rows[0]["budget_change"] != "-100.00" || rows[0]["from_spending"] != "30.00" || rows[0]["to_spending"] != "0.00" || rows[0]["spending_change"] != "-30.00" {
		t.Fatalf("Food & Dining = %#v", rows[0])
	}
	if rows[1]["category"] != "Groceries" || rows[1]["category_id"] != float64(groceries.ID) || rows[1]["budget_change"] != "-50.00" || rows[1]["spending_change"] != "30.00" {
		t.Fatalf("Groceries = %#v", rows[1])
	}
	if rows[2]["category"] != "Health" || rows[2]["category_id"] != float64(health.ID) || rows[2]["from_budget"] != "0.00" || rows[2]["to_budget"] != "0.00" || rows[2]["from_spending"] != "0.00" || rows[2]["to_spending"] != "20.00" || rows[2]["spending_change"] != "20.00" {
		t.Fatalf("Health = %#v", rows[2])
	}
}

func TestCompareMonthsValidationMissingAndEmpty(t *testing.T) {
	db := openCategoryDB(t)
	session := connectCategorySession(t, db, fixedBudgetNow, nil)
	groceries := createCategoryForMerchantTest(t, session, "Groceries")
	insertCurrentMonthBudget(t, db, groceries.ID, "2026-07", 0)
	insertCurrentMonthBudget(t, db, groceries.ID, "2026-08", 0)

	invalid := callTool(t, session, "compare_months", map[string]any{
		"from_month": "2026-7",
		"to_month":   "2026-13",
	})
	if !invalid.IsError {
		t.Fatal("invalid compare_months IsError = false, want true")
	}
	requireStructuredEqual(t, invalid, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{
			"fields": []contract.FieldIssue{
				{Field: "from_month", Reason: "must be a valid YYYY-MM month"},
				{Field: "to_month", Reason: "must be a valid YYYY-MM month"},
			},
		},
	)))

	for _, months := range []map[string]any{
		{"from_month": "2026-08", "to_month": "2026-08"},
		{"from_month": "2026-08", "to_month": "2026-07"},
	} {
		relationship := callTool(t, session, "compare_months", months)
		if !relationship.IsError {
			t.Fatalf("relationship input %#v IsError = false, want true", months)
		}
		requireStructuredEqual(t, relationship, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeInvalidInput,
			"",
			false,
			map[string]any{"fields": []contract.FieldIssue{{Field: "to_month", Reason: "must be later than from_month"}}},
		)))
	}

	missing := callTool(t, session, "compare_months", map[string]any{
		"from_month": "2026-08",
		"to_month":   "2026-09",
	})
	if !missing.IsError {
		t.Fatal("missing to_month snapshot IsError = false, want true")
	}
	requireStructuredEqual(t, missing, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeMonthlyBudgetNotFound,
		"No monthly budget exists for 2026-09.",
		false,
		map[string]any{"month": "2026-09", "latest_earlier_month": "2026-08"},
	)))

	empty := callTool(t, session, "compare_months", map[string]any{
		"from_month": "2026-07",
		"to_month":   "2026-08",
	})
	if empty.IsError {
		t.Fatalf("empty compare_months failed: %s", structuredJSON(t, empty))
	}
	rows := comparisonCategories(t, structuredObject(t, empty))
	if rows == nil || len(rows) != 0 {
		t.Fatalf("empty categories = %#v, want non-nil empty slice", rows)
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
	if keys := objectKeys(got); strings.Join(keys, ",") != "categories,month,ok,remaining,spent_of_budget,total_budget,total_spending" {
		t.Fatalf("keys = %v", keys)
	}
	if got["ok"] != true || got["month"] != "2026-08" || got["total_budget"] != "650.00" || got["total_spending"] != "120.00" || got["remaining"] != "530.00" || got["spent_of_budget"] != "18.46" {
		t.Fatalf("totals = %s", structuredJSON(t, result))
	}
	rows := monthlyCategories(t, got)
	if len(rows) != 2 {
		t.Fatalf("categories = %#v", rows)
	}
	if keys := objectKeys(rows[0]); strings.Join(keys, ",") != "budget,category,category_id,remaining,spending,spent_of_budget" {
		t.Fatalf("category row keys = %v", keys)
	}
	if rows[0]["category"] != "Dining" || rows[0]["budget"] != "150.00" || rows[0]["category_id"] != float64(dining.ID) || rows[0]["spent_of_budget"] != "20.00" {
		t.Fatalf("Dining = %#v", rows[0])
	}
	if rows[1]["category"] != "Groceries" || rows[1]["spending"] != "90.00" || rows[1]["category_id"] != float64(groceries.ID) || rows[1]["spent_of_budget"] != "18.00" {
		t.Fatalf("Groceries = %#v", rows[1])
	}

	categoryResult := callTool(t, session, "get_category_summary", map[string]any{"category": "Groceries", "month": "2026-08"})
	if categoryResult.IsError {
		t.Fatalf("get_category_summary failed: %s", structuredJSON(t, categoryResult))
	}
	categoryGot := structuredObject(t, categoryResult)
	if keys := objectKeys(categoryGot); strings.Join(keys, ",") != "budget,category,category_id,month,ok,remaining,spent_of_budget,total_spending,transaction_count" {
		t.Fatalf("category keys = %v", keys)
	}
	if categoryGot["ok"] != true || categoryGot["category"] != "Groceries" || categoryGot["budget"] != "500.00" || categoryGot["total_spending"] != "90.00" || categoryGot["remaining"] != "410.00" || categoryGot["spent_of_budget"] != "18.00" || categoryGot["transaction_count"] != float64(1) {
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
	if got["category"] != "Health" || got["budget"] != "0.00" || got["total_spending"] != "25.00" || got["remaining"] != "-25.00" || got["transaction_count"] != float64(1) || got["spent_of_budget"] != nil {
		t.Fatalf("unbudgeted Health = %s", structuredJSON(t, unbudgeted))
	}
	if keys := objectKeys(got); strings.Join(keys, ",") != "budget,category,category_id,month,ok,remaining,spent_of_budget,total_spending,transaction_count" {
		t.Fatalf("unbudgeted keys = %v, want spent_of_budget present as null", keys)
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

func TestGetSpendingSummarySuccess(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)
	dining := createCategoryForMerchantTest(t, session, "Dining")
	groceries := createCategoryForMerchantTest(t, session, "Groceries")
	addTestTransaction(t, session, map[string]any{
		"amount": "1.00", "merchant": "Jul", "category": "Groceries", "date": "2026-07-31",
	})
	addTestTransaction(t, session, map[string]any{
		"amount": "20.00", "merchant": "Metro", "category": "Groceries", "date": "2026-08-14",
	})
	addTestTransaction(t, session, map[string]any{
		"amount": "15.00", "merchant": "Cafe", "category": "Dining", "date": "2026-08-14",
	})
	addTestTransaction(t, session, map[string]any{
		"amount": "12.00", "merchant": "Metro Grocery", "category": "Groceries", "date": "2026-08-13",
	})

	allTime := callTool(t, session, "get_spending_summary", map[string]any{})
	if allTime.IsError {
		t.Fatalf("all-time spending failed: %s", structuredJSON(t, allTime))
	}
	got := structuredObject(t, allTime)
	if keys := objectKeys(got); strings.Join(keys, ",") != "categories,category,end_date,merchant,ok,start_date,total_spending,transaction_count" {
		t.Fatalf("keys = %v", keys)
	}
	if got["ok"] != true || got["total_spending"] != "48.00" || got["transaction_count"] != float64(4) {
		t.Fatalf("all-time totals = %s", structuredJSON(t, allTime))
	}
	if got["start_date"] != nil || got["end_date"] != nil || got["category"] != nil || got["merchant"] != nil {
		t.Fatalf("omitted filters = %s, want nulls", structuredJSON(t, allTime))
	}
	rows := monthlyCategories(t, got)
	if len(rows) != 2 || rows[0]["category"] != "Dining" || rows[0]["category_id"] != float64(dining.ID) || rows[0]["spending"] != "15.00" {
		t.Fatalf("all-time rows = %#v, want Dining then Groceries", rows)
	}
	if rows[1]["category"] != "Groceries" || rows[1]["category_id"] != float64(groceries.ID) || rows[1]["spending"] != "33.00" || rows[1]["transaction_count"] != float64(3) {
		t.Fatalf("Groceries row = %#v", rows[1])
	}

	filtered := callTool(t, session, "get_spending_summary", map[string]any{
		"start_date": "2026-08-01",
		"end_date":   "2026-08-31",
		"category":   "groceries",
		"merchant":   "metro",
	})
	if filtered.IsError {
		t.Fatalf("filtered spending failed: %s", structuredJSON(t, filtered))
	}
	filteredGot := structuredObject(t, filtered)
	if filteredGot["ok"] != true || filteredGot["total_spending"] != "20.00" || filteredGot["transaction_count"] != float64(1) {
		t.Fatalf("filtered totals = %s", structuredJSON(t, filtered))
	}
	if filteredGot["start_date"] != "2026-08-01" || filteredGot["end_date"] != "2026-08-31" || filteredGot["category"] != "Groceries" || filteredGot["merchant"] != "metro" {
		t.Fatalf("echoed filters = %s", structuredJSON(t, filtered))
	}
	filteredRows := monthlyCategories(t, filteredGot)
	if len(filteredRows) != 1 || filteredRows[0]["category"] != "Groceries" || filteredRows[0]["spending"] != "20.00" {
		t.Fatalf("filtered rows = %#v, want exact Metro Groceries", filteredRows)
	}

	empty := callTool(t, session, "get_spending_summary", map[string]any{"merchant": "Unknown Store"})
	if empty.IsError {
		t.Fatalf("unknown merchant IsError = true, want success: %s", structuredJSON(t, empty))
	}
	emptyGot := structuredObject(t, empty)
	if emptyGot["total_spending"] != "0.00" || emptyGot["transaction_count"] != float64(0) || emptyGot["merchant"] != "Unknown Store" {
		t.Fatalf("unknown merchant = %s, want zeros", structuredJSON(t, empty))
	}
	emptyRows, ok := emptyGot["categories"].([]any)
	if !ok || emptyRows == nil || len(emptyRows) != 0 {
		t.Fatalf("unknown merchant categories = %#v, want []", emptyGot["categories"])
	}
}

func TestGetSpendingSummaryInvalidInputAndMissingCategory(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)
	groceries := createCategoryForMerchantTest(t, session, "Groceries")

	invalid := callTool(t, session, "get_spending_summary", map[string]any{
		"start_date": "2026-08-31",
		"end_date":   "2026-08-01",
		"merchant":   "   ",
	})
	if !invalid.IsError {
		t.Fatal("invalid spending IsError = false, want true")
	}
	requireStructuredEqual(t, invalid, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{
			"fields": []contract.FieldIssue{
				{Field: "end_date", Reason: "must be on or after start_date"},
				{Field: "merchant", Reason: "must not be empty"},
			},
		},
	)))

	missing := callTool(t, session, "get_spending_summary", map[string]any{"category": "  Pharmacy  "})
	if !missing.IsError {
		t.Fatal("category_not_found IsError = false, want true")
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

func TestGetSpendingSummaryInternalError(t *testing.T) {
	db := openCategoryDB(t)
	var logs bytes.Buffer
	session := connectCategorySession(t, db, fixedBudgetNow, log.New(&logs, "", 0))
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	result := callTool(t, session, "get_spending_summary", map[string]any{})
	if !result.IsError {
		t.Fatal("internal get_spending_summary IsError = false, want true")
	}
	requireStructuredEqual(t, result, contract.NewInternalErrorEnvelope())
	if leakedInternalError(structuredJSON(t, result)) || leakedInternalError(toolText(result)) {
		t.Fatalf("public payload leaked internal details: %s", structuredJSON(t, result))
	}
	if logs.Len() == 0 {
		t.Fatal("logger did not record the private cause")
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

func TestCompareMonthsInternalError(t *testing.T) {
	db := openCategoryDB(t)
	var logs bytes.Buffer
	session := connectCategorySession(t, db, fixedBudgetNow, log.New(&logs, "", 0))
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	result := callTool(t, session, "compare_months", map[string]any{
		"from_month": "2026-07",
		"to_month":   "2026-08",
	})
	if !result.IsError {
		t.Fatal("internal compare_months IsError = false, want true")
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

func comparisonCategories(t *testing.T, payload map[string]any) []map[string]any {
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
