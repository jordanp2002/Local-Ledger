package server_test

import (
	"strings"
	"testing"
	"time"
)

func TestAccountActivityToolBasics(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)

	created := callTool(t, session, "create_account", map[string]any{"name": "Checking", "type": "checking", "opening_balance": "100.00"})
	if created.IsError {
		t.Fatalf("create: %s", structuredJSON(t, created))
	}
	id := int64(objectField(t, structuredObject(t, created), "account")["id"].(float64))

	dep := callTool(t, session, "record_account_activity", map[string]any{"account_id": id, "type": "deposit", "amount": "25.00", "date": "2026-08-14", "idempotency_key": "dep-1"})
	if dep.IsError {
		t.Fatalf("deposit: %s", structuredJSON(t, dep))
	}
	got := structuredObject(t, dep)
	if keys := objectKeys(got); strings.Join(keys, ",") != "balance,entry,idempotent_replay,ok" {
		t.Fatalf("record keys = %v", keys)
	}
	entry := objectField(t, got, "entry")
	if keys := objectKeys(entry); strings.Join(keys, ",") != "account,account_id,amount,balance_after,created_at,date,delta,id,kind,note,reversal_of_entry_id,transfer_id" {
		t.Fatalf("entry keys = %v", keys)
	}
	if entry["kind"] != "deposit" || entry["amount"] != "25.00" || entry["delta"] != "25.00" || entry["balance_after"] != "125.00" || got["balance"] != "125.00" {
		t.Fatalf("deposit = %s", structuredJSON(t, dep))
	}
	if entry["transfer_id"] != nil || entry["reversal_of_entry_id"] != nil {
		t.Fatalf("transfer/reversal = %s", structuredJSON(t, dep))
	}
	entryID := int64(entry["id"].(float64))

	wd := callTool(t, session, "record_account_activity", map[string]any{"account_id": id, "type": "withdrawal", "amount": "5.00", "date": "2026-08-14", "idempotency_key": "wd-1"})
	if wd.IsError {
		t.Fatalf("withdraw: %s", structuredJSON(t, wd))
	}
	if objectField(t, structuredObject(t, wd), "entry")["balance_after"] != "120.00" {
		t.Fatalf("withdraw = %s", structuredJSON(t, wd))
	}

	rec := callTool(t, session, "reconcile_account_balance", map[string]any{"account_id": id, "balance": "130.00", "idempotency_key": "rec-1"})
	if rec.IsError {
		t.Fatalf("reconcile: %s", structuredJSON(t, rec))
	}
	recObj := structuredObject(t, rec)
	if keys := objectKeys(recObj); strings.Join(keys, ",") != "adjustment,balance,changed,entry,idempotent_replay,ok,previous_balance" {
		t.Fatalf("reconcile keys = %v", keys)
	}
	if recObj["previous_balance"] != "120.00" || recObj["adjustment"] != "10.00" || recObj["balance"] != "130.00" || recObj["changed"] != true {
		t.Fatalf("reconcile = %s", structuredJSON(t, rec))
	}

	noop := callTool(t, session, "reconcile_account_balance", map[string]any{"account_id": id, "balance": "130.00", "idempotency_key": "noop-1"})
	if noop.IsError {
		t.Fatalf("noop: %s", structuredJSON(t, noop))
	}
	noopObj := structuredObject(t, noop)
	if noopObj["changed"] != false || noopObj["entry"] != nil || noopObj["adjustment"] != "0.00" {
		t.Fatalf("noop = %s", structuredJSON(t, noop))
	}

	listed := callTool(t, session, "list_account_activity", map[string]any{"account_id": id})
	if listed.IsError {
		t.Fatalf("list: %s", structuredJSON(t, listed))
	}
	listObj := structuredObject(t, listed)
	if keys := objectKeys(listObj); strings.Join(keys, ",") != "entries,ok,page" {
		t.Fatalf("list keys = %v", keys)
	}
	if rows, ok := listObj["entries"].([]any); !ok || rows == nil || len(rows) != 3 {
		t.Fatalf("entries = %#v", listObj["entries"])
	}

	rev := callTool(t, session, "reverse_account_activity", map[string]any{"id": entryID, "idempotency_key": "rv-1"})
	if rev.IsError {
		t.Fatalf("reverse: %s", structuredJSON(t, rev))
	}
	revObj := structuredObject(t, rev)
	if keys := objectKeys(revObj); strings.Join(keys, ",") != "balance,changed,entry,idempotent_replay,ok" {
		t.Fatalf("reverse keys = %v", keys)
	}
	if objectField(t, revObj, "entry")["delta"] != "-25.00" || revObj["changed"] != true {
		t.Fatalf("reverse = %s", structuredJSON(t, rev))
	}

	accounts := callTool(t, session, "list_accounts", map[string]any{})
	rows := structuredObject(t, accounts)["accounts"].([]any)
	if rows[0].(map[string]any)["current_balance"] != "105.00" {
		t.Fatalf("derived = %s", structuredJSON(t, accounts))
	}
}

