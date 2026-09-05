package server_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/jordanp2002/local-finance-mcp/internal/database"
	"github.com/jordanp2002/local-finance-mcp/internal/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var categoryToolNames = []string{"add_split_transaction", "add_transaction", "add_transactions", "compare_months", "create_account", "create_budget_rollover", "create_category", "create_monthly_budget", "create_recurring_transaction", "disable_account", "disable_category", "disable_recurring_transaction", "disable_sinking_fund", "enable_recurring_transaction", "enable_sinking_fund", "get_category_summary", "get_monthly_series", "get_monthly_summary", "get_spending_summary", "list_account_activity", "list_account_transfers", "list_accounts", "list_budget_rollovers", "list_categories", "list_known_merchants", "list_recurring_transactions", "list_sinking_funds", "list_top_merchants", "list_transactions", "materialize_due_transactions", "preview_due_transactions", "preview_upcoming_transactions", "reconcile_account_balance", "record_account_activity", "remove_budget_rollover", "remove_known_merchant", "remove_transaction", "rename_category", "rename_known_merchant", "reverse_account_activity", "reverse_account_transfer", "set_budgets", "set_known_merchant", "transfer_between_accounts", "update_account", "update_recurring_transaction", "update_transaction"}

func TestCategoryToolDiscovery(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if got := listedToolNames(result.Tools); strings.Join(got, ",") != strings.Join(categoryToolNames, ",") {
		t.Fatalf("tools = %v, want %v", got, categoryToolNames)
	}

	byName := make(map[string]*mcp.Tool, len(result.Tools))
	for _, tool := range result.Tools {
		byName[tool.Name] = tool
	}
	for _, name := range []string{"create_category", "disable_category"} {
		schema := schemaObject(t, byName[name].InputSchema)
		if schema["type"] != "object" {
			t.Fatalf("%s input schema type = %v, want object", name, schema["type"])
		}
		required, _ := schema["required"].([]any)
		if !containsValue(required, "name") {
			t.Fatalf("%s input schema required = %v, want name", name, required)
		}
		properties, _ := schema["properties"].(map[string]any)
		nameSchema, _ := properties["name"].(map[string]any)
		if nameSchema["type"] != "string" {
			t.Fatalf("%s name type = %v, want string", name, nameSchema["type"])
		}
	}

	listSchema := schemaObject(t, byName["list_categories"].InputSchema)
	if listSchema["type"] != "object" {
		t.Fatalf("list_categories input schema type = %v, want object", listSchema["type"])
	}
}

func TestCreateCategorySuccess(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)

	result := callTool(t, session, "create_category", map[string]any{"name": "Groceries"})
	if result.IsError {
		t.Fatalf("create_category IsError = true, want success: %s", structuredJSON(t, result))
	}

	got := structuredObject(t, result)
	if keys := objectKeys(got); strings.Join(keys, ",") != "category,created,ok,reactivated" {
		t.Fatalf("create_category keys = %v, want [category created ok reactivated]", keys)
	}
	if got["ok"] != true {
		t.Fatalf("ok = %v, want true", got["ok"])
	}
	if got["created"] != true {
		t.Fatalf("created = %v, want true", got["created"])
	}
	if got["reactivated"] != false {
		t.Fatalf("reactivated = %v, want false", got["reactivated"])
	}

	cat := objectField(t, got, "category")
	if keys := objectKeys(cat); strings.Join(keys, ",") != "active,created_at,id,name,updated_at" {
		t.Fatalf("category keys = %v, want [active created_at id name updated_at]", keys)
	}
	if cat["name"] != "Groceries" {
		t.Fatalf("category name = %v, want Groceries", cat["name"])
	}
	if cat["active"] != true {
		t.Fatalf("category active = %v, want true", cat["active"])
	}
	if categoryID(t, cat) == 0 {
		t.Fatal("category id = 0")
	}
}

func TestCreateCategoryAlreadyExists(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)
	first := callTool(t, session, "create_category", map[string]any{"name": "Groceries"})
	if first.IsError {
		t.Fatalf("first create_category failed: %s", structuredJSON(t, first))
	}
	existing := decodeCategory(t, structuredObject(t, first)["category"])

	result := callTool(t, session, "create_category", map[string]any{"name": "Groceries"})
	if !result.IsError {
		t.Fatal("duplicate create_category IsError = false, want true")
	}

	want := contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeCategoryAlreadyExists,
		"Category 'Groceries' already exists.",
		false,
		map[string]any{"category": existing},
	))
	requireStructuredEqual(t, result, want)
}

func TestCategoryNameInvalidInput(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)
	want := invalidNameEnvelope("must not be empty")

	for _, tool := range []string{"create_category", "disable_category"} {
		for _, name := range []string{"", "   ", "\t\n\r\v\f"} {
			result := callTool(t, session, tool, map[string]any{"name": name})
			if !result.IsError {
				t.Fatalf("%s name %q IsError = false, want true", tool, name)
			}
			requireStructuredEqual(t, result, want)
		}
	}
}

