package server_test

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

func TestGetMonthlySeriesToolSchemaAndAnnotations(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	tool := toolByName(t, result.Tools, "get_monthly_series")
	schema := schemaObject(t, tool.InputSchema)
	if schema["type"] != "object" {
		t.Fatalf("input schema type = %v, want object", schema["type"])
	}
	required, _ := schema["required"].([]any)
	if len(required) != 2 || !containsValue(required, "from_month") || !containsValue(required, "to_month") {
		t.Fatalf("required = %v, want only from_month and to_month", required)
	}
	properties, _ := schema["properties"].(map[string]any)
	for _, field := range []string{"from_month", "to_month", "category"} {
		property, _ := properties[field].(map[string]any)
		if property == nil || !schemaTypeContains(property["type"], "string") {
			t.Fatalf("%s schema = %#v, want string", field, properties[field])
		}
	}
	includeCategories, _ := properties["include_categories"].(map[string]any)
	if includeCategories == nil || !schemaTypeContains(includeCategories["type"], "boolean") {
		t.Fatalf("include_categories schema = %#v, want boolean", properties["include_categories"])
	}
	if containsValue(required, "category") {
		t.Fatal("category is required, want optional")
	}
	if tool.Annotations == nil {
		t.Fatal("annotations are nil")
	}
	if !tool.Annotations.ReadOnlyHint {
		t.Fatal("readOnlyHint = false, want true")
	}
	if tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
		t.Fatalf("openWorldHint = %v, want explicit false", tool.Annotations.OpenWorldHint)
	}
	if tool.Annotations.DestructiveHint != nil {
		t.Fatalf("destructiveHint = %v, want omitted", *tool.Annotations.DestructiveHint)
	}
}

func TestGetMonthlySeriesSuccessContiguousNullsAndNoWrite(t *testing.T) {
	db := openCategoryDB(t)
	session := connectCategorySession(t, db, fixedBudgetNow, nil)
	groceries := createCategoryForMerchantTest(t, session, "Groceries")
	dining := createCategoryForMerchantTest(t, session, "Dining")
	createCategoryForMerchantTest(t, session, "Health")
	insertCurrentMonthBudget(t, db, groceries.ID, "2026-01", 50000)
	insertCurrentMonthBudget(t, db, dining.ID, "2026-03", 10000)
	addTestTransaction(t, session, map[string]any{
		"amount": "90.00", "merchant": "Metro", "category": "Groceries", "date": "2026-01-10",
	})
	addTestTransaction(t, session, map[string]any{
		"amount": "20.00", "merchant": "Shoppers", "category": "Health", "date": "2026-01-11",
	})
	addTestTransaction(t, session, map[string]any{
		"amount": "20.00", "merchant": "Shoppers", "category": "Health", "date": "2026-02-11",
	})
	addTestTransaction(t, session, map[string]any{
		"amount": "5.00", "merchant": "Shoppers", "category": "Health", "date": "2026-04-11",
	})

	beforeTransactions := countTable(t, db, "transactions")
	beforeBudgets := countTable(t, db, "budgets")
	result := callTool(t, session, "get_monthly_series", map[string]any{
		"from_month": "2026-01",
		"to_month":   "2026-04",
	})
	if result.IsError {
		t.Fatalf("get_monthly_series failed: %s", structuredJSON(t, result))
	}
	got := structuredObject(t, result)
	if keys := objectKeys(got); strings.Join(keys, ",") != "category,from_month,include_categories,months,ok,to_month" {
		t.Fatalf("keys = %v", keys)
	}
	if got["ok"] != true || got["from_month"] != "2026-01" || got["to_month"] != "2026-04" || got["category"] != nil {
		t.Fatalf("header = %s", structuredJSON(t, result))
	}
	rows := monthlySeriesRows(t, got)
	if len(rows) != 4 {
		t.Fatalf("months = %#v, want four contiguous rows", rows)
	}
	assertMonthlySeriesRow(t, rows[0], "2026-01", "500.00", "110.00", "390.00", "22.00", 2)
	assertMonthlySeriesRow(t, rows[1], "2026-02", nil, "20.00", nil, nil, 1)
	assertMonthlySeriesRow(t, rows[2], "2026-03", "100.00", "0.00", "100.00", "0.00", 0)
	assertMonthlySeriesRow(t, rows[3], "2026-04", nil, "5.00", nil, nil, 1)
	if got := countTable(t, db, "transactions"); got != beforeTransactions {
		t.Fatalf("transactions changed from %d to %d", beforeTransactions, got)
	}
	if got := countTable(t, db, "budgets"); got != beforeBudgets {
		t.Fatalf("budgets changed from %d to %d", beforeBudgets, got)
	}
}

