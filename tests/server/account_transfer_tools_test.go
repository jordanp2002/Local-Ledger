package server_test

import (
	"strings"
	"testing"
	"time"
)

func TestAccountTransferToolBasics(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
	checking := callTool(t, session, "create_account", map[string]any{"name": "Checking", "type": "checking", "opening_balance": "100.00"})
	savings := callTool(t, session, "create_account", map[string]any{"name": "Savings", "type": "savings", "opening_balance": "-50.00"})
	if checking.IsError || savings.IsError {
		t.Fatalf("accounts = %s / %s", structuredJSON(t, checking), structuredJSON(t, savings))
	}
	sourceID := int64(objectField(t, structuredObject(t, checking), "account")["id"].(float64))
	destinationID := int64(objectField(t, structuredObject(t, savings), "account")["id"].(float64))

	transfer := callTool(t, session, "transfer_between_accounts", map[string]any{
		"source_account_id": sourceID, "destination_account_id": destinationID,
		"amount": "25.00", "date": "2026-08-14", "note": "move", "idempotency_key": "server-transfer",
	})
	if transfer.IsError {
		t.Fatalf("transfer: %s", structuredJSON(t, transfer))
	}
	transferObject := structuredObject(t, transfer)
	if keys := objectKeys(transferObject); strings.Join(keys, ",") != "destination_balance,executed_externally,idempotent_replay,ok,source_balance,transfer" {
		t.Fatalf("transfer keys = %v", keys)
	}
	if transferObject["source_balance"] != "75.00" || transferObject["destination_balance"] != "-25.00" || transferObject["executed_externally"] != false {
		t.Fatalf("transfer = %s", structuredJSON(t, transfer))
	}
	transferRecord := objectField(t, transferObject, "transfer")
	if transferRecord["source_account"] != "Checking" || transferRecord["destination_account"] != "Savings" || transferRecord["amount"] != "25.00" || transferRecord["status"] != "recorded" || transferRecord["reversal_of_transfer_id"] != nil {
		t.Fatalf("transfer record = %v", transferRecord)
	}
	transferID := int64(transferRecord["id"].(float64))

	replay := callTool(t, session, "transfer_between_accounts", map[string]any{
		"source_account_id": sourceID, "destination_account_id": destinationID,
		"amount": "25", "date": "2026-08-14", "note": "move", "idempotency_key": "server-transfer",
	})
	if replay.IsError || structuredObject(t, replay)["idempotent_replay"] != true {
		t.Fatalf("replay: %s", structuredJSON(t, replay))
	}
	conflict := callTool(t, session, "transfer_between_accounts", map[string]any{
		"source_account_id": sourceID, "destination_account_id": destinationID,
		"amount": "26.00", "date": "2026-08-14", "idempotency_key": "server-transfer",
	})
	if !conflict.IsError || structuredObject(t, conflict)["error"].(map[string]any)["code"] != "idempotency_conflict" {
		t.Fatalf("conflict: %s", structuredJSON(t, conflict))
	}

	listed := callTool(t, session, "list_account_transfers", map[string]any{"account_id": sourceID})
	if listed.IsError {
		t.Fatalf("list: %s", structuredJSON(t, listed))
	}
	listObject := structuredObject(t, listed)
	if rows, ok := listObject["transfers"].([]any); !ok || rows == nil || len(rows) != 1 {
		t.Fatalf("transfers = %#v", listObject["transfers"])
	}

	reversed := callTool(t, session, "reverse_account_transfer", map[string]any{"id": transferID, "note": "undo", "idempotency_key": "server-reverse"})
	if reversed.IsError {
		t.Fatalf("reverse: %s", structuredJSON(t, reversed))
	}
	reversedObject := structuredObject(t, reversed)
	if reversedObject["source_balance"] != "-50.00" || reversedObject["destination_balance"] != "100.00" || reversedObject["executed_externally"] != false || reversedObject["changed"] != true {
		t.Fatalf("reverse = %s", structuredJSON(t, reversed))
	}
	reversedRecord := objectField(t, reversedObject, "transfer")
	if reversedRecord["reversal_of_transfer_id"] != float64(transferID) || reversedRecord["status"] != "recorded" {
		t.Fatalf("reversed record = %v", reversedRecord)
	}

	closed := callTool(t, session, "list_account_transfers", map[string]any{"status": "reversed"})
	if closed.IsError {
		t.Fatalf("list reversed: %s", structuredJSON(t, closed))
	}
	if rows := structuredObject(t, closed)["transfers"].([]any); len(rows) != 1 {
		t.Fatalf("reversed transfers = %#v", rows)
	}
}

