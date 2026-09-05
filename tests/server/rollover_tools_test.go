package server_test

import (
	"context"
	"testing"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
)

func TestBudgetRolloverToolsJourneyAndSummaryExplanation(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)
	createCategoryForMerchantTest(t, session, "Groceries")

	budget := callTool(t, session, "create_monthly_budget", map[string]any{
		"month":   "2026-07",
		"budgets": []map[string]any{{"category": "Groceries", "amount": "300.00"}},
	})
	if budget.IsError {
		t.Fatalf("create July budget: %s", structuredJSON(t, budget))
	}

	added := callTool(t, session, "add_transaction", map[string]any{
		"amount": "320.00", "merchant": "Metro", "category": "Groceries", "date": "2026-07-15",
	})
	if added.IsError {
		t.Fatalf("add source transaction: %s", structuredJSON(t, added))
	}
	addedPayload := structuredObject(t, added)
	offers, ok := addedPayload["rollover_offers"].([]any)
	if !ok || len(offers) != 1 {
		t.Fatalf("rollover_offers = %#v, want one offer", addedPayload["rollover_offers"])
	}
	offer := asObject(t, offers[0])
	if offer["source_month"] != "2026-07" || offer["target_month"] != "2026-08" || offer["eligible_rollover"] != "20.00" {
		t.Fatalf("offer = %#v", offer)
	}
	transaction := decodeTransaction(t, addedPayload["transaction"])

	created := callTool(t, session, "create_budget_rollover", map[string]any{
		"source_month":          "2026-07",
		"category":              "Groceries",
		"amount":                "20.00",
		"source_transaction_id": transaction.ID,
		"note":                  "July grocery overage",
	})
	if created.IsError {
		t.Fatalf("create_budget_rollover: %s", structuredJSON(t, created))
	}
	rolloverRecord := objectField(t, structuredObject(t, created), "rollover")
	if rolloverRecord["source_month"] != "2026-07" || rolloverRecord["target_month"] != "2026-08" || rolloverRecord["amount"] != "20.00" || rolloverRecord["note"] != "July grocery overage" {
		t.Fatalf("created rollover = %#v", rolloverRecord)
	}

	augustBudget := callTool(t, session, "create_monthly_budget", map[string]any{
		"month":   "2026-08",
		"budgets": []map[string]any{{"category": "Groceries", "amount": "300.00"}},
	})
	if augustBudget.IsError {
		t.Fatalf("create August budget: %s", structuredJSON(t, augustBudget))
	}
	listed := callTool(t, session, "list_budget_rollovers", map[string]any{
		"target_month": "2026-08", "category": "groceries",
	})
	if listed.IsError {
		t.Fatalf("list_budget_rollovers: %s", structuredJSON(t, listed))
	}
	listedPayload := structuredObject(t, listed)
	listedRows, ok := listedPayload["rollovers"].([]any)
	if !ok || len(listedRows) != 1 {
		t.Fatalf("listed rollovers = %#v", listedPayload["rollovers"])
	}
	if asObject(t, listedRows[0])["status"] != "applied" {
		t.Fatalf("listed rollover status = %#v, want applied", asObject(t, listedRows[0])["status"])
	}

	summaryResult := callTool(t, session, "get_monthly_summary", map[string]any{"month": "2026-08"})
	if summaryResult.IsError {
		t.Fatalf("get_monthly_summary: %s", structuredJSON(t, summaryResult))
	}
	summaryPayload := structuredObject(t, summaryResult)
	if summaryPayload["total_base_budget"] != "300.00" || summaryPayload["total_rollover_adjustment"] != "-20.00" || summaryPayload["total_budget"] != "280.00" {
		t.Fatalf("summary totals = %#v", summaryPayload)
	}

	tooLarge := callTool(t, session, "create_budget_rollover", map[string]any{
		"source_month": "2026-07", "category": "Groceries", "amount": "0.01",
	})
	if !tooLarge.IsError {
		t.Fatal("ineligible rollover IsError = false, want true")
	}
	errorPayload := objectField(t, structuredObject(t, tooLarge), "error")
	if errorPayload["code"] != string(contract.ErrorCodeBudgetRolloverNotEligible) {
		t.Fatalf("ineligible error = %#v", errorPayload)
	}
	details := objectField(t, errorPayload, "details")
	if details["source_overspending"] != "20.00" || details["already_rolled"] != "20.00" || details["eligible_rollover"] != "0.00" {
		t.Fatalf("ineligible details = %#v", details)
	}

	removed := callTool(t, session, "remove_budget_rollover", map[string]any{"id": rolloverRecord["id"]})
	if removed.IsError {
		t.Fatalf("remove_budget_rollover: %s", structuredJSON(t, removed))
	}
	restored := callTool(t, session, "get_category_summary", map[string]any{"category": "Groceries", "month": "2026-08"})
	if restored.IsError {
		t.Fatalf("restored category summary: %s", structuredJSON(t, restored))
	}
	restoredPayload := structuredObject(t, restored)
	if restoredPayload["base_budget"] != "300.00" || restoredPayload["rollover_adjustment"] != "0.00" || restoredPayload["budget"] != "300.00" {
		t.Fatalf("restored summary = %#v", restoredPayload)
	}

	missing := callTool(t, session, "remove_budget_rollover", map[string]any{"id": rolloverRecord["id"]})
	if !missing.IsError {
		t.Fatal("repeated remove IsError = false, want true")
	}
	if objectField(t, structuredObject(t, missing), "error")["code"] != string(contract.ErrorCodeBudgetRolloverNotFound) {
		t.Fatalf("repeated remove error = %#v", structuredObject(t, missing))
	}
}

func TestBudgetRolloverToolSchemasAndAnnotations(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	for _, name := range []string{"create_budget_rollover", "list_budget_rollovers", "remove_budget_rollover"} {
		tool := toolByName(t, result.Tools, name)
		if tool.Annotations == nil || tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Fatalf("%s annotations = %#v, want local tool", name, tool.Annotations)
		}
		schema := schemaObject(t, tool.InputSchema)
		if schema["type"] != "object" {
			t.Fatalf("%s schema = %#v, want object", name, schema)
		}
	}
	create := toolByName(t, result.Tools, "create_budget_rollover")
	required, _ := schemaObject(t, create.InputSchema)["required"].([]any)
	for _, field := range []string{"source_month", "category", "amount"} {
		if !containsValue(required, field) {
			t.Fatalf("create required = %v, missing %s", required, field)
		}
	}
	if containsValue(required, "target_month") {
		t.Fatal("create schema accepts target_month, want derived target")
	}
}
