package server_test

import (
	"bytes"
	"context"
	"database/sql"
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
	if !containsValue(required, "month") || containsValue(required, "budgets") || containsValue(required, "carry_forward") || containsValue(required, "overrides") || len(required) != 1 {
		t.Fatalf("required = %v, want only month", required)
	}
	properties, _ := schema["properties"].(map[string]any)
	monthSchema, _ := properties["month"].(map[string]any)
	if monthSchema["type"] != "string" {
		t.Fatalf("month schema type = %v, want string", monthSchema["type"])
	}
	carryForwardSchema, _ := properties["carry_forward"].(map[string]any)
	if carryForwardSchema == nil || !schemaTypeContains(carryForwardSchema["type"], "boolean") {
		t.Fatalf("carry_forward schema = %#v, want optional boolean", properties["carry_forward"])
	}
	assertAllocationArraySchema(t, properties, "budgets")
	assertAllocationArraySchema(t, properties, "overrides")
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

func TestCreateMonthlyBudgetCarryForwardSuccess(t *testing.T) {
	db := openCategoryDB(t)
	session := connectCategorySession(t, db, fixedBudgetNow, nil)
	dining := createCategoryForMerchantTest(t, session, "Dining")
	groceries := createCategoryForMerchantTest(t, session, "Groceries")
	insertCurrentMonthBudget(t, db, groceries.ID, "2026-07", 50000)
	insertCurrentMonthBudget(t, db, dining.ID, "2026-07", 15000)

	result := callTool(t, session, "create_monthly_budget", map[string]any{
		"month":         "2026-08",
		"carry_forward": true,
		"overrides": []map[string]any{
			{"category": " Dining ", "amount": "100.00"},
		},
	})
	if result.IsError {
		t.Fatalf("carry-forward create failed: %s", structuredJSON(t, result))
	}
	got := structuredObject(t, result)
	if keys := objectKeys(got); strings.Join(keys, ",") != "budgets,creation_mode,month,ok,source_month,total_budget" {
		t.Fatalf("carry-forward keys = %v", keys)
	}
	if got["ok"] != true || got["month"] != "2026-08" || got["creation_mode"] != "carry_forward" {
		t.Fatalf("carry-forward metadata = %s", structuredJSON(t, result))
	}
	if got["source_month"] != "2026-07" {
		t.Fatalf("source_month = %v, want 2026-07", got["source_month"])
	}
	if got["total_budget"] != "600.00" {
		t.Fatalf("total_budget = %v, want 600.00", got["total_budget"])
	}
	budgets, ok := got["budgets"].([]any)
	if !ok || len(budgets) != 2 {
		t.Fatalf("budgets = %#v, want two rows", got["budgets"])
	}
	first := decodeBudget(t, budgets[0])
	second := decodeBudget(t, budgets[1])
	if first.Category != dining.Name || first.CategoryID != dining.ID || first.Amount != "100.00" || first.Month != "2026-08" {
		t.Fatalf("first budget = %#v, want Dining/100.00", first)
	}
	if second.Category != groceries.Name || second.CategoryID != groceries.ID || second.Amount != "500.00" || second.Month != "2026-08" {
		t.Fatalf("second budget = %#v, want Groceries/500.00", second)
	}
	if budgetAmountHundredths(t, db, "2026-07", "Dining") != 15000 || budgetAmountHundredths(t, db, "2026-07", "Groceries") != 50000 {
		t.Fatal("source month changed after carry-forward")
	}
	if countBudgetsForMonth(t, db, "2026-07") != 2 {
		t.Fatalf("July rows = %d, want 2", countBudgetsForMonth(t, db, "2026-07"))
	}
}

func TestCreateMonthlyBudgetCarryForwardCatchUp(t *testing.T) {
	db := openCategoryDB(t)
	session := connectCategorySession(t, db, fixedBudgetNow, nil)
	createCategoryForMerchantTest(t, session, "Groceries")

	january := callTool(t, session, "create_monthly_budget", map[string]any{
		"month":   "2026-01",
		"budgets": []map[string]any{{"category": "Groceries", "amount": "80.00"}},
	})
	if january.IsError {
		t.Fatalf("create January: %s", structuredJSON(t, january))
	}

	march := callTool(t, session, "create_monthly_budget", map[string]any{
		"month":         "2026-03",
		"carry_forward": true,
	})
	if march.IsError {
		t.Fatalf("carry into March: %s", structuredJSON(t, march))
	}
	marchGot := structuredObject(t, march)
	if marchGot["creation_mode"] != "carry_forward" || marchGot["source_month"] != "2026-01" || marchGot["total_budget"] != "80.00" {
		t.Fatalf("March carry-forward = %s, want source 2026-01 80.00", structuredJSON(t, march))
	}

	august := callTool(t, session, "create_monthly_budget", map[string]any{
		"month":         "2026-08",
		"carry_forward": true,
	})
	if august.IsError {
		t.Fatalf("carry into August: %s", structuredJSON(t, august))
	}
	augustGot := structuredObject(t, august)
	if augustGot["source_month"] != "2026-03" || augustGot["total_budget"] != "80.00" {
		t.Fatalf("August carry-forward = %s, want source 2026-03 80.00", structuredJSON(t, august))
	}
	if countBudgetsForMonth(t, db, "2026-02") != 0 {
		t.Fatalf("February rows = %d, want 0", countBudgetsForMonth(t, db, "2026-02"))
	}
	if budgetAmountHundredths(t, db, "2026-01", "Groceries") != 8000 {
		t.Fatal("January changed after later carry-forwards")
	}

	for _, month := range []string{"2026-01", "2026-03", "2026-08"} {
		summary := callTool(t, session, "get_monthly_summary", map[string]any{"month": month})
		if summary.IsError {
			t.Fatalf("get_monthly_summary(%s): %s", month, structuredJSON(t, summary))
		}
		if structuredObject(t, summary)["total_budget"] != "80.00" {
			t.Fatalf("%s summary = %s, want 80.00", month, structuredJSON(t, summary))
		}
	}
}

func TestCreateMonthlyBudgetCarryForwardErrors(t *testing.T) {
	t.Run("source not found", func(t *testing.T) {
		session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)
		createCategoryForMerchantTest(t, session, "Groceries")
		result := callTool(t, session, "create_monthly_budget", map[string]any{
			"month":         "2026-08",
			"carry_forward": true,
		})
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeBudgetSourceNotFound,
			"There is no earlier monthly budget to carry forward into 2026-08.",
			false,
			map[string]any{"month": "2026-08"},
		)))
	})

	t.Run("past month ignores later snapshot", func(t *testing.T) {
		session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)
		createCategoryForMerchantTest(t, session, "Groceries")
		july := callTool(t, session, "create_monthly_budget", map[string]any{
			"month":   "2026-07",
			"budgets": []map[string]any{{"category": "Groceries", "amount": "200.00"}},
		})
		if july.IsError {
			t.Fatalf("create July: %s", structuredJSON(t, july))
		}
		result := callTool(t, session, "create_monthly_budget", map[string]any{
			"month":         "2026-03",
			"carry_forward": true,
		})
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeBudgetSourceNotFound,
			"There is no earlier monthly budget to carry forward into 2026-03.",
			false,
			map[string]any{"month": "2026-03"},
		)))
	})

	t.Run("source empty", func(t *testing.T) {
		db := openCategoryDB(t)
		session := connectCategorySession(t, db, fixedBudgetNow, nil)
		dining := createCategoryForMerchantTest(t, session, "Dining")
		createCategoryForMerchantTest(t, session, "Groceries")
		insertCurrentMonthBudget(t, db, dining.ID, "2026-07", 4000)
		disabled := callTool(t, session, "disable_category", map[string]any{"name": "Dining"})
		if disabled.IsError {
			t.Fatalf("disable Dining: %s", structuredJSON(t, disabled))
		}

		result := callTool(t, session, "create_monthly_budget", map[string]any{
			"month":         "2026-08",
			"carry_forward": true,
			"overrides":     []map[string]any{{"category": "Groceries", "amount": "10.00"}},
		})
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeBudgetSourceEmpty,
			"The earlier monthly budget has no active categories to carry forward.",
			false,
			map[string]any{"month": "2026-08", "source_month": "2026-07"},
		)))
		if countBudgetsForMonth(t, db, "2026-08") != 0 {
			t.Fatalf("August rows after source empty = %d, want 0", countBudgetsForMonth(t, db, "2026-08"))
		}
	})

	t.Run("already exists", func(t *testing.T) {
		session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)
		createCategoryForMerchantTest(t, session, "Groceries")
		created := callTool(t, session, "create_monthly_budget", map[string]any{
			"month":   "2026-08",
			"budgets": []map[string]any{{"category": "Groceries", "amount": "10.00"}},
		})
		if created.IsError {
			t.Fatalf("explicit create: %s", structuredJSON(t, created))
		}
		result := callTool(t, session, "create_monthly_budget", map[string]any{
			"month":         "2026-08",
			"carry_forward": true,
		})
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeMonthlyBudgetAlreadyExists,
			"A monthly budget already exists for 2026-08.",
			false,
			map[string]any{"month": "2026-08"},
		)))
	})

	t.Run("override category not found", func(t *testing.T) {
		db := openCategoryDB(t)
		session := connectCategorySession(t, db, fixedBudgetNow, nil)
		groceries := createCategoryForMerchantTest(t, session, "Groceries")
		health := createCategoryForMerchantTest(t, session, "Health")
		insertCurrentMonthBudget(t, db, groceries.ID, "2026-07", 1000)
		result := callTool(t, session, "create_monthly_budget", map[string]any{
			"month":         "2026-08",
			"carry_forward": true,
			"overrides":     []map[string]any{{"category": " Pharmacy ", "amount": "10.00"}},
		})
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeCategoryNotFound,
			"Category 'Pharmacy' does not exist.",
			false,
			map[string]any{
				"requested_category": "Pharmacy",
				"categories":         []contract.Category{groceries, health},
			},
		)))
	})

	t.Run("override category inactive", func(t *testing.T) {
		db := openCategoryDB(t)
		session := connectCategorySession(t, db, fixedBudgetNow, nil)
		groceries := createCategoryForMerchantTest(t, session, "Groceries")
		health := createCategoryForMerchantTest(t, session, "Health")
		insertCurrentMonthBudget(t, db, groceries.ID, "2026-07", 1000)
		disabled := callTool(t, session, "disable_category", map[string]any{"name": "Health"})
		if disabled.IsError {
			t.Fatalf("disable Health: %s", structuredJSON(t, disabled))
		}
		inactive := decodeCategory(t, structuredObject(t, disabled)["category"])
		if health.ID != inactive.ID {
			t.Fatalf("disabled Health id = %d, want %d", inactive.ID, health.ID)
		}
		result := callTool(t, session, "create_monthly_budget", map[string]any{
			"month":         "2026-08",
			"carry_forward": true,
			"overrides":     []map[string]any{{"category": "health", "amount": "10.00"}},
		})
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeCategoryInactive,
			"Category 'Health' is inactive.",
			false,
			map[string]any{
				"category":          inactive,
				"active_categories": []contract.Category{groceries},
			},
		)))
	})
}

