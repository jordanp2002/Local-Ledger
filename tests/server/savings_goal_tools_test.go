package server_test

import (
	"strings"
	"testing"
)

func TestSavingsGoalToolBasics(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)

	savings := callTool(t, session, "create_account", map[string]any{"name": "Savings", "type": "savings", "opening_balance": "5000.00"})
	if savings.IsError {
		t.Fatalf("create account: %s", structuredJSON(t, savings))
	}
	acctID := int64(objectField(t, structuredObject(t, savings), "account")["id"].(float64))

	created := callTool(t, session, "create_savings_goal", map[string]any{
		"name":          "Japan Trip",
		"account_id":    acctID,
		"target_amount": "5000.00",
		"target_date":   "2027-10-01",
		"note":          "vacation",
	})
	if created.IsError {
		t.Fatalf("create goal: %s", structuredJSON(t, created))
	}
	got := structuredObject(t, created)
	if keys := objectKeys(got); strings.Join(keys, ",") != "goal,ok" {
		t.Fatalf("create keys = %v", keys)
	}
	goal := objectField(t, got, "goal")
	if keys := objectKeys(goal); strings.Join(keys, ",") != "account,account_id,cancelled_at,completed_at,created_at,current_amount,id,name,note,progress_percent,remaining_amount,status,target_amount,target_date,target_reached,updated_at" {
		t.Fatalf("goal keys = %v", keys)
	}
	if goal["name"] != "Japan Trip" || goal["account"] != "Savings" || goal["target_amount"] != "5000.00" || goal["current_amount"] != "0.00" || goal["remaining_amount"] != "5000.00" || goal["progress_percent"] != "0.00" || goal["target_reached"] != false || goal["status"] != "active" {
		t.Fatalf("goal fields = %v", goal)
	}
	goalID := int64(goal["id"].(float64))

	dup := callTool(t, session, "create_savings_goal", map[string]any{
		"name":          "japan trip",
		"account_id":    acctID,
		"target_amount": "3000.00",
	})
	if !dup.IsError || structuredObject(t, dup)["error"].(map[string]any)["code"] != "savings_goal_already_exists" {
		t.Fatalf("duplicate error = %s", structuredJSON(t, dup))
	}

	listed := callTool(t, session, "list_savings_goals", map[string]any{})
	if listed.IsError {
		t.Fatalf("list goals: %s", structuredJSON(t, listed))
	}
	listObj := structuredObject(t, listed)
	if keys := objectKeys(listObj); strings.Join(keys, ",") != "goals,ok" {
		t.Fatalf("list keys = %v", keys)
	}
	rows := listObj["goals"].([]any)
	if len(rows) != 1 {
		t.Fatalf("goals count = %d, want 1", len(rows))
	}

	noOp := callTool(t, session, "update_savings_goal", map[string]any{
		"id":            goalID,
		"name":          "Japan Trip",
		"target_amount": "5000.00",
		"target_date":   "2027-10-01",
		"note":          "vacation",
	})
	if noOp.IsError {
		t.Fatalf("no-op update: %s", structuredJSON(t, noOp))
	}
	noOpObj := structuredObject(t, noOp)
	if noOpObj["changed"] != false {
		t.Fatalf("expected changed=false on no-op, got %v", noOpObj["changed"])
	}

	updated := callTool(t, session, "update_savings_goal", map[string]any{
		"id":          goalID,
		"name":        "Japan 2027",
		"target_date": nil,
		"note":        nil,
	})
	if updated.IsError {
		t.Fatalf("update goal: %s", structuredJSON(t, updated))
	}
	updatedObj := structuredObject(t, updated)
	if updatedObj["changed"] != true {
		t.Fatalf("expected changed=true on update, got %v", updatedObj["changed"])
	}
	upGoal := objectField(t, updatedObj, "goal")
	if upGoal["name"] != "Japan 2027" || upGoal["target_date"] != nil || upGoal["note"] != nil {
		t.Fatalf("updated fields = %v", upGoal)
	}
}

