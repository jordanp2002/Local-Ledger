package server_test

import (
	"strings"
	"testing"
	"time"
)

func TestAccountToolBasics(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)

	created := callTool(t, session, "create_account", map[string]any{"name": "Checking", "type": "checking", "opening_balance": "2500.00"})
	if created.IsError {
		t.Fatalf("create_account: %s", structuredJSON(t, created))
	}
	got := structuredObject(t, created)
	if keys := objectKeys(got); strings.Join(keys, ",") != "account,created,ok,reactivated" {
		t.Fatalf("create keys = %v", keys)
	}
	acct := objectField(t, got, "account")
	if keys := objectKeys(acct); strings.Join(keys, ",") != "active,created_at,current_balance,id,name,note,opening_balance,type,updated_at" {
		t.Fatalf("account keys = %v", keys)
	}
	if acct["opening_balance"] != "2500.00" || acct["current_balance"] != "2500.00" {
		t.Fatalf("balances = %v", acct)
	}
	id := int64(acct["id"].(float64))

	dup := callTool(t, session, "create_account", map[string]any{"name": "checking", "type": "checking", "opening_balance": "2500.00"})
	if !dup.IsError {
		t.Fatal("duplicate IsError=false")
	}
	if structuredObject(t, dup)["error"].(map[string]any)["code"] != "account_already_exists" {
		t.Fatalf("duplicate = %s", structuredJSON(t, dup))
	}

	listed := callTool(t, session, "list_accounts", map[string]any{})
	if listed.IsError {
		t.Fatalf("list: %s", structuredJSON(t, listed))
	}
	if rows := structuredObject(t, listed)["accounts"].([]any); len(rows) != 1 {
		t.Fatalf("list rows = %v", rows)
	}

	updated := callTool(t, session, "update_account", map[string]any{"id": id, "name": "Primary"})
	if updated.IsError {
		t.Fatalf("update: %s", structuredJSON(t, updated))
	}

	disabled := callTool(t, session, "disable_account", map[string]any{"id": id})
	if !disabled.IsError {
		t.Fatal("disable non-zero IsError=false")
	}
	if structuredObject(t, disabled)["error"].(map[string]any)["code"] != "account_balance_not_zero" {
		t.Fatalf("disable = %s", structuredJSON(t, disabled))
	}

	missing := callTool(t, session, "disable_account", map[string]any{"id": 9999})
	if structuredObject(t, missing)["error"].(map[string]any)["code"] != "account_not_found" {
		t.Fatalf("missing = %s", structuredJSON(t, missing))
	}
}

func TestAccountToolNoteClearing(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)

	created := callTool(t, session, "create_account", map[string]any{"name": "Checking", "type": "checking", "opening_balance": "10.00", "note": "my note"})
	if created.IsError {
		t.Fatalf("create: %s", structuredJSON(t, created))
	}
	id := int64(objectField(t, structuredObject(t, created), "account")["id"].(float64))

	cleared := callTool(t, session, "update_account", map[string]any{"id": id, "note": nil})
	if cleared.IsError {
		t.Fatalf("clear note: %s", structuredJSON(t, cleared))
	}
	clearedAcct := objectField(t, structuredObject(t, cleared), "account")
	if clearedAcct["note"] != nil {
		t.Fatalf("note after clear = %v, want null", clearedAcct["note"])
	}
}

func TestAccountToolReactivation(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)

	created := callTool(t, session, "create_account", map[string]any{"name": "Empty", "type": "savings", "opening_balance": "0.00"})
	if created.IsError {
		t.Fatalf("create: %s", structuredJSON(t, created))
	}
	id := int64(objectField(t, structuredObject(t, created), "account")["id"].(float64))

	disabled := callTool(t, session, "disable_account", map[string]any{"id": id})
	if disabled.IsError {
		t.Fatalf("disable: %s", structuredJSON(t, disabled))
	}

	reactivated := callTool(t, session, "create_account", map[string]any{"name": "empty", "type": "savings", "opening_balance": "0.00", "note": "back"})
	if reactivated.IsError {
		t.Fatalf("reactivate: %s", structuredJSON(t, reactivated))
	}
	payload := structuredObject(t, reactivated)
	if payload["created"] != false || payload["reactivated"] != true {
		t.Fatalf("reactivate flags = %s", structuredJSON(t, reactivated))
	}
	acct := objectField(t, payload, "account")
	if int64(acct["id"].(float64)) != id || acct["note"] != "back" {
		t.Fatalf("reactivated = %v", acct)
	}
}