func TestCreateMonthlyBudgetModeConflicts(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)

	combined := callTool(t, session, "create_monthly_budget", map[string]any{
		"month":         "2026-08",
		"carry_forward": true,
		"budgets":       []map[string]any{{"category": "Groceries", "amount": "1.00"}},
	})
	requireStructuredEqual(t, combined, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{"fields": []contract.FieldIssue{{Field: "budgets", Reason: "cannot be combined with carry_forward"}}},
	)))

	falseCarry := callTool(t, session, "create_monthly_budget", map[string]any{
		"month":         "2026-08",
		"carry_forward": false,
		"budgets":       []map[string]any{{"category": "Groceries", "amount": "1.00"}},
	})
	requireStructuredEqual(t, falseCarry, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{"fields": []contract.FieldIssue{{Field: "carry_forward", Reason: "must be true when supplied"}}},
	)))

	overridesOnly := callTool(t, session, "create_monthly_budget", map[string]any{
		"month":     "2026-08",
		"budgets":   []map[string]any{{"category": "Groceries", "amount": "1.00"}},
		"overrides": []map[string]any{{"category": "Groceries", "amount": "2.00"}},
	})
	requireStructuredEqual(t, overridesOnly, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{"fields": []contract.FieldIssue{{Field: "overrides", Reason: "cannot be supplied unless carry_forward is true"}}},
	)))

	monthOnly := callTool(t, session, "create_monthly_budget", map[string]any{"month": "2026-08"})
	requireStructuredEqual(t, monthOnly, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{"fields": []contract.FieldIssue{{Field: "budgets", Reason: "must contain at least one allocation"}}},
	)))
}

