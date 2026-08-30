package server_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCreateRecurringTransactionToolSuccess(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)

	callTool(t, session, "create_category", map[string]any{"name": "Entertainment"})

	result := callTool(t, session, "create_recurring_transaction", map[string]any{
		"merchant":     "Netflix",
		"amount":       "22.99",
		"category":     "Entertainment",
		"day_of_month": 15,
		"note":         "Monthly subscription",
	})
	if result.IsError {
		t.Fatalf("create_recurring_transaction IsError = true, want success: %s", structuredJSON(t, result))
	}

	got := structuredObject(t, result)
	if got["ok"] != true {
		t.Fatalf("ok = %v, want true", got["ok"])
	}

	tmplObj := objectField(t, got, "recurring_transaction")
	if tmplObj["merchant"] != "Netflix" {
		t.Errorf("merchant = %v, want Netflix", tmplObj["merchant"])
	}
	if tmplObj["amount"] != "22.99" {
		t.Errorf("amount = %v, want 22.99", tmplObj["amount"])
	}
	if tmplObj["category"] != "Entertainment" {
		t.Errorf("category = %v, want Entertainment", tmplObj["category"])
	}
	if tmplObj["category_active"] != true {
		t.Errorf("category_active = %v, want true", tmplObj["category_active"])
	}
	if tmplObj["day_of_month"] != float64(15) && tmplObj["day_of_month"] != int64(15) {
		t.Errorf("day_of_month = %v, want 15", tmplObj["day_of_month"])
	}
	if tmplObj["note"] != "Monthly subscription" {
		t.Errorf("note = %v, want Monthly subscription", tmplObj["note"])
	}
	if tmplObj["active"] != true {
		t.Errorf("active = %v, want true", tmplObj["active"])
	}
}

func TestCreateRecurringTransactionToolInvalidInput(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)

	result := callTool(t, session, "create_recurring_transaction", map[string]any{
		"merchant":     "",
		"amount":       "-5.00",
		"category":     "",
		"day_of_month": 35,
	})
	if !result.IsError {
		t.Fatal("create_recurring_transaction IsError = false, want true")
	}

	envelope := decodeRecurringErrorEnvelope(t, result)
	if envelope.Error.Code != contract.ErrorCodeInvalidInput {
		t.Fatalf("error code = %q, want %q", envelope.Error.Code, contract.ErrorCodeInvalidInput)
	}

	fields, ok := envelope.Error.Details["fields"].([]any)
	if !ok || len(fields) != 4 {
		t.Fatalf("details.fields = %v, want 4 field issues", envelope.Error.Details["fields"])
	}
}

func TestCreateRecurringTransactionToolCategoryNotFound(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)
	callTool(t, session, "create_category", map[string]any{"name": "Groceries"})

	result := callTool(t, session, "create_recurring_transaction", map[string]any{
		"merchant":     "Netflix",
		"amount":       "22.99",
		"category":     "Entertainment",
		"day_of_month": 15,
	})
	if !result.IsError {
		t.Fatal("create_recurring_transaction IsError = false, want error")
	}

	envelope := decodeRecurringErrorEnvelope(t, result)
	if envelope.Error.Code != contract.ErrorCodeCategoryNotFound {
		t.Fatalf("error code = %q, want %q", envelope.Error.Code, contract.ErrorCodeCategoryNotFound)
	}
	if envelope.Error.Details["requested_category"] != "Entertainment" {
		t.Errorf("requested_category = %v, want Entertainment", envelope.Error.Details["requested_category"])
	}
	cats, ok := envelope.Error.Details["categories"].([]any)
	if !ok || len(cats) != 1 {
		t.Fatalf("categories = %v, want 1 active category", envelope.Error.Details["categories"])
	}
}