func TestAccountToolFiltersAndEmptyArray(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)

	for _, a := range []map[string]any{
		{"name": "Checking", "type": "checking", "opening_balance": "1.00"},
		{"name": "Savings", "type": "savings", "opening_balance": "2.00"},
		{"name": "Zero", "type": "cash", "opening_balance": "0.00"},
	} {
		if result := callTool(t, session, "create_account", a); result.IsError {
			t.Fatalf("create %v: %s", a, structuredJSON(t, result))
		}
	}
	zeroList := callTool(t, session, "list_accounts", map[string]any{"name": "Zero"})
	zeroRows := structuredObject(t, zeroList)["accounts"].([]any)
	if len(zeroRows) != 1 {
		t.Fatalf("zero rows = %v", zeroRows)
	}
	zeroID := int64(zeroRows[0].(map[string]any)["id"].(float64))
	disableZero := callTool(t, session, "disable_account", map[string]any{"id": zeroID})
	if disableZero.IsError {
		t.Fatalf("disable zero: %s", structuredJSON(t, disableZero))
	}

	filtered := callTool(t, session, "list_accounts", map[string]any{"type": "checking"})
	if filtered.IsError {
		t.Fatalf("filter: %s", structuredJSON(t, filtered))
	}
	if rows := structuredObject(t, filtered)["accounts"].([]any); len(rows) != 1 {
		t.Fatalf("type filter rows = %v", rows)
	}

	withInactive := callTool(t, session, "list_accounts", map[string]any{"include_inactive": true})
	if rows := structuredObject(t, withInactive)["accounts"].([]any); len(rows) != 3 {
		t.Fatalf("include_inactive rows = %v", rows)
	}

	empty := callTool(t, session, "list_accounts", map[string]any{"type": "other"})
	if empty.IsError {
		t.Fatalf("empty: %s", structuredJSON(t, empty))
	}
	accounts, ok := structuredObject(t, empty)["accounts"].([]any)
	if !ok || accounts == nil || len(accounts) != 0 {
		t.Fatalf("empty accounts = %#v, want non-null []", structuredObject(t, empty)["accounts"])
	}
}

func TestAccountToolValidationErrors(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)

	for name, args := range map[string]map[string]any{
		"empty name":  {"name": "", "type": "checking", "opening_balance": "1.00"},
		"bad type":    {"name": "A", "type": "credit", "opening_balance": "1.00"},
		"bad amount":  {"name": "A", "type": "checking", "opening_balance": "1.000"},
		"empty patch": {"id": 1},
		"null name":   {"id": 1, "name": nil},
		"missing id":  {"id": 0, "name": "X"},
		"bad list":    {},
	} {
		tool := "create_account"
		if name == "empty patch" || name == "null name" || name == "missing id" {
			tool = "update_account"
		}
		if name == "bad list" {
			args = map[string]any{"type": "credit"}
			tool = "list_accounts"
		}
		result := callTool(t, session, tool, args)
		if !result.IsError {
			t.Fatalf("%s IsError=false", name)
		}
		errObj, ok := structuredObject(t, result)["error"].(map[string]any)
		if !ok || errObj["code"] != "invalid_input" {
			t.Fatalf("%s = %s, want invalid_input", name, structuredJSON(t, result))
		}
		details, ok := errObj["details"].(map[string]any)
		if !ok || details["fields"] == nil {
			t.Fatalf("%s details = %v, want fields", name, errObj)
		}
	}
}

func TestAccountToolDescriptions(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)
	result, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	want := map[string][]string{
		"create_account":  {"local", "opening balance", "reactivate"},
		"update_account":  {"rename", "note", "balances"},
		"list_accounts":   {"local", "balances"},
		"disable_account": {"zero balance", "history", "never deleted"},
	}
	for _, tool := range result.Tools {
		fragments, ok := want[tool.Name]
		if !ok {
			continue
		}
		description := strings.ToLower(tool.Description)
		for _, fragment := range fragments {
			if !strings.Contains(description, fragment) {
				t.Fatalf("%s description %q missing %q", tool.Name, tool.Description, fragment)
			}
		}
		delete(want, tool.Name)
	}
	if len(want) != 0 {
		t.Fatalf("account tools missing descriptions: %v", want)
	}
}
