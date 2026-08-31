package server_test

import "testing"

func TestSinkingFundLifecycleIsIdempotent(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedBudgetNow, nil)
	createCategoryForMerchantTest(t, session, "Car Repairs")
	budget := callTool(t, session, "create_monthly_budget", map[string]any{
		"month": "2026-08", "budgets": []map[string]any{{"category": "Car Repairs", "amount": "100.00"}},
	})
	if budget.IsError {
		t.Fatalf("create budget: %s", structuredJSON(t, budget))
	}

	enabled := callTool(t, session, "enable_sinking_fund", map[string]any{"category": "Car Repairs"})
	if enabled.IsError || structuredObject(t, enabled)["changed"] != true {
		t.Fatalf("enable sinking fund: %s", structuredJSON(t, enabled))
	}
	repeatedEnable := callTool(t, session, "enable_sinking_fund", map[string]any{"category": "Car Repairs"})
	if repeatedEnable.IsError || structuredObject(t, repeatedEnable)["changed"] != false {
		t.Fatalf("repeat enable: %s", structuredJSON(t, repeatedEnable))
	}

	disabled := callTool(t, session, "disable_sinking_fund", map[string]any{"category": "Car Repairs"})
	if disabled.IsError || structuredObject(t, disabled)["changed"] != true || structuredObject(t, disabled)["released_balance"] != "100.00" {
		t.Fatalf("disable sinking fund: %s", structuredJSON(t, disabled))
	}
	repeatedDisable := callTool(t, session, "disable_sinking_fund", map[string]any{"category": "Car Repairs"})
	if repeatedDisable.IsError || structuredObject(t, repeatedDisable)["changed"] != false || structuredObject(t, repeatedDisable)["released_balance"] != "100.00" {
		t.Fatalf("repeat disable: %s", structuredJSON(t, repeatedDisable))
	}
}