func TestCategoryNameRejectsNUL(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)
	want := invalidNameEnvelope("must not contain NUL characters")

	for _, tool := range []string{"create_category", "disable_category"} {
		for _, name := range []string{"\x00", " \x00 ", "Food\x00Test"} {
			result := callTool(t, session, tool, map[string]any{"name": name})
			if !result.IsError {
				t.Fatalf("%s name %q IsError = false, want true", tool, name)
			}
			requireStructuredEqual(t, result, want)
		}
	}
}

func TestCategoryNameSchemaErrors(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)

	for _, tool := range []string{"create_category", "disable_category"} {
		t.Run(tool+"/missing", func(t *testing.T) {
			result := callTool(t, session, tool, map[string]any{})
			if !result.IsError {
				t.Fatalf("missing name IsError = false, want true")
			}
		})
		t.Run(tool+"/wrong-type", func(t *testing.T) {
			result := callTool(t, session, tool, map[string]any{"name": 12})
			if !result.IsError {
				t.Fatalf("numeric name IsError = false, want true")
			}
		})
	}
}

func TestListCategoriesEmpty(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)

	result := callTool(t, session, "list_categories", map[string]any{})
	if result.IsError {
		t.Fatalf("list_categories IsError = true: %s", structuredJSON(t, result))
	}

	got := structuredObject(t, result)
	if keys := objectKeys(got); strings.Join(keys, ",") != "categories,ok" {
		t.Fatalf("list_categories keys = %v, want [categories ok]", keys)
	}
	if got["ok"] != true {
		t.Fatalf("ok = %v, want true", got["ok"])
	}
	cats, ok := got["categories"].([]any)
	if !ok {
		t.Fatalf("categories = %T, want array", got["categories"])
	}
	if len(cats) != 0 {
		t.Fatalf("categories = %#v, want empty array", cats)
	}
}

func TestListCategoriesAfterCreateHidesDisabledAndKeepsID(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)

	created := callTool(t, session, "create_category", map[string]any{"name": "Groceries"})
	if created.IsError {
		t.Fatalf("create_category failed: %s", structuredJSON(t, created))
	}
	first := decodeCategory(t, structuredObject(t, created)["category"])

	listed := callTool(t, session, "list_categories", nil)
	if listed.IsError {
		t.Fatalf("list after create failed: %s", structuredJSON(t, listed))
	}
	active := decodeCategories(t, structuredObject(t, listed)["categories"])
	if len(active) != 1 || active[0].ID != first.ID || active[0].Name != "Groceries" {
		t.Fatalf("list after create = %#v, want Groceries id %d", active, first.ID)
	}

	disabled := callTool(t, session, "disable_category", map[string]any{"name": "Groceries"})
	if disabled.IsError {
		t.Fatalf("disable_category failed: %s", structuredJSON(t, disabled))
	}

	hidden := callTool(t, session, "list_categories", nil)
	if hidden.IsError {
		t.Fatalf("list after disable failed: %s", structuredJSON(t, hidden))
	}
	if cats := decodeCategories(t, structuredObject(t, hidden)["categories"]); len(cats) != 0 {
		t.Fatalf("list after disable = %#v, want empty", cats)
	}

	reactivated := callTool(t, session, "create_category", map[string]any{"name": "Groceries"})
	if reactivated.IsError {
		t.Fatalf("reactivate failed: %s", structuredJSON(t, reactivated))
	}
	again := structuredObject(t, reactivated)
	if again["ok"] != true || again["created"] != false || again["reactivated"] != true {
		t.Fatalf("reactivate payload = %s", structuredJSON(t, reactivated))
	}
	restored := decodeCategory(t, again["category"])
	if restored.ID != first.ID {
		t.Fatalf("reactivated id = %d, want %d", restored.ID, first.ID)
	}

	listedAgain := callTool(t, session, "list_categories", nil)
	if listedAgain.IsError {
		t.Fatalf("list after reactivate failed: %s", structuredJSON(t, listedAgain))
	}
	shown := decodeCategories(t, structuredObject(t, listedAgain)["categories"])
	if len(shown) != 1 || shown[0].ID != first.ID {
		t.Fatalf("list after reactivate = %#v, want id %d", shown, first.ID)
	}
}