func TestSetBudgetsToolDiscovery(t *testing.T) {
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
		if candidate.Name == "set_budgets" {
			tool = candidate
			break
		}
	}
	if tool == nil {
		t.Fatal("set_budgets is not discoverable")
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
	monthSchema, _ := properties["month"].(map[string]any)
	if monthSchema["type"] != "string" {
		t.Fatalf("month schema type = %v, want string", monthSchema["type"])
	}
	assertAllocationArraySchema(t, properties, "budgets")
}

func TestSetBudgetsSuccess(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)
	dining := createCategoryForMerchantTest(t, session, "Dining")
	groceries := createCategoryForMerchantTest(t, session, "Groceries")
	created := callTool(t, session, "create_monthly_budget", map[string]any{
		"month":   "2026-08",
		"budgets": []map[string]any{{"category": "Groceries", "amount": "100.00"}},
	})
	if created.IsError {
		t.Fatalf("create monthly budget: %s", structuredJSON(t, created))
	}
	existing := decodeBudget(t, structuredObject(t, created)["budgets"].([]any)[0])

	result := callTool(t, session, "set_budgets", map[string]any{
		"month": "2026-08",
		"budgets": []map[string]any{
			{"category": "GROCERIES", "amount": "200.00"},
			{"category": "Dining", "amount": "50.00"},
		},
	})
	if result.IsError {
		t.Fatalf("set_budgets failed: %s", structuredJSON(t, result))
	}
	got := structuredObject(t, result)
	if keys := objectKeys(got); strings.Join(keys, ",") != "changes,month,ok" {
		t.Fatalf("set_budgets keys = %v", keys)
	}
	if got["ok"] != true || got["month"] != "2026-08" {
		t.Fatalf("set_budgets metadata = %s", structuredJSON(t, result))
	}
	changes, ok := got["changes"].([]any)
	if !ok || len(changes) != 2 {
		t.Fatalf("changes = %#v, want two rows", got["changes"])
	}
	firstChange := asObject(t, changes[0])
	secondChange := asObject(t, changes[1])
	first := decodeBudget(t, firstChange["budget"])
	second := decodeBudget(t, secondChange["budget"])
	if firstChange["created"] != true || first.Category != dining.Name || first.CategoryID != dining.ID || first.Amount != "50.00" {
		t.Fatalf("first change = %#v, want inserted Dining/50.00", changes[0])
	}
	if secondChange["created"] != false || second.Category != groceries.Name || second.ID != existing.ID || second.Amount != "200.00" {
		t.Fatalf("second change = %#v, want replaced Groceries/200.00", changes[1])
	}
}