func TestCreateRecurringTransactionToolCategoryInactive(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)
	callTool(t, session, "create_category", map[string]any{"name": "Entertainment"})
	callTool(t, session, "create_category", map[string]any{"name": "Groceries"})
	callTool(t, session, "disable_category", map[string]any{"name": "Entertainment"})

	result := callTool(t, session, "create_recurring_transaction", map[string]any{
		"merchant":     "Netflix",
		"amount":       "22.99",
		"category":     "Entertainment",
		"day_of_month": 15,
	})
	if !result.IsError {
		t.Fatal("create_recurring_transaction IsError = false, want error")
	}

	envelope := decodeRecurringErrorEnvelope(t, result)
	if envelope.Error.Code != contract.ErrorCodeCategoryInactive {
		t.Fatalf("error code = %q, want %q", envelope.Error.Code, contract.ErrorCodeCategoryInactive)
	}
	cat, ok := envelope.Error.Details["category"].(map[string]any)
	if !ok || cat["name"] != "Entertainment" {
		t.Errorf("category = %v, want Entertainment", envelope.Error.Details["category"])
	}
	activeCats, ok := envelope.Error.Details["active_categories"].([]any)
	if !ok || len(activeCats) != 1 {
		t.Fatalf("active_categories = %v, want 1 active category", envelope.Error.Details["active_categories"])
	}
}

func TestListRecurringTransactionsTool(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)

	resultEmpty := callTool(t, session, "list_recurring_transactions", map[string]any{})
	if resultEmpty.IsError {
		t.Fatalf("list_recurring_transactions error: %s", structuredJSON(t, resultEmpty))
	}
	gotEmpty := structuredObject(t, resultEmpty)
	if gotEmpty["ok"] != true {
		t.Fatalf("ok = %v, want true", gotEmpty["ok"])
	}
	itemsEmpty, ok := gotEmpty["recurring_transactions"].([]any)
	if !ok || len(itemsEmpty) != 0 {
		t.Fatalf("recurring_transactions = %v, want empty array", gotEmpty["recurring_transactions"])
	}

	callTool(t, session, "create_category", map[string]any{"name": "Entertainment"})
	callTool(t, session, "create_category", map[string]any{"name": "Housing"})

	callTool(t, session, "create_recurring_transaction", map[string]any{
		"merchant":     "Spotify",
		"amount":       "10.99",
		"category":     "Entertainment",
		"day_of_month": 15,
	})
	callTool(t, session, "create_recurring_transaction", map[string]any{
		"merchant":     "Rent",
		"amount":       "1500.00",
		"category":     "Housing",
		"day_of_month": 1,
	})

	resultPopulated := callTool(t, session, "list_recurring_transactions", map[string]any{})
	if resultPopulated.IsError {
		t.Fatalf("list_recurring_transactions error: %s", structuredJSON(t, resultPopulated))
	}
	gotPopulated := structuredObject(t, resultPopulated)
	itemsPopulated, ok := gotPopulated["recurring_transactions"].([]any)
	if !ok || len(itemsPopulated) != 2 {
		t.Fatalf("recurring_transactions = %v, want 2 templates", gotPopulated["recurring_transactions"])
	}

	first := itemsPopulated[0].(map[string]any)
	if first["merchant"] != "Rent" {
		t.Errorf("first merchant = %v, want Rent", first["merchant"])
	}
	second := itemsPopulated[1].(map[string]any)
	if second["merchant"] != "Spotify" {
		t.Errorf("second merchant = %v, want Spotify", second["merchant"])
	}
}

