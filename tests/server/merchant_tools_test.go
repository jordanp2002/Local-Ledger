package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestKnownMerchantToolDiscovery(t *testing.T) {
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
	setSchema := schemaObject(t, byName["set_known_merchant"].InputSchema)
	if setSchema["type"] != "object" {
		t.Fatalf("set_known_merchant input schema type = %v, want object", setSchema["type"])
	}
	required, _ := setSchema["required"].([]any)
	if !containsValue(required, "merchant") || !containsValue(required, "category") {
		t.Fatalf("set_known_merchant required = %v, want merchant and category", required)
	}
	properties, _ := setSchema["properties"].(map[string]any)
	for _, field := range []string{"merchant", "category"} {
		property, _ := properties[field].(map[string]any)
		if property["type"] != "string" {
			t.Fatalf("set_known_merchant %s type = %v, want string", field, property["type"])
		}
	}

	listSchema := schemaObject(t, byName["list_known_merchants"].InputSchema)
	if listSchema["type"] != "object" {
		t.Fatalf("list_known_merchants input schema type = %v, want object", listSchema["type"])
	}
	if required, _ := listSchema["required"].([]any); len(required) != 0 {
		t.Fatalf("list_known_merchants required = %v, want no required fields", required)
	}
}

func TestSetKnownMerchantLifecycle(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)
	createCategoryForMerchantTest(t, session, "Groceries")
	createCategoryForMerchantTest(t, session, "Dining")

	created := callTool(t, session, "set_known_merchant", map[string]any{
		"merchant": "  METRO  ",
		"category": " Groceries ",
	})
	if created.IsError {
		t.Fatalf("set_known_merchant create failed: %s", structuredJSON(t, created))
	}
	createdPayload := structuredObject(t, created)
	if keys := objectKeys(createdPayload); strings.Join(keys, ",") != "created,known_merchant,ok,previous_category" {
		t.Fatalf("set_known_merchant keys = %v", keys)
	}
	if createdPayload["ok"] != true || createdPayload["created"] != true || createdPayload["previous_category"] != nil {
		t.Fatalf("create payload = %s, want created true and null previous_category", structuredJSON(t, created))
	}
	first := decodeKnownMerchant(t, createdPayload["known_merchant"])
	if first.Merchant != "METRO" || first.Category != "Groceries" || !first.CategoryActive {
		t.Fatalf("created known merchant = %#v, want METRO/Groceries/active", first)
	}

	same := callTool(t, session, "set_known_merchant", map[string]any{
		"merchant": "metro",
		"category": "groceries",
	})
	if same.IsError {
		t.Fatalf("same-category set failed: %s", structuredJSON(t, same))
	}
	samePayload := structuredObject(t, same)
	second := decodeKnownMerchant(t, samePayload["known_merchant"])
	if samePayload["created"] != false || samePayload["previous_category"] != nil {
		t.Fatalf("same-category payload = %s, want created false and null previous_category", structuredJSON(t, same))
	}
	if second != first {
		t.Fatalf("same-category row = %#v, want unchanged %#v", second, first)
	}

	replaced := callTool(t, session, "set_known_merchant", map[string]any{
		"merchant": " metro ",
		"category": "Dining",
	})
	if replaced.IsError {
		t.Fatalf("replacement set failed: %s", structuredJSON(t, replaced))
	}
	replacedPayload := structuredObject(t, replaced)
	third := decodeKnownMerchant(t, replacedPayload["known_merchant"])
	if replacedPayload["created"] != false || replacedPayload["previous_category"] != "Groceries" {
		t.Fatalf("replacement payload = %s, want created false and previous Groceries", structuredJSON(t, replaced))
	}
	if third.ID != first.ID || third.Merchant != first.Merchant || third.CreatedAt != first.CreatedAt || third.Category != "Dining" || !third.CategoryActive {
		t.Fatalf("replacement row = %#v, want preserved identity and Dining", third)
	}
}