func TestAccountActivityValidationErrors(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
	created := callTool(t, session, "create_account", map[string]any{"name": "C", "type": "cash", "opening_balance": "0.00"})
	id := int64(objectField(t, structuredObject(t, created), "account")["id"].(float64))

	for name, tc := range map[string]struct {
		tool string
		args map[string]any
	}{
		"bad type":     {"record_account_activity", map[string]any{"account_id": id, "type": "x", "amount": "1.00", "date": "2026-08-14", "idempotency_key": "k1"}},
		"zero amount":  {"record_account_activity", map[string]any{"account_id": id, "type": "deposit", "amount": "0.00", "date": "2026-08-14", "idempotency_key": "k2"}},
		"future date":  {"record_account_activity", map[string]any{"account_id": id, "type": "deposit", "amount": "1.00", "date": "2026-08-16", "idempotency_key": "k3"}},
		"empty key":    {"record_account_activity", map[string]any{"account_id": id, "type": "deposit", "amount": "1.00", "date": "2026-08-14", "idempotency_key": ""}},
		"bad balance":  {"reconcile_account_balance", map[string]any{"account_id": id, "balance": "1.000", "idempotency_key": "k4"}},
		"bad account":  {"record_account_activity", map[string]any{"account_id": 0, "type": "deposit", "amount": "1.00", "date": "2026-08-14", "idempotency_key": "k5"}},
		"bad entry":    {"reverse_account_activity", map[string]any{"id": 0, "idempotency_key": "k6"}},
		"bad limit":    {"list_account_activity", map[string]any{"account_id": id, "limit": 0}},
		"bad kind":     {"list_account_activity", map[string]any{"account_id": id, "kind": "x"}},
		"range invert": {"list_account_activity", map[string]any{"account_id": id, "start_date": "2026-08-15", "end_date": "2026-08-14"}},
	} {
		result := callTool(t, session, tc.tool, tc.args)
		if !result.IsError {
			t.Fatalf("%s IsError=false", name)
		}
		errObj := structuredObject(t, result)["error"].(map[string]any)
		if errObj["code"] != "invalid_input" {
			t.Fatalf("%s = %s", name, structuredJSON(t, result))
		}
		if details, ok := errObj["details"].(map[string]any); !ok || details["fields"] == nil {
			t.Fatalf("%s details = %v", name, errObj)
		}
	}
}