func TestDisableRecurringTransactionTool(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)
	callTool(t, session, "create_category", map[string]any{"name": "Entertainment"})

	createRes := callTool(t, session, "create_recurring_transaction", map[string]any{
		"merchant":     "Netflix",
		"amount":       "22.99",
		"category":     "Entertainment",
		"day_of_month": 15,
	})
	createObj := structuredObject(t, createRes)
	tmplObj := objectField(t, createObj, "recurring_transaction")
	var id int64
	switch v := tmplObj["id"].(type) {
	case float64:
		id = int64(v)
	case int64:
		id = v
	case json.Number:
		n, _ := v.Int64()
		id = n
	}

	disableRes1 := callTool(t, session, "disable_recurring_transaction", map[string]any{"id": id})
	if disableRes1.IsError {
		t.Fatalf("disable_recurring_transaction error: %s", structuredJSON(t, disableRes1))
	}
	disableObj1 := structuredObject(t, disableRes1)
	if disableObj1["ok"] != true || disableObj1["changed"] != true {
		t.Fatalf("disable response = %v, want ok=true, changed=true", disableObj1)
	}
	afterDisable := objectField(t, disableObj1, "recurring_transaction")
	if afterDisable["active"] != false {
		t.Errorf("active = %v, want false", afterDisable["active"])
	}

	disableRes2 := callTool(t, session, "disable_recurring_transaction", map[string]any{"id": id})
	if disableRes2.IsError {
		t.Fatalf("repeated disable_recurring_transaction error: %s", structuredJSON(t, disableRes2))
	}
	disableObj2 := structuredObject(t, disableRes2)
	if disableObj2["ok"] != true || disableObj2["changed"] != false {
		t.Fatalf("repeated disable response = %v, want ok=true, changed=false", disableObj2)
	}

	missingRes := callTool(t, session, "disable_recurring_transaction", map[string]any{"id": 999999})
	if !missingRes.IsError {
		t.Fatal("disable missing ID IsError = false, want true")
	}
	missingEnvelope := decodeRecurringErrorEnvelope(t, missingRes)
	if missingEnvelope.Error.Code != contract.ErrorCodeRecurringTransactionNotFound {
		t.Fatalf("error code = %q, want %q", missingEnvelope.Error.Code, contract.ErrorCodeRecurringTransactionNotFound)
	}

	invalidRes := callTool(t, session, "disable_recurring_transaction", map[string]any{"id": 0})
	if !invalidRes.IsError {
		t.Fatal("disable invalid ID IsError = false, want true")
	}
	invalidEnvelope := decodeRecurringErrorEnvelope(t, invalidRes)
	if invalidEnvelope.Error.Code != contract.ErrorCodeInvalidInput {
		t.Fatalf("error code = %q, want %q", invalidEnvelope.Error.Code, contract.ErrorCodeInvalidInput)
	}
}

func TestRecurringToolsInternalErrorRedaction(t *testing.T) {
	db := openCategoryDB(t)
	session := connectCategorySession(t, db, time.Now, nil)

	db.Close()

	result := callTool(t, session, "create_recurring_transaction", map[string]any{
		"merchant":     "Netflix",
		"amount":       "22.99",
		"category":     "Entertainment",
		"day_of_month": 15,
	})
	if !result.IsError {
		t.Fatal("IsError = false, want true for closed database")
	}

	envelope := decodeRecurringErrorEnvelope(t, result)
	if envelope.Error.Code != contract.ErrorCodeInternalError {
		t.Fatalf("error code = %q, want %q", envelope.Error.Code, contract.ErrorCodeInternalError)
	}
	raw := structuredJSON(t, result)
	if leakedInternalError(raw) {
		t.Fatalf("internal error leaked driver details: %s", raw)
	}
}

func TestPreviewDueTransactionsToolDiscoveryAndSchema(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	tool := toolByName(t, result.Tools, "preview_due_transactions")
	if tool == nil {
		t.Fatal("preview_due_transactions is not discoverable")
	}
	schema := schemaObject(t, tool.InputSchema)
	if schema["type"] != "object" {
		t.Fatalf("preview_due_transactions input schema type = %v, want object", schema["type"])
	}
	required, _ := schema["required"].([]any)
	if len(required) != 0 {
		t.Fatalf("preview_due_transactions required = %v, want no required fields", required)
	}
	if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
		t.Fatalf("annotations = %#v, want read-only", tool.Annotations)
	}
	if tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
		t.Fatalf("openWorldHint = %v, want false", tool.Annotations.OpenWorldHint)
	}
	if tool.Annotations.DestructiveHint != nil {
		t.Fatalf("destructiveHint = %v, want omitted for read-only tool", *tool.Annotations.DestructiveHint)
	}
	if !strings.Contains(tool.Description, "without writing") || !strings.Contains(tool.Description, "confirmation") {
		t.Fatalf("description = %q, want no-write and confirmation guidance", tool.Description)
	}
}