func TestSetBudgetsValidation(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)
	result := callTool(t, session, "set_budgets", map[string]any{
		"month": "2026-8",
		"budgets": []map[string]any{
			{"category": " \x00 ", "amount": "-1"},
			{"category": "Groceries", "amount": "1.001"},
			{"category": "gROCERIES", "amount": "2"},
		},
	})
	if !result.IsError {
		t.Fatal("invalid set_budgets IsError = false, want true")
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
				{Field: "budgets[2].category", Reason: "must not repeat a category"},
			},
		},
	)))

	empty := callTool(t, session, "set_budgets", map[string]any{
		"month":   "2026-08",
		"budgets": []map[string]any{},
	})
	requireStructuredEqual(t, empty, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{"fields": []contract.FieldIssue{{Field: "budgets", Reason: "must contain at least one allocation"}}},
	)))
}

func TestSetBudgetsNotFound(t *testing.T) {
	t.Run("no earlier month", func(t *testing.T) {
		db := openCategoryDB(t)
		session := connectCategorySession(t, db, fixedBudgetNow, nil)
		createCategoryForMerchantTest(t, session, "Groceries")
		result := callTool(t, session, "set_budgets", map[string]any{
			"month":   "2026-08",
			"budgets": []map[string]any{{"category": "Groceries", "amount": "10.00"}},
		})
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeMonthlyBudgetNotFound,
			"No monthly budget exists for 2026-08.",
			false,
			map[string]any{"month": "2026-08", "latest_earlier_month": nil},
		)))
		if countBudgetsForMonth(t, db, "2026-08") != 0 {
			t.Fatalf("August rows after missing month = %d, want 0", countBudgetsForMonth(t, db, "2026-08"))
		}
	})

	t.Run("latest earlier month", func(t *testing.T) {
		db := openCategoryDB(t)
		session := connectCategorySession(t, db, fixedBudgetNow, nil)
		january := createCategoryForMerchantTest(t, session, "JanuaryOnly")
		march := createCategoryForMerchantTest(t, session, "MarchOnly")
		insertCurrentMonthBudget(t, db, january.ID, "2026-01", 1000)
		insertCurrentMonthBudget(t, db, march.ID, "2026-03", 2500)
		result := callTool(t, session, "set_budgets", map[string]any{
			"month":   "2026-08",
			"budgets": []map[string]any{{"category": "MarchOnly", "amount": "1.00"}},
		})
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeMonthlyBudgetNotFound,
			"No monthly budget exists for 2026-08.",
			false,
			map[string]any{"month": "2026-08", "latest_earlier_month": "2026-03"},
		)))
		if countBudgetsForMonth(t, db, "2026-08") != 0 {
			t.Fatalf("August rows after missing month = %d, want 0", countBudgetsForMonth(t, db, "2026-08"))
		}
		if budgetAmountHundredths(t, db, "2026-01", "JanuaryOnly") != 1000 || budgetAmountHundredths(t, db, "2026-03", "MarchOnly") != 2500 {
			t.Fatal("earlier months changed after set_budgets not-found")
		}
	})
}

