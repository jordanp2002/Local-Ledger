package server_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
)

func TestRecurringManagementToolDiscoveryAndSchemas(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	update := toolByName(t, result.Tools, "update_recurring_transaction")
	if update == nil {
		t.Fatal("update_recurring_transaction is not discoverable")
	}
	updateSchema := schemaObject(t, update.InputSchema)
	if updateSchema["type"] != "object" {
		t.Fatalf("update schema type = %v, want object", updateSchema["type"])
	}
	updateRequired, _ := updateSchema["required"].([]any)
	if len(updateRequired) != 1 || !containsValue(updateRequired, "id") {
		t.Fatalf("update required = %v, want only id", updateRequired)
	}
	updateProperties, _ := updateSchema["properties"].(map[string]any)
	if !schemaTypeContains(updateProperties["id"].(map[string]any)["type"], "integer") || !schemaTypeContains(updateProperties["day_of_month"].(map[string]any)["type"], "integer") {
		t.Fatalf("update numeric properties = %v", updateProperties)
	}
	if !schemaTypeContains(updateProperties["note"].(map[string]any)["type"], "null") {
		t.Fatalf("update note schema = %v, want null accepted", updateProperties["note"])
	}
	if update.Annotations == nil || update.Annotations.ReadOnlyHint || !update.Annotations.IdempotentHint || update.Annotations.DestructiveHint == nil || !*update.Annotations.DestructiveHint {
		t.Fatalf("update annotations = %#v", update.Annotations)
	}
	if !strings.Contains(update.Description, "without changing") {
		t.Fatalf("update description = %q", update.Description)
	}

	enable := toolByName(t, result.Tools, "enable_recurring_transaction")
	if enable == nil {
		t.Fatal("enable_recurring_transaction is not discoverable")
	}
	enableSchema := schemaObject(t, enable.InputSchema)
	enableRequired, _ := enableSchema["required"].([]any)
	if len(enableRequired) != 1 || !containsValue(enableRequired, "id") {
		t.Fatalf("enable required = %v, want only id", enableRequired)
	}

	upcoming := toolByName(t, result.Tools, "preview_upcoming_transactions")
	if upcoming == nil {
		t.Fatal("preview_upcoming_transactions is not discoverable")
	}
	upcomingSchema := schemaObject(t, upcoming.InputSchema)
	upcomingRequired, _ := upcomingSchema["required"].([]any)
	if len(upcomingRequired) != 0 || upcoming.Annotations == nil || !upcoming.Annotations.ReadOnlyHint || upcoming.Annotations.OpenWorldHint == nil || *upcoming.Annotations.OpenWorldHint {
		t.Fatalf("upcoming schema/annotations = (%v, %#v)", upcomingSchema, upcoming.Annotations)
	}
	if !strings.Contains(upcoming.Description, "without writing") {
		t.Fatalf("upcoming description = %q", upcoming.Description)
	}
}

func TestRecurringManagementToolsUpdateEnableAndUpcoming(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
	callTool(t, session, "create_category", map[string]any{"name": "Entertainment"})
	created := callTool(t, session, "create_recurring_transaction", map[string]any{
		"merchant":     "Netflix",
		"amount":       "22.99",
		"category":     "Entertainment",
		"day_of_month": 15,
		"note":         "old",
	})
	if created.IsError {
		t.Fatalf("create recurring: %s", structuredJSON(t, created))
	}
	id := recurringID(t, objectField(t, structuredObject(t, created), "recurring_transaction")["id"])

	updated := callTool(t, session, "update_recurring_transaction", map[string]any{
		"id":     id,
		"amount": "24.99",
		"note":   nil,
	})
	if updated.IsError {
		t.Fatalf("update recurring: %s", structuredJSON(t, updated))
	}
	updatedObject := structuredObject(t, updated)
	if updatedObject["ok"] != true || updatedObject["changed"] != true || objectField(t, updatedObject, "recurring_transaction")["amount"] != "24.99" || objectField(t, updatedObject, "recurring_transaction")["note"] != nil {
		t.Fatalf("update response = %s", structuredJSON(t, updated))
	}

	disabled := callTool(t, session, "disable_recurring_transaction", map[string]any{"id": id})
	if disabled.IsError {
		t.Fatalf("disable recurring: %s", structuredJSON(t, disabled))
	}
	enabled := callTool(t, session, "enable_recurring_transaction", map[string]any{"id": id})
	if enabled.IsError {
		t.Fatalf("enable recurring: %s", structuredJSON(t, enabled))
	}
	if structuredObject(t, enabled)["changed"] != true {
		t.Fatalf("enable response = %s", structuredJSON(t, enabled))
	}

	upcoming := callTool(t, session, "preview_upcoming_transactions", map[string]any{})
	if upcoming.IsError {
		t.Fatalf("upcoming preview: %s", structuredJSON(t, upcoming))
	}
	got := structuredObject(t, upcoming)
	rows, ok := got["upcoming_transactions"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("upcoming rows = %#v, want one row", got["upcoming_transactions"])
	}
	row := rows[0].(map[string]any)
	if got["total_amount"] != "24.99" || row["scheduled_date"] != "2026-08-15" || row["status"] != "due" || row["note"] != nil {
		t.Fatalf("upcoming response = %s", structuredJSON(t, upcoming))
	}
	if got["blocked"] == nil {
		t.Fatal("blocked = nil, want an array")
	}
}

func recurringID(t *testing.T, value any) int64 {
	t.Helper()
	switch value := value.(type) {
	case float64:
		return int64(value)
	case int64:
		return value
	case json.Number:
		id, err := value.Int64()
		if err == nil {
			return id
		}
	}
	t.Fatalf("recurring id = %#v, want integer", value)
	return 0
}

func TestRecurringManagementToolErrorsUseExistingContracts(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
	invalid := callTool(t, session, "update_recurring_transaction", map[string]any{"id": 0, "amount": "20.00"})
	if !invalid.IsError {
		t.Fatal("invalid update IsError = false, want true")
	}
	if envelope := decodeRecurringErrorEnvelope(t, invalid); envelope.Error.Code != contract.ErrorCodeInvalidInput {
		t.Fatalf("invalid update code = %q", envelope.Error.Code)
	}
	empty := callTool(t, session, "update_recurring_transaction", map[string]any{"id": 1})
	if !empty.IsError {
		t.Fatal("empty update IsError = false, want true")
	}
	if envelope := decodeRecurringErrorEnvelope(t, empty); envelope.Error.Code != contract.ErrorCodeInvalidInput {
		t.Fatalf("empty update code = %q", envelope.Error.Code)
	}
	missing := callTool(t, session, "enable_recurring_transaction", map[string]any{"id": 999})
	if !missing.IsError {
		t.Fatal("missing enable IsError = false, want true")
	}
	if envelope := decodeRecurringErrorEnvelope(t, missing); envelope.Error.Code != contract.ErrorCodeRecurringTransactionNotFound {
		t.Fatalf("missing enable code = %q", envelope.Error.Code)
	}
}