func TestSetKnownMerchantRecoveryErrors(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)
	groceries := createCategoryForMerchantTest(t, session, "Groceries")
	health := createCategoryForMerchantTest(t, session, "Health")

	missing := callTool(t, session, "set_known_merchant", map[string]any{
		"merchant": "Metro",
		"category": "  Pharmacy  ",
	})
	if !missing.IsError {
		t.Fatal("missing category IsError = false, want true")
	}
	requireStructuredEqual(t, missing, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeCategoryNotFound,
		"Category 'Pharmacy' does not exist.",
		false,
		map[string]any{
			"requested_category": "Pharmacy",
			"categories":         []contract.Category{groceries, health},
		},
	)))

	initial := callTool(t, session, "set_known_merchant", map[string]any{
		"merchant": "Metro",
		"category": "Health",
	})
	if initial.IsError {
		t.Fatalf("initial inactive-test mapping failed: %s", structuredJSON(t, initial))
	}
	disabled := callTool(t, session, "disable_category", map[string]any{"name": "Health"})
	if disabled.IsError {
		t.Fatalf("disable Health failed: %s", structuredJSON(t, disabled))
	}
	inactive := decodeCategory(t, structuredObject(t, disabled)["category"])

	inactiveResult := callTool(t, session, "set_known_merchant", map[string]any{
		"merchant": "metro",
		"category": "health",
	})
	if !inactiveResult.IsError {
		t.Fatal("inactive category IsError = false, want true")
	}
	requireStructuredEqual(t, inactiveResult, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeCategoryInactive,
		"Category 'Health' is inactive.",
		false,
		map[string]any{
			"category":          inactive,
			"active_categories": []contract.Category{groceries},
		},
	)))

	replaced := callTool(t, session, "set_known_merchant", map[string]any{
		"merchant": "Metro",
		"category": "Groceries",
	})
	if replaced.IsError {
		t.Fatalf("replace inactive mapping failed: %s", structuredJSON(t, replaced))
	}
	if got := structuredObject(t, replaced)["previous_category"]; got != "Health" {
		t.Fatalf("previous_category = %v, want Health", got)
	}
}

func TestSetKnownMerchantValidationCollectsFields(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)

	result := callTool(t, session, "set_known_merchant", map[string]any{
		"merchant": " \t",
		"category": "\x00",
	})
	if !result.IsError {
		t.Fatal("invalid set IsError = false, want true")
	}
	requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{
			"fields": []contract.FieldIssue{
				{Field: "merchant", Reason: "must not be empty"},
				{Field: "category", Reason: "must not contain NUL characters"},
			},
		},
	)))
}

func TestKnownMerchantSchemaErrors(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{name: "set_known_merchant", args: map[string]any{"merchant": "Metro"}},
		{name: "set_known_merchant", args: map[string]any{"merchant": 12, "category": "Groceries"}},
		{name: "list_known_merchants", args: map[string]any{"limit": "50"}},
	} {
		result := callTool(t, session, tc.name, tc.args)
		if !result.IsError {
			t.Fatalf("%s schema-invalid args %#v IsError = false, want true", tc.name, tc.args)
		}
	}
}