func TestSetBudgetsDomainErrors(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)
	groceries := createCategoryForMerchantTest(t, session, "Groceries")
	health := createCategoryForMerchantTest(t, session, "Health")
	created := callTool(t, session, "create_monthly_budget", map[string]any{
		"month":   "2026-08",
		"budgets": []map[string]any{{"category": "Groceries", "amount": "10.00"}},
	})
	if created.IsError {
		t.Fatalf("create monthly budget: %s", structuredJSON(t, created))
	}

	missing := callTool(t, session, "set_budgets", map[string]any{
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
	inactiveResult := callTool(t, session, "set_budgets", map[string]any{
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
}

func TestSetBudgetsInternalError(t *testing.T) {
	db := openCategoryDB(t)
	var logs bytes.Buffer
	session := connectCategorySession(t, db, fixedBudgetNow, log.New(&logs, "", 0))
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	result := callTool(t, session, "set_budgets", map[string]any{
		"month":   "2026-08",
		"budgets": []map[string]any{{"category": "Groceries", "amount": "10.00"}},
	})
	if !result.IsError {
		t.Fatal("internal set_budgets IsError = false, want true")
	}
	requireStructuredEqual(t, result, contract.NewInternalErrorEnvelope())
	if leakedInternalError(structuredJSON(t, result)) || leakedInternalError(toolText(result)) {
		t.Fatalf("public payload leaked internal details: %s", structuredJSON(t, result))
	}
	if logs.Len() == 0 {
		t.Fatal("logger did not record private cause")
	}
}

func TestCreateMonthlyBudgetPastMonthThenSummary(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)
	createCategoryForMerchantTest(t, session, "Groceries")

	created := callTool(t, session, "create_monthly_budget", map[string]any{
		"month":   "2026-07",
		"budgets": []map[string]any{{"category": "Groceries", "amount": "400.00"}},
	})
	if created.IsError {
		t.Fatalf("create past month: %s", structuredJSON(t, created))
	}
	if structuredObject(t, created)["month"] != "2026-07" {
		t.Fatalf("created month = %s, want 2026-07", structuredJSON(t, created))
	}

	summary := callTool(t, session, "get_monthly_summary", map[string]any{"month": "2026-07"})
	if summary.IsError {
		t.Fatalf("get_monthly_summary(2026-07): %s", structuredJSON(t, summary))
	}
	got := structuredObject(t, summary)
	if got["ok"] != true || got["month"] != "2026-07" || got["total_budget"] != "400.00" {
		t.Fatalf("past-month summary = %s, want July 400.00", structuredJSON(t, summary))
	}
}

func TestCreateMonthlyBudgetRejectsFutureMonth(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)
	createCategoryForMerchantTest(t, session, "Groceries")
	result := callTool(t, session, "create_monthly_budget", map[string]any{
		"month":   "2026-09",
		"budgets": []map[string]any{{"category": "Groceries", "amount": "1.00"}},
	})
	requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{"fields": []contract.FieldIssue{{Field: "month", Reason: "must not be in the future"}}},
	)))
}

func TestSetBudgetsUpdatesExistingPastMonth(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)
	createCategoryForMerchantTest(t, session, "Groceries")
	created := callTool(t, session, "create_monthly_budget", map[string]any{
		"month":   "2026-07",
		"budgets": []map[string]any{{"category": "Groceries", "amount": "20.00"}},
	})
	if created.IsError {
		t.Fatalf("create past month: %s", structuredJSON(t, created))
	}

	result := callTool(t, session, "set_budgets", map[string]any{
		"month":   "2026-07",
		"budgets": []map[string]any{{"category": "Groceries", "amount": "55.00"}},
	})
	if result.IsError {
		t.Fatalf("set_budgets(2026-07): %s", structuredJSON(t, result))
	}
	got := structuredObject(t, result)
	if got["ok"] != true || got["month"] != "2026-07" {
		t.Fatalf("set_budgets metadata = %s", structuredJSON(t, result))
	}
	changes, _ := got["changes"].([]any)
	if len(changes) != 1 {
		t.Fatalf("changes = %#v, want one row", got["changes"])
	}
	change := asObject(t, changes[0])
	updated := decodeBudget(t, change["budget"])
	if change["created"] != false || updated.Amount != "55.00" || updated.Month != "2026-07" {
		t.Fatalf("past-month change = %#v, want July Groceries 55.00", changes[0])
	}
}

