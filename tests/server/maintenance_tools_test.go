package server_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMaintenanceToolSchemas(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), time.Now, nil)
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	byName := make(map[string]*mcp.Tool, len(result.Tools))
	for _, tool := range result.Tools {
		byName[tool.Name] = tool
	}

	for _, test := range []struct {
		name     string
		required []string
		fields   []string
	}{
		{name: "rename_category", required: []string{"category", "new_name"}, fields: []string{"category", "new_name"}},
		{name: "rename_known_merchant", required: []string{"merchant", "new_merchant"}, fields: []string{"merchant", "new_merchant"}},
		{name: "remove_known_merchant", required: []string{"merchant"}, fields: []string{"merchant"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tool := byName[test.name]
			if tool == nil {
				t.Fatal("tool is not discoverable")
			}
			schema := schemaObject(t, tool.InputSchema)
			if schema["type"] != "object" {
				t.Fatalf("schema type = %v, want object", schema["type"])
			}
			required, _ := schema["required"].([]any)
			for _, field := range test.required {
				if !containsValue(required, field) {
					t.Fatalf("required = %v, missing %q", required, field)
				}
			}
			properties, _ := schema["properties"].(map[string]any)
			if len(properties) != len(test.fields) {
				t.Fatalf("properties = %v, want %d fields", properties, len(test.fields))
			}
			for _, field := range test.fields {
				property, _ := properties[field].(map[string]any)
				if property["type"] != "string" {
					t.Fatalf("%s schema type = %v, want string", field, property["type"])
				}
			}
		})
	}
}