func TestPreviewDueTransactionsToolEmpty(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)

	result := callTool(t, session, "preview_due_transactions", map[string]any{})
	if result.IsError {
		t.Fatalf("preview_due_transactions IsError = true, want success: %s", structuredJSON(t, result))
	}

	got := structuredObject(t, result)
	if got["ok"] != true {
		t.Fatalf("ok = %v, want true", got["ok"])
	}
	if got["as_of_date"] != "2026-08-15" {
		t.Errorf("as_of_date = %v, want 2026-08-15", got["as_of_date"])
	}

	if got["month"] != "2026-08" {
		t.Errorf("month = %v, want 2026-08", got["month"])
	}
	if got["total_amount"] != "0.00" {
		t.Errorf("total_amount = %v, want 0.00", got["total_amount"])
	}
	due, ok := got["due_transactions"].([]any)
	if !ok || len(due) != 0 {
		t.Fatalf("due_transactions = %v, want empty array", got["due_transactions"])
	}
	blocked, ok := got["blocked"].([]any)
	if !ok || len(blocked) != 0 {
		t.Fatalf("blocked = %v, want empty array", got["blocked"])
	}
}

func TestPreviewDueTransactionsToolSuccessWithDueAndBlocked(t *testing.T) {
	toronto := time.FixedZone("EDT", -4*60*60)
	now := func() time.Time { return time.Date(2026, 8, 30, 10, 0, 0, 0, toronto) }
	session := connectCategorySession(t, openCategoryDB(t), now, nil)

	callTool(t, session, "create_category", map[string]any{"name": "Housing"})
	callTool(t, session, "create_category", map[string]any{"name": "Entertainment"})
	callTool(t, session, "create_category", map[string]any{"name": "Fitness"})

	callTool(t, session, "create_recurring_transaction", map[string]any{
		"merchant":     "Rent",
		"amount":       "1500.00",
		"category":     "Housing",
		"day_of_month": 1,
	})
	callTool(t, session, "create_recurring_transaction", map[string]any{
		"merchant":     "Netflix",
		"amount":       "22.99",
		"category":     "Entertainment",
		"day_of_month": 15,
		"note":         "Monthly subscription",
	})
	callTool(t, session, "create_recurring_transaction", map[string]any{
		"merchant":     "Gym",
		"amount":       "50.00",
		"category":     "Fitness",
		"day_of_month": 10,
	})
	callTool(t, session, "disable_category", map[string]any{"name": "Fitness"})

	result := callTool(t, session, "preview_due_transactions", map[string]any{})
	if result.IsError {
		t.Fatalf("preview_due_transactions IsError = true, want success: %s", structuredJSON(t, result))
	}

	got := structuredObject(t, result)
	if got["ok"] != true {
		t.Fatalf("ok = %v, want true", got["ok"])
	}
	if got["as_of_date"] != "2026-08-30" {
		t.Errorf("as_of_date = %v, want 2026-08-30", got["as_of_date"])
	}
	if got["month"] != "2026-08" {
		t.Errorf("month = %v, want 2026-08", got["month"])
	}
	if got["total_amount"] != "1522.99" {
		t.Errorf("total_amount = %v, want 1522.99", got["total_amount"])
	}

	due, ok := got["due_transactions"].([]any)
	if !ok || len(due) != 2 {
		t.Fatalf("due_transactions = %v, want 2 due rows", got["due_transactions"])
	}
	rent := due[0].(map[string]any)
	if rent["merchant"] != "Rent" || rent["amount"] != "1500.00" || rent["due_date"] != "2026-08-01" || rent["note"] != nil {
		t.Errorf("rent = %v", rent)
	}
	netflix := due[1].(map[string]any)
	if netflix["merchant"] != "Netflix" || netflix["amount"] != "22.99" || netflix["due_date"] != "2026-08-15" || netflix["note"] != "Monthly subscription" {
		t.Errorf("netflix = %v", netflix)
	}

	blocked, ok := got["blocked"].([]any)
	if !ok || len(blocked) != 1 {
		t.Fatalf("blocked = %v, want 1 blocked row", got["blocked"])
	}
	gym := blocked[0].(map[string]any)
	if gym["merchant"] != "Gym" || gym["category"] != "Fitness" || gym["due_date"] != "2026-08-10" || gym["reason"] != "category_inactive" {
		t.Errorf("gym = %v", gym)
	}
}