func TestSavingsGoalToolErrorsAndGuards(t *testing.T) {
	db := openCategoryDB(t)
	session := connectCategorySession(t, db, fixedTransactionNow, nil)

	savings := callTool(t, session, "create_account", map[string]any{"name": "Savings", "type": "savings", "opening_balance": "0.00"})
	acctID := int64(objectField(t, structuredObject(t, savings), "account")["id"].(float64))

	missingAccount := callTool(t, session, "create_savings_goal", map[string]any{
		"name":          "Test",
		"account_id":    9999,
		"target_amount": "100.00",
	})
	if !missingAccount.IsError || structuredObject(t, missingAccount)["error"].(map[string]any)["code"] != "account_not_found" {
		t.Fatalf("missing account error = %s", structuredJSON(t, missingAccount))
	}

	inactive := callTool(t, session, "create_account", map[string]any{"name": "Inactive", "type": "savings", "opening_balance": "0.00"})
	inactID := int64(objectField(t, structuredObject(t, inactive), "account")["id"].(float64))
	callTool(t, session, "disable_account", map[string]any{"id": inactID})

	inactiveTarget := callTool(t, session, "create_savings_goal", map[string]any{
		"name":          "Test Inactive",
		"account_id":    inactID,
		"target_amount": "100.00",
	})
	if !inactiveTarget.IsError || structuredObject(t, inactiveTarget)["error"].(map[string]any)["code"] != "account_inactive" {
		t.Fatalf("inactive account error = %s", structuredJSON(t, inactiveTarget))
	}

	created := callTool(t, session, "create_savings_goal", map[string]any{
		"name":          "Active Goal",
		"account_id":    acctID,
		"target_amount": "1000.00",
	})
	goalID := int64(objectField(t, structuredObject(t, created), "goal")["id"].(float64))

	disableGuarded := callTool(t, session, "disable_account", map[string]any{"id": acctID})
	if !disableGuarded.IsError || structuredObject(t, disableGuarded)["error"].(map[string]any)["code"] != "account_goal_active" {
		t.Fatalf("expected account_goal_active, got %s", structuredJSON(t, disableGuarded))
	}

	missingGoal := callTool(t, session, "update_savings_goal", map[string]any{
		"id":   9999,
		"name": "Nonexistent",
	})
	if !missingGoal.IsError || structuredObject(t, missingGoal)["error"].(map[string]any)["code"] != "savings_goal_not_found" {
		t.Fatalf("missing goal error = %s", structuredJSON(t, missingGoal))
	}

	checking := callTool(t, session, "create_account", map[string]any{"name": "Checking", "type": "checking", "opening_balance": "500.00"})
	checkingID := int64(objectField(t, structuredObject(t, checking), "account")["id"].(float64))

	_, err := db.Exec(`
		INSERT INTO savings_goal_entries (goal_id, account_id, delta_hundredths, kind, date, idempotency_key, fingerprint)
		VALUES (?, ?, 10000, 'allocation', '2026-08-14', 'seed-alloc', 'seed-fp')
	`, goalID, acctID)
	if err != nil {
		t.Fatalf("seed entry: %v", err)
	}

	changeAccountBlocked := callTool(t, session, "update_savings_goal", map[string]any{
		"id":         goalID,
		"account_id": checkingID,
	})
	if !changeAccountBlocked.IsError || structuredObject(t, changeAccountBlocked)["error"].(map[string]any)["code"] != "savings_goal_has_allocations" {
		t.Fatalf("expected savings_goal_has_allocations, got %s", structuredJSON(t, changeAccountBlocked))
	}

	_, err = db.Exec("UPDATE savings_goals SET status = 'completed', completed_at = '2026-08-15T12:00:00.000Z' WHERE id = ?", goalID)
	if err != nil {
		t.Fatalf("mark completed: %v", err)
	}
	closedGoal := callTool(t, session, "update_savings_goal", map[string]any{
		"id":   goalID,
		"name": "New Goal Name",
	})
	if !closedGoal.IsError || structuredObject(t, closedGoal)["error"].(map[string]any)["code"] != "savings_goal_closed" {
		t.Fatalf("expected savings_goal_closed, got %s", structuredJSON(t, closedGoal))
	}
}