func TestAccountActivityStructuredErrors(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)

	missing := callTool(t, session, "record_account_activity", map[string]any{"account_id": 9999, "type": "deposit", "amount": "1.00", "date": "2026-08-14", "idempotency_key": "k"})
	if structuredObject(t, missing)["error"].(map[string]any)["code"] != "account_not_found" {
		t.Fatalf("missing account = %s", structuredJSON(t, missing))
	}

	created := callTool(t, session, "create_account", map[string]any{"name": "Z", "type": "cash", "opening_balance": "0.00"})
	id := int64(objectField(t, structuredObject(t, created), "account")["id"].(float64))
	disabled := callTool(t, session, "disable_account", map[string]any{"id": id})
	if disabled.IsError {
		t.Fatalf("disable: %s", structuredJSON(t, disabled))
	}
	inactive := callTool(t, session, "record_account_activity", map[string]any{"account_id": id, "type": "deposit", "amount": "1.00", "date": "2026-08-14", "idempotency_key": "k2"})
	if structuredObject(t, inactive)["error"].(map[string]any)["code"] != "account_inactive" {
		t.Fatalf("inactive = %s", structuredJSON(t, inactive))
	}

	missingEntry := callTool(t, session, "reverse_account_activity", map[string]any{"id": 9999, "idempotency_key": "k3"})
	if structuredObject(t, missingEntry)["error"].(map[string]any)["code"] != "account_entry_not_found" {
		t.Fatalf("missing entry = %s", structuredJSON(t, missingEntry))
	}

	active := callTool(t, session, "create_account", map[string]any{"name": "A", "type": "cash", "opening_balance": "0.00"})
	aid := int64(objectField(t, structuredObject(t, active), "account")["id"].(float64))
	dep := callTool(t, session, "record_account_activity", map[string]any{"account_id": aid, "type": "deposit", "amount": "4.00", "date": "2026-08-14", "idempotency_key": "d"})
	depID := int64(objectField(t, structuredObject(t, dep), "entry")["id"].(float64))
	rev := callTool(t, session, "reverse_account_activity", map[string]any{"id": depID, "idempotency_key": "rv"})
	revID := int64(objectField(t, structuredObject(t, rev), "entry")["id"].(float64))
	revOfRev := callTool(t, session, "reverse_account_activity", map[string]any{"id": revID, "idempotency_key": "rv2"})
	if structuredObject(t, revOfRev)["error"].(map[string]any)["code"] != "account_entry_not_reversible" {
		t.Fatalf("reversal of reversal = %s", structuredJSON(t, revOfRev))
	}

	conflict := callTool(t, session, "record_account_activity", map[string]any{"account_id": aid, "type": "deposit", "amount": "9.00", "date": "2026-08-14", "idempotency_key": "d"})
	errObj := structuredObject(t, conflict)["error"].(map[string]any)
	if errObj["code"] != "idempotency_conflict" {
		t.Fatalf("conflict = %s", structuredJSON(t, conflict))
	}
	if details, ok := errObj["details"].(map[string]any); !ok || details["idempotency_key"] != "d" || details["reason"] != "payload_mismatch" {
		t.Fatalf("conflict details = %v", errObj)
	}
}

func TestAccountActivityOverflowIsInternalError(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
	created := callTool(t, session, "create_account", map[string]any{"name": "Big", "type": "checking", "opening_balance": "92233720368547758.07"})
	id := int64(objectField(t, structuredObject(t, created), "account")["id"].(float64))
	result := callTool(t, session, "record_account_activity", map[string]any{"account_id": id, "type": "deposit", "amount": "0.01", "date": "2026-08-14", "idempotency_key": "over"})
	if !result.IsError {
		t.Fatal("overflow IsError=false")
	}
	errObj := structuredObject(t, result)["error"].(map[string]any)
	if errObj["code"] != "internal_error" {
		t.Fatalf("overflow = %s", structuredJSON(t, result))
	}
	if strings.Contains(structuredJSON(t, result), "sqlite") || strings.Contains(structuredJSON(t, result), "overflow") {
		t.Fatalf("overflow leaked detail: %s", structuredJSON(t, result))
	}
	listed := callTool(t, session, "list_account_activity", map[string]any{"account_id": id})
	if rows := structuredObject(t, listed)["entries"].([]any); len(rows) != 0 {
		t.Fatalf("entries after overflow = %d", len(rows))
	}
}

func TestAccountActivityToolDescriptions(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)
	result, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	want := map[string][]string{
		"record_account_activity":   {"local", "never contacts a bank", "budgets"},
		"reconcile_account_balance": {"local", "never contacts a bank"},
		"list_account_activity":     {"running balances"},
		"reverse_account_activity":  {"never edited or deleted"},
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
		t.Fatalf("activity tools missing descriptions: %v", want)
	}
}