func TestPreviewDueTransactionsToolClosedDB(t *testing.T) {
	db := openCategoryDB(t)
	session := connectCategorySession(t, db, time.Now, nil)

	db.Close()

	result := callTool(t, session, "preview_due_transactions", map[string]any{})
	if !result.IsError {
		t.Fatal("IsError = false, want true for closed database")
	}

	envelope := decodeRecurringErrorEnvelope(t, result)
	if envelope.Error.Code != contract.ErrorCodeInternalError {
		t.Fatalf("error code = %q, want %q", envelope.Error.Code, contract.ErrorCodeInternalError)
	}
	raw := structuredJSON(t, result)
	if leakedInternalError(raw) {
		t.Fatalf("internal error leaked driver details: %s", raw)
	}
}

func TestMaterializeDueTransactionsToolDiscoveryAndSchema(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	tool := toolByName(t, result.Tools, "materialize_due_transactions")
	if tool == nil {
		t.Fatal("materialize_due_transactions is not discoverable")
	}
	schema := schemaObject(t, tool.InputSchema)
	if schema["type"] != "object" {
		t.Fatalf("materialize_due_transactions input schema type = %v, want object", schema["type"])
	}
	required, _ := schema["required"].([]any)
	if len(required) != 0 {
		t.Fatalf("materialize_due_transactions required = %v, want no required fields", required)
	}
	if tool.Annotations == nil || tool.Annotations.ReadOnlyHint {
		t.Fatalf("annotations = %#v, want writable", tool.Annotations)
	}
	if !tool.Annotations.IdempotentHint {
		t.Fatalf("idempotentHint = false, want true")
	}
	if tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint {
		t.Fatalf("destructiveHint = %v, want true", tool.Annotations.DestructiveHint)
	}
	if tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
		t.Fatalf("openWorldHint = %v, want false", tool.Annotations.OpenWorldHint)
	}
	if !strings.Contains(tool.Description, "After user confirmation") || !strings.Contains(tool.Description, "retries are safe") {
		t.Fatalf("description = %q, want confirmation and retry guidance", tool.Description)
	}
}

func TestMaterializeDueTransactionsToolSuccessAndIntegration(t *testing.T) {
	toronto := time.FixedZone("EDT", -4*60*60)
	now := func() time.Time { return time.Date(2026, 8, 30, 10, 0, 0, 0, toronto) }
	session := connectCategorySession(t, openCategoryDB(t), now, nil)

	callTool(t, session, "create_category", map[string]any{"name": "Housing"})
	callTool(t, session, "create_category", map[string]any{"name": "Entertainment"})
	callTool(t, session, "create_monthly_budget", map[string]any{
		"month": "2026-08",
		"budgets": []map[string]any{
			{"category": "Housing", "amount": "2000.00"},
			{"category": "Entertainment", "amount": "100.00"},
		},
	})

	callTool(t, session, "create_recurring_transaction", map[string]any{
		"merchant":     "Rent",
		"amount":       "1500.00",
		"category":     "Housing",
		"day_of_month": 1,
	})
	callTool(t, session, "create_recurring_transaction", map[string]any{
		"merchant":     "Netflix",
		"amount":       "22.99",
		"category":     "Entertainment",
		"day_of_month": 15,
		"note":         "Monthly subscription",
	})

	result := callTool(t, session, "materialize_due_transactions", map[string]any{})
	if result.IsError {
		t.Fatalf("materialize_due_transactions error: %s", structuredJSON(t, result))
	}

	got := structuredObject(t, result)
	if got["ok"] != true {
		t.Fatalf("ok = %v, want true", got["ok"])
	}
	if got["as_of_date"] != "2026-08-30" || got["month"] != "2026-08" {
		t.Errorf("dates = (%v, %v)", got["as_of_date"], got["month"])
	}
	if got["created"] != float64(2) && got["created"] != int64(2) {
		t.Errorf("created = %v, want 2", got["created"])
	}
	if got["total_amount"] != "1522.99" {
		t.Errorf("total_amount = %v, want 1522.99", got["total_amount"])
	}

	txns, ok := got["transactions"].([]any)
	if !ok || len(txns) != 2 {
		t.Fatalf("transactions = %v, want 2 items", got["transactions"])
	}
	rentTxn := txns[0].(map[string]any)
	if rentTxn["merchant"] != "Rent" || rentTxn["amount"] != "1500.00" || rentTxn["date"] != "2026-08-01" {
		t.Errorf("rentTxn = %v", rentTxn)
	}
	netflixTxn := txns[1].(map[string]any)
	if netflixTxn["merchant"] != "Netflix" || netflixTxn["amount"] != "22.99" || netflixTxn["date"] != "2026-08-15" || netflixTxn["note"] != "Monthly subscription" {
		t.Errorf("netflixTxn = %v", netflixTxn)
	}

	repeatResult := callTool(t, session, "materialize_due_transactions", map[string]any{})
	if repeatResult.IsError {
		t.Fatalf("repeat error: %s", structuredJSON(t, repeatResult))
	}
	repeatGot := structuredObject(t, repeatResult)
	if repeatGot["created"] != float64(0) && repeatGot["created"] != int64(0) {
		t.Errorf("repeat created = %v, want 0", repeatGot["created"])
	}

	summaryResult := callTool(t, session, "get_monthly_summary", map[string]any{"month": "2026-08"})
	if summaryResult.IsError {
		t.Fatalf("get_monthly_summary error: %s", structuredJSON(t, summaryResult))
	}
	summaryObj := structuredObject(t, summaryResult)
	if summaryObj["total_spending"] != "1522.99" {
		t.Errorf("summary total_spending = %v, want 1522.99", summaryObj["total_spending"])
	}

	listResult := callTool(t, session, "list_transactions", map[string]any{"start_date": "2026-08-01", "end_date": "2026-08-31"})
	if listResult.IsError {
		t.Fatalf("list_transactions error: %s", structuredJSON(t, listResult))
	}
	listObj := structuredObject(t, listResult)
	listRows, ok := listObj["transactions"].([]any)
	if !ok {
		t.Fatalf("transactions = %T, want array", listObj["transactions"])
	}
	if len(listRows) != 2 {
		t.Fatalf("list_transactions rows = %d, want 2", len(listRows))
	}
}

