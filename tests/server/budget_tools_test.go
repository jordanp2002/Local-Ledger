package server_test

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCreateMonthlyBudgetToolDiscovery(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if got := listedToolNames(result.Tools); strings.Join(got, ",") != strings.Join(categoryToolNames, ",") {
		t.Fatalf("tools = %v, want %v", got, categoryToolNames)
	}

	var tool *mcp.Tool
	for _, candidate := range result.Tools {
		if candidate.Name == "create_monthly_budget" {
			tool = candidate
			break
		}
	}
	if tool == nil {
		t.Fatal("create_monthly_budget is not discoverable")
	}
	schema := schemaObject(t, tool.InputSchema)
	if schema["type"] != "object" {
		t.Fatalf("input schema type = %v, want object", schema["type"])
	}
	required, _ := schema["required"].([]any)
	if !containsValue(required, "month") || !containsValue(required, "budgets") {
		t.Fatalf("required = %v, want month and budgets", required)
	}
	properties, _ := schema["properties"].(map[string]any)
	if _, ok := properties["carry_forward"]; ok {
		t.Fatal("schema advertises deferred carry_forward input")
	}
	if _, ok := properties["overrides"]; ok {
		t.Fatal("schema advertises deferred overrides input")
	}
	monthSchema, _ := properties["month"].(map[string]any)
	if monthSchema["type"] != "string" {
		t.Fatalf("month schema type = %v, want string", monthSchema["type"])
	}
	budgetsSchema, _ := properties["budgets"].(map[string]any)
	if !schemaTypeContains(budgetsSchema["type"], "array") {
		t.Fatalf("budgets schema type = %#v, want array", budgetsSchema["type"])
	}
	items, _ := budgetsSchema["items"].(map[string]any)
	if !schemaTypeContains(items["type"], "object") {
		t.Fatalf("budget item schema type = %#v, want object", items["type"])
	}
	itemRequired, _ := items["required"].([]any)
	if !containsValue(itemRequired, "category") || !containsValue(itemRequired, "amount") {
		t.Fatalf("budget item required = %v, want category and amount", itemRequired)
	}
}

func TestCreateMonthlyBudgetSuccess(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)
	dining := createCategoryForMerchantTest(t, session, "Dining")
	groceries := createCategoryForMerchantTest(t, session, "Groceries")

	result := callTool(t, session, "create_monthly_budget", map[string]any{
		"month": "2026-08",
		"budgets": []map[string]any{
			{"category": " groceries ", "amount": "500"},
			{"category": "Dining", "amount": "150.0"},
		},
	})
	if result.IsError {
		t.Fatalf("create_monthly_budget failed: %s", structuredJSON(t, result))
	}
	got := structuredObject(t, result)
	if keys := objectKeys(got); strings.Join(keys, ",") != "budgets,creation_mode,month,ok,source_month,total_budget" {
		t.Fatalf("create_monthly_budget keys = %v", keys)
	}
	if got["ok"] != true || got["month"] != "2026-08" || got["creation_mode"] != "explicit" {
		t.Fatalf("success metadata = %s", structuredJSON(t, result))
	}
	if got["source_month"] != nil {
		t.Fatalf("source_month = %v, want null", got["source_month"])
	}
	if got["total_budget"] != "650.00" {
		t.Fatalf("total_budget = %v, want 650.00", got["total_budget"])
	}
	budgets, ok := got["budgets"].([]any)
	if !ok || len(budgets) != 2 {
		t.Fatalf("budgets = %#v, want two rows", got["budgets"])
	}
	first := decodeBudget(t, budgets[0])
	second := decodeBudget(t, budgets[1])
	if first.Category != dining.Name || first.CategoryID != dining.ID || first.Amount != "150.00" {
		t.Fatalf("first budget = %#v, want Dining/150.00", first)
	}
	if second.Category != groceries.Name || second.CategoryID != groceries.ID || second.Amount != "500.00" {
		t.Fatalf("second budget = %#v, want Groceries/500.00", second)
	}
}