func TestAccountTransferToolValidationAndErrors(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
	created := callTool(t, session, "create_account", map[string]any{"name": "Checking", "type": "checking", "opening_balance": "0.00"})
	id := int64(objectField(t, structuredObject(t, created), "account")["id"].(float64))
	other := callTool(t, session, "create_account", map[string]any{"name": "Savings", "type": "savings", "opening_balance": "0.00"})
	otherID := int64(objectField(t, structuredObject(t, other), "account")["id"].(float64))
	for name, args := range map[string]map[string]any{
		"same account":           {"source_account_id": id, "destination_account_id": id, "amount": "1.00", "date": "2026-08-14", "idempotency_key": "same"},
		"zero amount":            {"source_account_id": id, "destination_account_id": otherID, "amount": "0.00", "date": "2026-08-14", "idempotency_key": "zero"},
		"negative amount":        {"source_account_id": id, "destination_account_id": otherID, "amount": "-1.00", "date": "2026-08-14", "idempotency_key": "negative"},
		"invalid source id":      {"source_account_id": 0, "destination_account_id": otherID, "amount": "1.00", "date": "2026-08-14", "idempotency_key": "invalid-source"},
		"invalid destination id": {"source_account_id": id, "destination_account_id": -1, "amount": "1.00", "date": "2026-08-14", "idempotency_key": "invalid-destination"},
		"future date":            {"source_account_id": id, "destination_account_id": otherID, "amount": "1.00", "date": "2026-08-16", "idempotency_key": "future"},
		"empty key":              {"source_account_id": id, "destination_account_id": otherID, "amount": "1.00", "date": "2026-08-14", "idempotency_key": ""},
	} {
		result := callTool(t, session, "transfer_between_accounts", args)
		if !result.IsError || structuredObject(t, result)["error"].(map[string]any)["code"] != "invalid_input" {
			t.Fatalf("%s = %s", name, structuredJSON(t, result))
		}
	}
	inactive := callTool(t, session, "create_account", map[string]any{"name": "Inactive", "type": "cash", "opening_balance": "0.00"})
	inactiveID := int64(objectField(t, structuredObject(t, inactive), "account")["id"].(float64))
	if disabled := callTool(t, session, "disable_account", map[string]any{"id": inactiveID}); disabled.IsError {
		t.Fatalf("disable inactive fixture: %s", structuredJSON(t, disabled))
	}
	for name, args := range map[string]map[string]any{
		"inactive source":      {"source_account_id": inactiveID, "destination_account_id": id, "amount": "1.00", "date": "2026-08-14", "idempotency_key": "inactive-source"},
		"inactive destination": {"source_account_id": id, "destination_account_id": inactiveID, "amount": "1.00", "date": "2026-08-14", "idempotency_key": "inactive-destination"},
	} {
		result := callTool(t, session, "transfer_between_accounts", args)
		if !result.IsError || structuredObject(t, result)["error"].(map[string]any)["code"] != "account_inactive" {
			t.Fatalf("%s = %s", name, structuredJSON(t, result))
		}
	}

	missing := callTool(t, session, "transfer_between_accounts", map[string]any{"source_account_id": id, "destination_account_id": 9999, "amount": "1.00", "date": "2026-08-14", "idempotency_key": "missing"})
	if structuredObject(t, missing)["error"].(map[string]any)["code"] != "account_not_found" {
		t.Fatalf("missing account = %s", structuredJSON(t, missing))
	}
	missingTransfer := callTool(t, session, "reverse_account_transfer", map[string]any{"id": 9999, "idempotency_key": "missing-reverse"})
	if structuredObject(t, missingTransfer)["error"].(map[string]any)["code"] != "account_transfer_not_found" {
		t.Fatalf("missing transfer = %s", structuredJSON(t, missingTransfer))
	}
}

func TestAccountTransferDescriptions(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)
	result, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	want := map[string][]string{
		"transfer_between_accounts": {"local", "never contacts a bank", "external transfer"},
		"list_account_transfers":    {"local", "canonical account identities"},
		"reverse_account_transfer":  {"local", "inverse transfer", "never contacts a bank"},
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
		t.Fatalf("transfer tools missing descriptions: %v", want)
	}
}