func TestRenameCategoryToolLifecycleAndErrors(t *testing.T) {
	db := openCategoryDB(t)
	session := connectCategorySession(t, db, time.Now, nil)
	diningResult := callTool(t, session, "create_category", map[string]any{"name": "Dining"})
	if diningResult.IsError {
		t.Fatalf("create Dining: %s", structuredJSON(t, diningResult))
	}
	dining := decodeCategory(t, structuredObject(t, diningResult)["category"])
	groceriesResult := callTool(t, session, "create_category", map[string]any{"name": "Groceries"})
	if groceriesResult.IsError {
		t.Fatalf("create Groceries: %s", structuredJSON(t, groceriesResult))
	}
	groceries := decodeCategory(t, structuredObject(t, groceriesResult)["category"])

	set := callTool(t, session, "set_known_merchant", map[string]any{"merchant": "Restaurant", "category": "Dining"})
	if set.IsError {
		t.Fatalf("set Restaurant: %s", structuredJSON(t, set))
	}
	added := callTool(t, session, "add_transaction", map[string]any{
		"amount":   "23.50",
		"merchant": "Restaurant",
		"category": "Dining",
		"date":     "2026-08-14",
	})
	if added.IsError {
		t.Fatalf("add Restaurant: %s", structuredJSON(t, added))
	}

	rename := callTool(t, session, "rename_category", map[string]any{"category": " dining ", "new_name": " Eating Out "})
	if rename.IsError {
		t.Fatalf("rename Dining: %s", structuredJSON(t, rename))
	}
	payload := structuredObject(t, rename)
	if keys := objectKeys(payload); strings.Join(keys, ",") != "category,changed,ok,previous_name" {
		t.Fatalf("rename payload keys = %v", keys)
	}
	renamed := decodeCategory(t, payload["category"])
	if !payload["ok"].(bool) || payload["previous_name"] != "Dining" || payload["changed"] != true || renamed.ID != dining.ID || renamed.Name != "Eating Out" || renamed.CreatedAt != dining.CreatedAt {
		t.Fatalf("rename payload = %s", structuredJSON(t, rename))
	}

	listedTransactions := callTool(t, session, "list_transactions", map[string]any{
		"start_date": "2026-08-01",
		"end_date":   "2026-08-31",
		"category":   "Eating Out",
	})
	if listedTransactions.IsError {
		t.Fatalf("list renamed transactions: %s", structuredJSON(t, listedTransactions))
	}
	transactions := structuredObject(t, listedTransactions)["transactions"].([]any)
	if len(transactions) != 1 || asObject(t, transactions[0])["merchant"] != "Restaurant" || asObject(t, transactions[0])["category"] != "Eating Out" {
		t.Fatalf("renamed category transaction = %#v", transactions)
	}

	known := callTool(t, session, "list_known_merchants", map[string]any{"query": "restaurant"})
	if known.IsError {
		t.Fatalf("list renamed merchant mapping: %s", structuredJSON(t, known))
	}
	knownRows := structuredObject(t, known)["known_merchants"].([]any)
	if len(knownRows) != 1 || asObject(t, knownRows[0])["category"] != "Eating Out" {
		t.Fatalf("renamed mapping = %#v", knownRows)
	}

	const frozen = "2020-01-01T00:00:00.000Z"
	if _, err := db.ExecContext(context.Background(), `UPDATE categories SET updated_at = ? WHERE id = ?`, frozen, renamed.ID); err != nil {
		t.Fatalf("freeze category timestamp: %v", err)
	}
	noOp := callTool(t, session, "rename_category", map[string]any{"category": "Eating Out", "new_name": "Eating Out"})
	if noOp.IsError {
		t.Fatalf("rename no-op: %s", structuredJSON(t, noOp))
	}
	noOpCategory := decodeCategory(t, structuredObject(t, noOp)["category"])
	if structuredObject(t, noOp)["changed"] != false || noOpCategory.UpdatedAt != frozen {
		t.Fatalf("no-op rename = %s, want unchanged timestamp", structuredJSON(t, noOp))
	}

	disabled := callTool(t, session, "disable_category", map[string]any{"name": "Eating Out"})
	if disabled.IsError {
		t.Fatalf("disable Eating Out: %s", structuredJSON(t, disabled))
	}
	inactiveRename := callTool(t, session, "rename_category", map[string]any{"category": "Eating Out", "new_name": "Restaurants"})
	if inactiveRename.IsError {
		t.Fatalf("rename inactive category: %s", structuredJSON(t, inactiveRename))
	}
	inactive := decodeCategory(t, structuredObject(t, inactiveRename)["category"])
	if inactive.ID != dining.ID || inactive.Name != "Restaurants" || inactive.Active {
		t.Fatalf("inactive rename = %#v", inactive)
	}

	collision := callTool(t, session, "rename_category", map[string]any{"category": "Restaurants", "new_name": " groceries "})
	if !collision.IsError {
		t.Fatal("category collision IsError = false, want true")
	}
	requireStructuredEqual(t, collision, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeCategoryAlreadyExists,
		"Category 'Groceries' already exists.",
		false,
		map[string]any{"category": groceries},
	)))

	missing := callTool(t, session, "rename_category", map[string]any{"category": "Missing", "new_name": "New"})
	if !missing.IsError {
		t.Fatal("missing category IsError = false, want true")
	}
	gotMissing := structuredObject(t, missing)
	if gotMissing["error"].(map[string]any)["code"] != string(contract.ErrorCodeCategoryNotFound) {
		t.Fatalf("missing category error = %s", structuredJSON(t, missing))
	}

	invalid := callTool(t, session, "rename_category", map[string]any{"category": " ", "new_name": "\x00"})
	if !invalid.IsError {
		t.Fatal("invalid category rename IsError = false, want true")
	}
	if fields := asObject(t, structuredObject(t, invalid)["error"])["details"].(map[string]any)["fields"].([]any); len(fields) != 2 || asObject(t, fields[0])["field"] != "category" || asObject(t, fields[1])["field"] != "new_name" {
		t.Fatalf("invalid category fields = %#v", fields)
	}
}