func TestCreateMonthlyBudgetValidation(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)
	result := callTool(t, session, "create_monthly_budget", map[string]any{
		"month": "2026-8",
		"budgets": []map[string]any{
			{"category": " \x00 ", "amount": "-1"},
			{"category": "Groceries", "amount": "1.001"},
		},
	})
	if !result.IsError {
		t.Fatal("invalid create_monthly_budget IsError = false, want true")
	}
	requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{
			"fields": []contract.FieldIssue{
				{Field: "month", Reason: "must be a valid YYYY-MM month"},
				{Field: "budgets[0].category", Reason: "must not contain NUL characters"},
				{Field: "budgets[0].amount", Reason: "must be a non-negative amount with at most two decimal places"},
				{Field: "budgets[1].amount", Reason: "must be a non-negative amount with at most two decimal places"},
			},
		},
	)))
}

func TestCreateMonthlyBudgetDomainErrors(t *testing.T) {
	db := openCategoryDB(t)
	session := connectCategorySession(t, db, fixedBudgetNow, nil)
	groceries := createCategoryForMerchantTest(t, session, "Groceries")
	health := createCategoryForMerchantTest(t, session, "Health")

	missing := callTool(t, session, "create_monthly_budget", map[string]any{
		"month":   "2026-08",
		"budgets": []map[string]any{{"category": " Pharmacy ", "amount": "10.00"}},
	})
	requireStructuredEqual(t, missing, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeCategoryNotFound,
		"Category 'Pharmacy' does not exist.",
		false,
		map[string]any{
			"requested_category": "Pharmacy",
			"categories":         []contract.Category{groceries, health},
		},
	)))

	disabled := callTool(t, session, "disable_category", map[string]any{"name": "Health"})
	if disabled.IsError {
		t.Fatalf("disable Health: %s", structuredJSON(t, disabled))
	}
	inactive := decodeCategory(t, structuredObject(t, disabled)["category"])
	inactiveResult := callTool(t, session, "create_monthly_budget", map[string]any{
		"month":   "2026-08",
		"budgets": []map[string]any{{"category": "health", "amount": "10.00"}},
	})
	requireStructuredEqual(t, inactiveResult, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeCategoryInactive,
		"Category 'Health' is inactive.",
		false,
		map[string]any{
			"category":          inactive,
			"active_categories": []contract.Category{groceries},
		},
	)))

	created := callTool(t, session, "create_monthly_budget", map[string]any{
		"month":   "2026-08",
		"budgets": []map[string]any{{"category": "Groceries", "amount": "0.00"}},
	})
	if created.IsError {
		t.Fatalf("create zero budget: %s", structuredJSON(t, created))
	}
	already := callTool(t, session, "create_monthly_budget", map[string]any{
		"month":   "2026-08",
		"budgets": []map[string]any{{"category": "Pharmacy", "amount": "10.00"}},
	})
	requireStructuredEqual(t, already, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeMonthlyBudgetAlreadyExists,
		"A monthly budget already exists for 2026-08.",
		false,
		map[string]any{"month": "2026-08"},
	)))
}

func TestCreateMonthlyBudgetInternalError(t *testing.T) {
	db := openCategoryDB(t)
	var logs bytes.Buffer
	session := connectCategorySession(t, db, fixedBudgetNow, log.New(&logs, "", 0))
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	result := callTool(t, session, "create_monthly_budget", map[string]any{
		"month":   "2026-08",
		"budgets": []map[string]any{{"category": "Groceries", "amount": "10.00"}},
	})
	if !result.IsError {
		t.Fatal("internal create_monthly_budget IsError = false, want true")
	}
	requireStructuredEqual(t, result, contract.NewInternalErrorEnvelope())
	if leakedInternalError(structuredJSON(t, result)) || leakedInternalError(toolText(result)) {
		t.Fatalf("public payload leaked internal details: %s", structuredJSON(t, result))
	}
	if logs.Len() == 0 {
		t.Fatal("logger did not record private cause")
	}
}

func fixedBudgetNow() time.Time {
	return time.Date(2026, time.August, 15, 12, 0, 0, 0, time.FixedZone("Toronto", -4*60*60))
}

func schemaTypeContains(value any, want string) bool {
	if value == want {
		return true
	}
	values, ok := value.([]any)
	if !ok {
		return false
	}
	for _, candidate := range values {
		if candidate == want {
			return true
		}
	}
	return false
}