func TestGetMonthlySeriesInactiveCategoryAndFutureMonths(t *testing.T) {
	db := openCategoryDB(t)
	session := connectCategorySession(t, db, fixedBudgetNow, nil)
	dining := createCategoryForMerchantTest(t, session, "Dining")
	addTestTransaction(t, session, map[string]any{
		"amount": "40.00", "merchant": "Cafe", "category": "Dining", "date": "2026-07-10",
	})
	disabled := callTool(t, session, "disable_category", map[string]any{"name": "Dining"})
	if disabled.IsError {
		t.Fatalf("disable_category failed: %s", structuredJSON(t, disabled))
	}
	insertCurrentMonthBudget(t, db, dining.ID, "2026-07", 15000)

	result := callTool(t, session, "get_monthly_series", map[string]any{
		"from_month": "2026-07",
		"to_month":   "2026-08",
		"category":   " dining ",
	})
	if result.IsError {
		t.Fatalf("inactive category series failed: %s", structuredJSON(t, result))
	}
	got := structuredObject(t, result)
	if got["category"] != "Dining" {
		t.Fatalf("category = %v, want stored name Dining", got["category"])
	}
	rows := monthlySeriesRows(t, got)
	if len(rows) != 2 {
		t.Fatalf("months = %#v, want two rows", rows)
	}
	assertMonthlySeriesRow(t, rows[0], "2026-07", "150.00", "40.00", "110.00", "26.66", 1)
	assertMonthlySeriesRow(t, rows[1], "2026-08", nil, "0.00", nil, nil, 0)

	future := callTool(t, session, "get_monthly_series", map[string]any{
		"from_month": "2027-01",
		"to_month":   "2027-02",
	})
	if future.IsError {
		t.Fatalf("future series failed: %s", structuredJSON(t, future))
	}
	futureRows := monthlySeriesRows(t, structuredObject(t, future))
	if len(futureRows) != 2 {
		t.Fatalf("future months = %#v, want two rows", futureRows)
	}
	for i, row := range futureRows {
		month := "2027-01"
		if i == 1 {
			month = "2027-02"
		}
		assertMonthlySeriesRow(t, row, month, nil, "0.00", nil, nil, 0)
	}
}