func TestSavingsGoalFundingLifecycleAndOverview(t *testing.T) {
	db := openCategoryDB(t)
	session := connectCategorySession(t, db, fixedTransactionNow, nil)
	savings := callTool(t, session, "create_account", map[string]any{"name": "Savings", "type": "savings", "opening_balance": "500.00"})
	checking := callTool(t, session, "create_account", map[string]any{"name": "Checking", "type": "checking", "opening_balance": "300.00"})
	savingsID := int64(objectField(t, structuredObject(t, savings), "account")["id"].(float64))
	checkingID := int64(objectField(t, structuredObject(t, checking), "account")["id"].(float64))
	created := callTool(t, session, "create_savings_goal", map[string]any{"name": "Japan", "account_id": savingsID, "target_amount": "300.00"})
	goalID := int64(objectField(t, structuredObject(t, created), "goal")["id"].(float64))

	allocated := callTool(t, session, "allocate_to_savings_goal", map[string]any{"goal_id": goalID, "amount": "200.00", "date": "2026-08-14", "idempotency_key": "japan-existing"})
	if allocated.IsError || objectField(t, structuredObject(t, allocated), "goal")["current_amount"] != "200.00" {
		t.Fatalf("allocate = %s", structuredJSON(t, allocated))
	}
	replay := callTool(t, session, "allocate_to_savings_goal", map[string]any{"goal_id": goalID, "amount": "200.00", "date": "2026-08-14", "idempotency_key": "japan-existing"})
	if replay.IsError || structuredObject(t, replay)["idempotent_replay"] != true {
		t.Fatalf("allocation replay = %s", structuredJSON(t, replay))
	}

	funded := callTool(t, session, "fund_savings_goal", map[string]any{"goal_id": goalID, "source_account_id": checkingID, "amount": "100.00", "date": "2026-08-14", "idempotency_key": "japan-fund"})
	if funded.IsError {
		t.Fatalf("fund = %s", structuredJSON(t, funded))
	}
	fundResult := structuredObject(t, funded)
	if fundResult["source_balance"] != "200.00" || fundResult["destination_balance"] != "600.00" || fundResult["executed_externally"] != false {
		t.Fatalf("fund result = %v", fundResult)
	}
	transferID := int64(fundResult["transfer"].(map[string]any)["id"].(float64))

	blocked := callTool(t, session, "reverse_account_transfer", map[string]any{"id": transferID, "idempotency_key": "generic-reverse"})
	if !blocked.IsError || structuredObject(t, blocked)["error"].(map[string]any)["code"] != "transfer_dependency_conflict" {
		t.Fatalf("generic reverse = %s", structuredJSON(t, blocked))
	}
	completed := callTool(t, session, "complete_savings_goal", map[string]any{"goal_id": goalID})
	if completed.IsError || objectField(t, structuredObject(t, completed), "goal")["status"] != "completed" {
		t.Fatalf("complete = %s", structuredJSON(t, completed))
	}
	released := callTool(t, session, "release_savings_goal_funds", map[string]any{"goal_id": goalID, "amount": "50.00", "date": "2026-08-14", "idempotency_key": "japan-release"})
	if released.IsError || objectField(t, structuredObject(t, released), "goal")["current_amount"] != "250.00" {
		t.Fatalf("release = %s", structuredJSON(t, released))
	}

	var entryID int64
	if err := db.QueryRow("SELECT id FROM savings_goal_entries WHERE transfer_id = ? AND kind = 'transfer_funding'", transferID).Scan(&entryID); err != nil {
		t.Fatalf("funding entry: %v", err)
	}
	reversed := callTool(t, session, "reverse_savings_goal_funding", map[string]any{"entry_id": entryID, "idempotency_key": "japan-fund-reverse"})
	if reversed.IsError {
		t.Fatalf("reverse funding = %s", structuredJSON(t, reversed))
	}
	overview := callTool(t, session, "get_savings_overview", map[string]any{"include_closed_goals": true})
	if overview.IsError {
		t.Fatalf("overview = %s", structuredJSON(t, overview))
	}
	gotOverview := objectField(t, structuredObject(t, overview), "overview")
	if gotOverview["total_balance"] != "800.00" || gotOverview["total_allocated"] != "150.00" || gotOverview["total_unallocated"] != "650.00" {
		t.Fatalf("overview totals = %v", gotOverview)
	}
}