func TestListKnownMerchantsPaginationAndLiteralSearch(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)
	createCategoryForMerchantTest(t, session, "Groceries")
	for _, merchantName := range []string{"100% Off", "Metro", "Shop_Mart"} {
		result := callTool(t, session, "set_known_merchant", map[string]any{
			"merchant": merchantName,
			"category": "Groceries",
		})
		if result.IsError {
			t.Fatalf("set %q failed: %s", merchantName, structuredJSON(t, result))
		}
	}

	empty := callTool(t, session, "list_known_merchants", map[string]any{})
	if empty.IsError {
		t.Fatalf("default list failed: %s", structuredJSON(t, empty))
	}
	emptyPayload := structuredObject(t, empty)
	if keys := objectKeys(emptyPayload); strings.Join(keys, ",") != "known_merchants,ok,page" {
		t.Fatalf("list_known_merchants keys = %v", keys)
	}
	if got := emptyPayload["known_merchants"]; got == nil {
		t.Fatal("known_merchants = null, want array")
	}
	emptyPage := objectField(t, emptyPayload, "page")
	if emptyPage["limit"] != float64(50) || emptyPage["offset"] != float64(0) {
		t.Fatalf("default page = %#v, want limit 50 offset 0", emptyPage)
	}

	one := int64(1)
	firstPage := callTool(t, session, "list_known_merchants", map[string]any{"limit": one})
	if firstPage.IsError {
		t.Fatalf("first page failed: %s", structuredJSON(t, firstPage))
	}
	firstPayload := structuredObject(t, firstPage)
	firstRows := firstPayload["known_merchants"].([]any)
	firstPageObject := objectField(t, firstPayload, "page")
	if len(firstRows) != 1 || firstPageObject["total"] != float64(3) || firstPageObject["returned"] != float64(1) || firstPageObject["has_more"] != true {
		t.Fatalf("first page = %s, want one of three with has_more", structuredJSON(t, firstPage))
	}

	two := int64(2)
	offset := int64(1)
	lastPage := callTool(t, session, "list_known_merchants", map[string]any{"limit": two, "offset": offset})
	if lastPage.IsError {
		t.Fatalf("last page failed: %s", structuredJSON(t, lastPage))
	}
	lastPageObject := objectField(t, structuredObject(t, lastPage), "page")
	if lastPageObject["total"] != float64(3) || lastPageObject["returned"] != float64(2) || lastPageObject["has_more"] != false {
		t.Fatalf("last page = %s, want two rows and no has_more", structuredJSON(t, lastPage))
	}

	for _, tc := range []struct {
		query string
		want  int
	}{
		{query: "%", want: 1},
		{query: "_", want: 1},
		{query: "groc", want: 0},
		{query: " metro ", want: 1},
	} {
		result := callTool(t, session, "list_known_merchants", map[string]any{"query": tc.query})
		if result.IsError {
			t.Fatalf("query %q failed: %s", tc.query, structuredJSON(t, result))
		}
		rows, ok := structuredObject(t, result)["known_merchants"].([]any)
		if !ok || len(rows) != tc.want {
			t.Fatalf("query %q rows = %#v, want %d", tc.query, rows, tc.want)
		}
	}

	beyond := callTool(t, session, "list_known_merchants", map[string]any{"query": "metro", "offset": int64(1)})
	if beyond.IsError {
		t.Fatalf("beyond-end page failed: %s", structuredJSON(t, beyond))
	}
	beyondPage := objectField(t, structuredObject(t, beyond), "page")
	if beyondPage["total"] != float64(1) || beyondPage["returned"] != float64(0) || beyondPage["has_more"] != false {
		t.Fatalf("beyond-end page = %s, want total one and zero returned", structuredJSON(t, beyond))
	}
}