func TestGetMonthlySeriesValidationAndCategoryNotFound(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)
	for _, test := range []struct {
		name string
		args map[string]any
		want []contract.FieldIssue
	}{
		{
			name: "validation order",
			args: map[string]any{
				"from_month": "2026-8",
				"to_month":   "2026-13",
				"category":   " ",
			},
			want: []contract.FieldIssue{
				{Field: "from_month", Reason: "must be a valid YYYY-MM month"},
				{Field: "to_month", Reason: "must be a valid YYYY-MM month"},
				{Field: "category", Reason: "must not be empty"},
			},
		},
		{
			name: "reversed before category",
			args: map[string]any{
				"from_month": "2026-08",
				"to_month":   "2026-07",
				"category":   " ",
			},
			want: []contract.FieldIssue{
				{Field: "to_month", Reason: "must be on or after from_month"},
				{Field: "category", Reason: "must not be empty"},
			},
		},
		{
			name: "25 month span before category",
			args: map[string]any{
				"from_month": "2025-01",
				"to_month":   "2027-01",
				"category":   "Pharmacy\x00",
			},
			want: []contract.FieldIssue{
				{Field: "to_month", Reason: "must be at most 24 months after from_month"},
				{Field: "category", Reason: "must not contain NUL characters"},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := callTool(t, session, "get_monthly_series", test.args)
			if !result.IsError {
				t.Fatal("IsError = false, want true")
			}
			requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
				contract.ErrorCodeInvalidInput,
				"",
				false,
				map[string]any{"fields": test.want},
			)))
		})
	}

	missing := callTool(t, session, "get_monthly_series", map[string]any{
		"from_month": "2026-08",
		"to_month":   "2026-08",
		"category":   " Pharmacy ",
	})
	if !missing.IsError {
		t.Fatal("missing category IsError = false, want true")
	}
	requireStructuredEqual(t, missing, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeCategoryNotFound,
		"Category 'Pharmacy' does not exist.",
		false,
		map[string]any{
			"requested_category": "Pharmacy",
			"categories":         []contract.Category{},
		},
	)))
}

func TestGetMonthlySeriesAllows24MonthsAndRejects25(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)

	allowed := callTool(t, session, "get_monthly_series", map[string]any{
		"from_month": "2024-01",
		"to_month":   "2025-12",
	})
	if allowed.IsError {
		t.Fatalf("24-month series failed: %s", structuredJSON(t, allowed))
	}
	if rows := monthlySeriesRows(t, structuredObject(t, allowed)); len(rows) != 24 {
		t.Fatalf("24-month rows = %d, want 24", len(rows))
	}

	rejected := callTool(t, session, "get_monthly_series", map[string]any{
		"from_month": "2024-01",
		"to_month":   "2026-01",
	})
	if !rejected.IsError {
		t.Fatal("25-month IsError = false, want true")
	}
	requireStructuredEqual(t, rejected, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{
			"fields": []contract.FieldIssue{{Field: "to_month", Reason: "must be at most 24 months after from_month"}},
		},
	)))
}

func TestGetMonthlySeriesInternalErrorRedacted(t *testing.T) {
	db := openCategoryDB(t)
	var logs bytes.Buffer
	session := connectCategorySession(t, db, fixedBudgetNow, log.New(&logs, "", 0))
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	result := callTool(t, session, "get_monthly_series", map[string]any{
		"from_month": "2026-08",
		"to_month":   "2026-08",
	})
	if !result.IsError {
		t.Fatal("internal get_monthly_series IsError = false, want true")
	}
	requireStructuredEqual(t, result, contract.NewInternalErrorEnvelope())
	if leakedInternalError(structuredJSON(t, result)) || leakedInternalError(toolText(result)) {
		t.Fatalf("public payload leaked internal details: %s", structuredJSON(t, result))
	}
	if logs.Len() == 0 {
		t.Fatal("logger did not record the private cause")
	}
}

func monthlySeriesRows(t *testing.T, payload map[string]any) []map[string]any {
	t.Helper()
	raw, ok := payload["months"].([]any)
	if !ok || raw == nil {
		t.Fatalf("months = %#v, want non-nil array", payload["months"])
	}
	rows := make([]map[string]any, len(raw))
	for i, value := range raw {
		rows[i] = asObject(t, value)
	}
	return rows
}

func assertMonthlySeriesRow(t *testing.T, row map[string]any, month string, budget, spending, remaining, spent any, count float64) {
	t.Helper()
	if row["month"] != month || row["total_budget"] != budget || row["total_spending"] != spending || row["remaining"] != remaining || row["spent_of_budget"] != spent || row["transaction_count"] != count {
		t.Fatalf("row = %#v, want month=%s budget=%v spending=%v remaining=%v spent=%v count=%v", row, month, budget, spending, remaining, spent, count)
	}
}