func TestDisableCategorySuccess(t *testing.T) {
	now := func() time.Time {
		return time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	}
	db := openCategoryDB(t)
	session := connectCategorySession(t, db, now, nil)

	created := callTool(t, session, "create_category", map[string]any{"name": "Groceries"})
	if created.IsError {
		t.Fatalf("create_category failed: %s", structuredJSON(t, created))
	}
	cat := decodeCategory(t, structuredObject(t, created)["category"])

	withoutBudget := callTool(t, session, "disable_category", map[string]any{"name": "Groceries"})
	if withoutBudget.IsError {
		t.Fatalf("disable without budget failed: %s", structuredJSON(t, withoutBudget))
	}
	got := structuredObject(t, withoutBudget)
	if keys := objectKeys(got); strings.Join(keys, ",") != "category,changed,ok,removed_budget" {
		t.Fatalf("disable keys = %v, want [category changed ok removed_budget]", keys)
	}
	if got["ok"] != true {
		t.Fatalf("ok = %v, want true", got["ok"])
	}
	if got["changed"] != true {
		t.Fatalf("changed = %v, want true", got["changed"])
	}
	if got["removed_budget"] != nil {
		t.Fatalf("removed_budget = %#v, want null", got["removed_budget"])
	}

	reactivated := callTool(t, session, "create_category", map[string]any{"name": "Groceries"})
	if reactivated.IsError {
		t.Fatalf("reactivate failed: %s", structuredJSON(t, reactivated))
	}

	insertCurrentMonthBudget(t, db, cat.ID, "2026-08", 50000)
	withBudget := callTool(t, session, "disable_category", map[string]any{"name": "Groceries"})
	if withBudget.IsError {
		t.Fatalf("disable with budget failed: %s", structuredJSON(t, withBudget))
	}
	payload := structuredObject(t, withBudget)
	if payload["ok"] != true || payload["changed"] != true {
		t.Fatalf("disable with budget payload = %s", structuredJSON(t, withBudget))
	}
	if payload["removed_budget"] == nil {
		t.Fatal("removed_budget = null, want canonical budget")
	}
	removed := decodeBudget(t, payload["removed_budget"])
	if removed.CategoryID != cat.ID || removed.Category != "Groceries" || removed.Month != "2026-08" || removed.Amount != "500.00" {
		t.Fatalf("removed_budget = %#v, want Groceries 2026-08 500.00", removed)
	}
}

func TestDisableCategoryNotFound(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)
	created := callTool(t, session, "create_category", map[string]any{"name": "Groceries"})
	if created.IsError {
		t.Fatalf("create_category failed: %s", structuredJSON(t, created))
	}
	existing := decodeCategory(t, structuredObject(t, created)["category"])

	result := callTool(t, session, "disable_category", map[string]any{"name": "  Pharmacy  "})
	if !result.IsError {
		t.Fatal("disable missing category IsError = false, want true")
	}

	want := contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeCategoryNotFound,
		"Category 'Pharmacy' does not exist.",
		false,
		map[string]any{
			"requested_category": "Pharmacy",
			"categories":         []contract.Category{existing},
		},
	))
	requireStructuredEqual(t, result, want)
}

func TestDisableCategoryAlreadyInactive(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)
	if result := callTool(t, session, "create_category", map[string]any{"name": "Groceries"}); result.IsError {
		t.Fatalf("create_category failed: %s", structuredJSON(t, result))
	}
	if result := callTool(t, session, "disable_category", map[string]any{"name": "Groceries"}); result.IsError {
		t.Fatalf("first disable failed: %s", structuredJSON(t, result))
	}

	result := callTool(t, session, "disable_category", map[string]any{"name": "Groceries"})
	if result.IsError {
		t.Fatalf("second disable failed: %s", structuredJSON(t, result))
	}
	got := structuredObject(t, result)
	if got["ok"] != true {
		t.Fatalf("ok = %v, want true", got["ok"])
	}
	if got["changed"] != false {
		t.Fatalf("changed = %v, want false", got["changed"])
	}
	if got["removed_budget"] != nil {
		t.Fatalf("removed_budget = %#v, want null", got["removed_budget"])
	}
}

func TestCategoryToolInternalError(t *testing.T) {
	db := openCategoryDB(t)
	var logs bytes.Buffer
	session := connectCategorySession(t, db, time.Now, log.New(&logs, "", 0))
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{name: "create_category", args: map[string]any{"name": "Groceries"}},
		{name: "list_categories", args: map[string]any{}},
		{name: "disable_category", args: map[string]any{"name": "Groceries"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs.Reset()
			result := callTool(t, session, tc.name, tc.args)
			if !result.IsError {
				t.Fatal("IsError = false, want true")
			}
			requireStructuredEqual(t, result, contract.NewInternalErrorEnvelope())

			public := structuredJSON(t, result)
			if leakedInternalError(public) {
				t.Fatalf("public payload leaked internal details: %s", public)
			}
			if text := toolText(result); leakedInternalError(text) {
				t.Fatalf("tool text leaked internal details: %s", text)
			}
			if logs.Len() == 0 {
				t.Fatal("logger did not record the private cause")
			}
			if !strings.Contains(logs.String(), "sql:") && !strings.Contains(logs.String(), "database is closed") {
				t.Fatalf("logger = %q, want private database cause", logs.String())
			}
		})
	}
}

func openCategoryDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "finance.db")
	db, err := database.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func connectCategorySession(t *testing.T, db *sql.DB, now func() time.Time, logger *log.Logger) *mcp.ClientSession {
	t.Helper()
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	srv := server.New(db, now, logger)
	serverSession, err := srv.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil && !errors.Is(err, mcp.ErrConnectionClosed) {
			t.Errorf("close client session: %v", err)
		}
		if err := serverSession.Close(); err != nil && !errors.Is(err, mcp.ErrConnectionClosed) {
			t.Errorf("close server session: %v", err)
		}
	})
	return session
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return result
}

func listedToolNames(tools []*mcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

func structuredJSON(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	return string(raw)
}

func structuredObject(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	if result.StructuredContent == nil {
		t.Fatal("structured content is nil")
	}
	return asObject(t, result.StructuredContent)
}

func requireStructuredEqual(t *testing.T, result *mcp.CallToolResult, want any) {
	t.Helper()
	gotVal := decodeJSONValue(t, result.StructuredContent)
	wantVal := decodeJSONValue(t, want)
	if !reflect.DeepEqual(gotVal, wantVal) {
		t.Fatalf("structured content = %s, want %s", structuredJSON(t, result), mustJSON(t, want))
	}
}

func decodeJSONValue(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json value: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal json value %s: %v", raw, err)
	}
	return decoded
}

func asObject(t *testing.T, value any) map[string]any {
	t.Helper()
	decoded := decodeJSONValue(t, value)
	obj, ok := decoded.(map[string]any)
	if !ok {
		t.Fatalf("json value %T is not an object", decoded)
	}
	return obj
}

func objectField(t *testing.T, obj map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := obj[key]
	if !ok {
		t.Fatalf("missing %q", key)
	}
	field, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s = %T, want object", key, value)
	}
	return field
}

func objectKeys(obj map[string]any) []string {
	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func schemaObject(t *testing.T, schema any) map[string]any {
	t.Helper()
	return asObject(t, schema)
}

func containsValue(values []any, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func categoryID(t *testing.T, cat map[string]any) int64 {
	t.Helper()
	switch id := cat["id"].(type) {
	case float64:
		return int64(id)
	case json.Number:
		n, err := id.Int64()
		if err != nil {
			t.Fatalf("category id: %v", err)
		}
		return n
	default:
		t.Fatalf("category id type = %T", cat["id"])
		return 0
	}
}

func decodeCategory(t *testing.T, value any) contract.Category {
	t.Helper()
	var cat contract.Category
	if err := json.Unmarshal([]byte(mustJSON(t, value)), &cat); err != nil {
		t.Fatalf("decode category: %v", err)
	}
	return cat
}

func decodeCategories(t *testing.T, value any) []contract.Category {
	t.Helper()
	if value == nil {
		t.Fatal("categories is null")
	}
	var cats []contract.Category
	if err := json.Unmarshal([]byte(mustJSON(t, value)), &cats); err != nil {
		t.Fatalf("decode categories: %v", err)
	}
	if cats == nil {
		return []contract.Category{}
	}
	return cats
}

func decodeBudget(t *testing.T, value any) contract.Budget {
	t.Helper()
	var budget contract.Budget
	if err := json.Unmarshal([]byte(mustJSON(t, value)), &budget); err != nil {
		t.Fatalf("decode budget: %v", err)
	}
	return budget
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return string(raw)
}

func invalidNameEnvelope(reason string) contract.ErrorEnvelope {
	return contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"One or more input fields are invalid.",
		false,
		map[string]any{
			"fields": []map[string]string{
				{"field": "name", "reason": reason},
			},
		},
	))
}

func insertCurrentMonthBudget(t *testing.T, db *sql.DB, categoryID int64, month string, amountHundredths int64) {
	t.Helper()
	if _, err := db.ExecContext(
		context.Background(),
		"INSERT INTO budgets (category_id, month, amount_hundredths) VALUES (?, ?, ?)",
		categoryID,
		month,
		amountHundredths,
	); err != nil {
		t.Fatalf("insert current-month budget: %v", err)
	}
}

func toolText(result *mcp.CallToolResult) string {
	if result == nil || len(result.Content) == 0 {
		return ""
	}
	text, _ := result.Content[0].(*mcp.TextContent)
	if text == nil {
		return ""
	}
	return text.Text
}

func leakedInternalError(payload string) bool {
	lower := strings.ToLower(payload)
	for _, secret := range []string{"sql:", "database is closed", "goroutine", "stack", "/var/", "finance.db"} {
		if strings.Contains(lower, secret) {
			return true
		}
	}
	return false
}