func TestListKnownMerchantsEmptyAndInactiveVisibility(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)
	empty := callTool(t, session, "list_known_merchants", map[string]any{})
	if empty.IsError {
		t.Fatalf("empty list failed: %s", structuredJSON(t, empty))
	}
	payload := structuredObject(t, empty)
	rows, ok := payload["known_merchants"].([]any)
	if !ok || rows == nil || len(rows) != 0 {
		t.Fatalf("empty known_merchants = %#v, want []", payload["known_merchants"])
	}
	page := objectField(t, payload, "page")
	if page["returned"] != float64(0) || page["total"] != float64(0) || page["has_more"] != false {
		t.Fatalf("empty page = %#v, want zero counts", page)
	}

	createCategoryForMerchantTest(t, session, "Groceries")
	set := callTool(t, session, "set_known_merchant", map[string]any{"merchant": "Metro", "category": "Groceries"})
	if set.IsError {
		t.Fatalf("set Metro failed: %s", structuredJSON(t, set))
	}
	original := decodeKnownMerchant(t, structuredObject(t, set)["known_merchant"])
	disable := callTool(t, session, "disable_category", map[string]any{"name": "Groceries"})
	if disable.IsError {
		t.Fatalf("disable Groceries failed: %s", structuredJSON(t, disable))
	}
	listed := callTool(t, session, "list_known_merchants", map[string]any{})
	if listed.IsError {
		t.Fatalf("list inactive mapping failed: %s", structuredJSON(t, listed))
	}
	rows, ok = structuredObject(t, listed)["known_merchants"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("inactive mapping rows = %#v, want one row", structuredObject(t, listed)["known_merchants"])
	}
	known := decodeKnownMerchant(t, rows[0])
	if known.Merchant != "Metro" || known.Category != "Groceries" || known.CategoryActive {
		t.Fatalf("inactive mapping = %#v, want category_active false", known)
	}

	reactivate := callTool(t, session, "create_category", map[string]any{"name": "groceries"})
	if reactivate.IsError {
		t.Fatalf("reactivate Groceries failed: %s", structuredJSON(t, reactivate))
	}
	relisted := callTool(t, session, "list_known_merchants", map[string]any{})
	if relisted.IsError {
		t.Fatalf("list reactivated mapping failed: %s", structuredJSON(t, relisted))
	}
	relistedRows, ok := structuredObject(t, relisted)["known_merchants"].([]any)
	if !ok || len(relistedRows) != 1 {
		t.Fatalf("reactivated mapping rows = %#v, want one row", structuredObject(t, relisted)["known_merchants"])
	}
	reactivated := decodeKnownMerchant(t, relistedRows[0])
	if !reactivated.CategoryActive {
		t.Fatalf("reactivated mapping = %#v, want category_active true", reactivated)
	}
	if reactivated.ID != original.ID || reactivated.Merchant != original.Merchant || reactivated.CreatedAt != original.CreatedAt || reactivated.UpdatedAt != original.UpdatedAt {
		t.Fatalf("reactivation rewrote merchant row: before=%#v after=%#v", original, reactivated)
	}
}

func TestListKnownMerchantsValidationCollectsFields(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)
	result := callTool(t, session, "list_known_merchants", map[string]any{
		"query":  "\x00",
		"limit":  int64(0),
		"offset": int64(-1),
	})
	if !result.IsError {
		t.Fatal("invalid list IsError = false, want true")
	}
	requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{
			"fields": []contract.FieldIssue{
				{Field: "query", Reason: "must not contain NUL characters"},
				{Field: "limit", Reason: "must be between 1 and 200"},
				{Field: "offset", Reason: "must be zero or greater"},
			},
		},
	)))
}

func TestKnownMerchantToolInternalError(t *testing.T) {
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
		{name: "set_known_merchant", args: map[string]any{"merchant": "Metro", "category": "Groceries"}},
		{name: "list_known_merchants", args: map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs.Reset()
			result := callTool(t, session, tc.name, tc.args)
			if !result.IsError {
				t.Fatal("IsError = false, want true")
			}
			requireStructuredEqual(t, result, contract.NewInternalErrorEnvelope())
			if public := structuredJSON(t, result); leakedInternalError(public) {
				t.Fatalf("public payload leaked internal details: %s", public)
			}
			if text := toolText(result); leakedInternalError(text) {
				t.Fatalf("tool text leaked internal details: %s", text)
			}
			if logs.Len() == 0 {
				t.Fatal("logger did not record private cause")
			}
		})
	}
}

func createCategoryForMerchantTest(t *testing.T, session *mcp.ClientSession, name string) contract.Category {
	t.Helper()
	result := callTool(t, session, "create_category", map[string]any{"name": name})
	if result.IsError {
		t.Fatalf("create category %q failed: %s", name, structuredJSON(t, result))
	}
	return decodeCategory(t, structuredObject(t, result)["category"])
}

func decodeKnownMerchant(t *testing.T, value any) contract.KnownMerchant {
	t.Helper()
	var known contract.KnownMerchant
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal known merchant: %v", err)
	}
	if err := json.Unmarshal(raw, &known); err != nil {
		t.Fatalf("decode known merchant: %v", err)
	}
	return known
}