func TestMaterializeDueTransactionsToolBlockedCategory(t *testing.T) {
	toronto := time.FixedZone("EDT", -4*60*60)
	now := func() time.Time { return time.Date(2026, 8, 30, 10, 0, 0, 0, toronto) }
	session := connectCategorySession(t, openCategoryDB(t), now, nil)

	callTool(t, session, "create_category", map[string]any{"name": "Housing"})
	callTool(t, session, "create_category", map[string]any{"name": "Fitness"})

	callTool(t, session, "create_recurring_transaction", map[string]any{
		"merchant":     "Rent",
		"amount":       "1500.00",
		"category":     "Housing",
		"day_of_month": 1,
	})
	callTool(t, session, "create_recurring_transaction", map[string]any{
		"merchant":     "Gym",
		"amount":       "50.00",
		"category":     "Fitness",
		"day_of_month": 10,
	})
	callTool(t, session, "disable_category", map[string]any{"name": "Fitness"})

	result := callTool(t, session, "materialize_due_transactions", map[string]any{})
	if !result.IsError {
		t.Fatal("materialize_due_transactions IsError = false, want true for blocked category")
	}

	envelope := decodeRecurringErrorEnvelope(t, result)
	if envelope.Error.Code != contract.ErrorCodeRecurringCategoryInactive {
		t.Fatalf("error code = %q, want %q", envelope.Error.Code, contract.ErrorCodeRecurringCategoryInactive)
	}
	if envelope.Error.Details["merchant"] != "Gym" || envelope.Error.Details["category"] != "Fitness" || envelope.Error.Details["due_date"] != "2026-08-10" {
		t.Fatalf("details = %v", envelope.Error.Details)
	}

	listResult := callTool(t, session, "list_transactions", map[string]any{})
	if listResult.IsError {
		t.Fatalf("list_transactions error: %s", structuredJSON(t, listResult))
	}
	listObj := structuredObject(t, listResult)
	listRows, ok := listObj["transactions"].([]any)
	if !ok {
		t.Fatalf("transactions = %T, want array", listObj["transactions"])
	}
	if len(listRows) != 0 {
		t.Fatalf("transactions created after blocker rollback: %d", len(listRows))
	}
}

func decodeRecurringErrorEnvelope(t *testing.T, result *mcp.CallToolResult) contract.ErrorEnvelope {
	t.Helper()
	var envelope contract.ErrorEnvelope
	if err := json.Unmarshal([]byte(mustJSON(t, result.StructuredContent)), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	return envelope
}