func TestKnownMerchantMaintenanceToolLifecycleAndErrors(t *testing.T) {
	db := openCategoryDB(t)
	session := connectCategorySession(t, db, time.Now, nil)
	createdCategory := callTool(t, session, "create_category", map[string]any{"name": "Groceries"})
	if createdCategory.IsError {
		t.Fatalf("create Groceries: %s", structuredJSON(t, createdCategory))
	}
	groceries := decodeCategory(t, structuredObject(t, createdCategory)["category"])

	created := callTool(t, session, "set_known_merchant", map[string]any{"merchant": "Metro", "category": "Groceries"})
	if created.IsError {
		t.Fatalf("set Metro: %s", structuredJSON(t, created))
	}
	original := decodeKnownMerchant(t, structuredObject(t, created)["known_merchant"])
	added := callTool(t, session, "add_transaction", map[string]any{
		"amount":   "12.50",
		"merchant": "Metro",
		"category": "Groceries",
		"date":     "2026-08-14",
	})
	if added.IsError {
		t.Fatalf("add Metro: %s", structuredJSON(t, added))
	}

	rename := callTool(t, session, "rename_known_merchant", map[string]any{"merchant": " metro ", "new_merchant": " Metro Grocery "})
	if rename.IsError {
		t.Fatalf("rename Metro: %s", structuredJSON(t, rename))
	}
	renamed := decodeKnownMerchant(t, structuredObject(t, rename)["known_merchant"])
	if renamed.ID != original.ID || renamed.Merchant != "Metro Grocery" || renamed.CategoryID != groceries.ID || renamed.CreatedAt != original.CreatedAt || structuredObject(t, rename)["previous_merchant"] != "Metro" || structuredObject(t, rename)["changed"] != true {
		t.Fatalf("rename payload = %s", structuredJSON(t, rename))
	}

	transactions := callTool(t, session, "list_transactions", map[string]any{"start_date": "2026-08-01", "end_date": "2026-08-31"})
	if transactions.IsError {
		t.Fatalf("list transactions: %s", structuredJSON(t, transactions))
	}
	transactionRows := structuredObject(t, transactions)["transactions"].([]any)
	if len(transactionRows) != 1 || asObject(t, transactionRows[0])["merchant"] != "Metro" {
		t.Fatalf("historical merchant rows = %#v", transactionRows)
	}

	collisionMapping := callTool(t, session, "set_known_merchant", map[string]any{"merchant": "Shoppers", "category": "Groceries"})
	if collisionMapping.IsError {
		t.Fatalf("set Shoppers: %s", structuredJSON(t, collisionMapping))
	}
	conflict := decodeKnownMerchant(t, structuredObject(t, collisionMapping)["known_merchant"])
	collision := callTool(t, session, "rename_known_merchant", map[string]any{"merchant": "Metro Grocery", "new_merchant": " shoppers "})
	if !collision.IsError {
		t.Fatal("merchant collision IsError = false, want true")
	}
	requireStructuredEqual(t, collision, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeKnownMerchantAlreadyExists,
		"Known merchant 'Shoppers' already exists.",
		false,
		map[string]any{"known_merchant": conflict},
	)))

	removed := callTool(t, session, "remove_known_merchant", map[string]any{"merchant": " metro grocery "})
	if removed.IsError {
		t.Fatalf("remove Metro Grocery: %s", structuredJSON(t, removed))
	}
	removedMerchant := decodeKnownMerchant(t, structuredObject(t, removed)["removed_known_merchant"])
	if removedMerchant != renamed {
		t.Fatalf("removed merchant = %#v, want %#v", removedMerchant, renamed)
	}

	repeated := callTool(t, session, "remove_known_merchant", map[string]any{"merchant": "METRO GROCERY"})
	if !repeated.IsError {
		t.Fatal("repeated merchant removal IsError = false, want true")
	}
	requireStructuredEqual(t, repeated, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeKnownMerchantNotFound,
		"Known merchant 'METRO GROCERY' does not exist.",
		false,
		map[string]any{"requested_merchant": "METRO GROCERY"},
	)))

	invalid := callTool(t, session, "rename_known_merchant", map[string]any{"merchant": " ", "new_merchant": "\x00"})
	if !invalid.IsError {
		t.Fatal("invalid merchant rename IsError = false, want true")
	}
	fields := asObject(t, structuredObject(t, invalid)["error"])["details"].(map[string]any)["fields"].([]any)
	if len(fields) != 2 || asObject(t, fields[0])["field"] != "merchant" || asObject(t, fields[1])["field"] != "new_merchant" {
		t.Fatalf("invalid merchant fields = %#v", fields)
	}

	missingRename := callTool(t, session, "rename_known_merchant", map[string]any{"merchant": "Missing", "new_merchant": "New"})
	if !missingRename.IsError {
		t.Fatal("missing merchant rename IsError = false, want true")
	}
	requireStructuredEqual(t, missingRename, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeKnownMerchantNotFound,
		"Known merchant 'Missing' does not exist.",
		false,
		map[string]any{"requested_merchant": "Missing"},
	)))
}