func TestSetBudgetsPastMonthWithoutSnapshotIsNotFound(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)
	createCategoryForMerchantTest(t, session, "Groceries")
	result := callTool(t, session, "set_budgets", map[string]any{
		"month":   "2026-07",
		"budgets": []map[string]any{{"category": "Groceries", "amount": "99.00"}},
	})
	requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeMonthlyBudgetNotFound,
		"No monthly budget exists for 2026-07.",
		false,
		map[string]any{"month": "2026-07", "latest_earlier_month": nil},
	)))
}

func TestSetBudgetsRejectsFutureMonth(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)
	createCategoryForMerchantTest(t, session, "Groceries")
	created := callTool(t, session, "create_monthly_budget", map[string]any{
		"month":   "2026-08",
		"budgets": []map[string]any{{"category": "Groceries", "amount": "20.00"}},
	})
	if created.IsError {
		t.Fatalf("create monthly budget: %s", structuredJSON(t, created))
	}

	result := callTool(t, session, "set_budgets", map[string]any{
		"month":   "2026-09",
		"budgets": []map[string]any{{"category": "Groceries", "amount": "99.00"}},
	})
	requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{"fields": []contract.FieldIssue{{Field: "month", Reason: "must not be in the future"}}},
	)))
}

func TestSetBudgetsCannotCreateNewMonth(t *testing.T) {
	db := openCategoryDB(t)
	session := connectCategorySession(t, db, fixedBudgetNow, nil)
	createCategoryForMerchantTest(t, session, "Dining")
	result := callTool(t, session, "set_budgets", map[string]any{
		"month":   "2026-08",
		"budgets": []map[string]any{{"category": "Dining", "amount": "25.00"}},
	})
	requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeMonthlyBudgetNotFound,
		"No monthly budget exists for 2026-08.",
		false,
		map[string]any{"month": "2026-08", "latest_earlier_month": nil},
	)))
	if countBudgetsForMonth(t, db, "2026-08") != 0 {
		t.Fatalf("set_budgets created a new month: %d rows", countBudgetsForMonth(t, db, "2026-08"))
	}
}

func fixedBudgetNow() time.Time {
	return time.Date(2026, time.August, 15, 12, 0, 0, 0, time.FixedZone("Toronto", -4*60*60))
}

func assertAllocationArraySchema(t *testing.T, properties map[string]any, field string) {
	t.Helper()
	arraySchema, _ := properties[field].(map[string]any)
	if arraySchema == nil || !schemaTypeContains(arraySchema["type"], "array") {
		t.Fatalf("%s schema = %#v, want array", field, properties[field])
	}
	items, _ := arraySchema["items"].(map[string]any)
	if !schemaTypeContains(items["type"], "object") {
		t.Fatalf("%s item schema type = %#v, want object", field, items["type"])
	}
	itemRequired, _ := items["required"].([]any)
	if !containsValue(itemRequired, "category") || !containsValue(itemRequired, "amount") {
		t.Fatalf("%s item required = %v, want category and amount", field, itemRequired)
	}
}

func budgetAmountHundredths(t *testing.T, db *sql.DB, month, category string) int64 {
	t.Helper()
	var amount int64
	if err := db.QueryRowContext(context.Background(), `
		SELECT b.amount_hundredths
		FROM budgets AS b
		INNER JOIN categories AS c ON c.id = b.category_id
		WHERE b.month = ? AND c.name = ? COLLATE NOCASE
	`, month, category).Scan(&amount); err != nil {
		t.Fatalf("select budget %s/%s: %v", month, category, err)
	}
	return amount
}

func countBudgetsForMonth(t *testing.T, db *sql.DB, month string) int64 {
	t.Helper()
	var count int64
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM budgets WHERE month = ?`, month).Scan(&count); err != nil {
		t.Fatalf("count budgets %s: %v", month, err)
	}
	return count
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
