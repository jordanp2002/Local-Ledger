package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAddTransactionToolDiscovery(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if got := listedToolNames(result.Tools); strings.Join(got, ",") != strings.Join(categoryToolNames, ",") {
		t.Fatalf("tools = %v, want %v", got, categoryToolNames)
	}
	if len(result.Tools) != 10 {
		t.Fatalf("tool count = %d, want 10", len(result.Tools))
	}

	var tool *mcp.Tool
	for _, candidate := range result.Tools {
		if candidate.Name == "add_transaction" {
			tool = candidate
			break
		}
	}
	if tool == nil {
		t.Fatal("add_transaction is not discoverable")
	}
	schema := schemaObject(t, tool.InputSchema)
	if schema["type"] != "object" {
		t.Fatalf("input schema type = %v, want object", schema["type"])
	}
	required, _ := schema["required"].([]any)
	if !containsValue(required, "amount") || !containsValue(required, "merchant") {
		t.Fatalf("required = %v, want amount and merchant", required)
	}
	if containsValue(required, "category") || containsValue(required, "date") || containsValue(required, "note") {
		t.Fatalf("required = %v, want category, date, and note optional", required)
	}
	properties, _ := schema["properties"].(map[string]any)
	for _, field := range []string{"amount", "merchant", "category", "date", "note"} {
		property, _ := properties[field].(map[string]any)
		if property == nil || !schemaTypeContains(property["type"], "string") {
			t.Fatalf("%s schema = %#v, want string", field, properties[field])
		}
	}
}

func TestAddTransactionMappingActions(t *testing.T) {
	t.Run("created", func(t *testing.T) {
		db := openCategoryDB(t)
		session := connectCategorySession(t, db, fixedTransactionNow, nil)
		groceries := createCategoryForMerchantTest(t, session, "Groceries")

		result := callTool(t, session, "add_transaction", map[string]any{
			"amount":   "20.5",
			"merchant": "  Metro  ",
			"category": " groceries ",
		})
		if result.IsError {
			t.Fatalf("add_transaction created failed: %s", structuredJSON(t, result))
		}
		got := structuredObject(t, result)
		if keys := objectKeys(got); strings.Join(keys, ",") != "category_source,merchant_mapping_action,ok,transaction" {
			t.Fatalf("add_transaction keys = %v", keys)
		}
		if got["ok"] != true || got["category_source"] != "provided" || got["merchant_mapping_action"] != "created" {
			t.Fatalf("created metadata = %s", structuredJSON(t, result))
		}
		txn := decodeTransaction(t, got["transaction"])
		if keys := objectKeys(objectField(t, got, "transaction")); strings.Join(keys, ",") != "amount,category,category_id,created_at,date,id,merchant,note,updated_at" {
			t.Fatalf("transaction keys = %v", objectKeys(objectField(t, got, "transaction")))
		}
		if txn.ID == 0 || txn.Merchant != "Metro" || txn.Amount != "20.50" {
			t.Fatalf("created transaction = %#v, want Metro/20.50", txn)
		}
		if txn.CategoryID != groceries.ID || txn.Category != "Groceries" {
			t.Fatalf("created category = %#v, want stored Groceries", txn)
		}
		if txn.Date != "2026-08-15" {
			t.Fatalf("omitted date = %q, want 2026-08-15", txn.Date)
		}
		if objectField(t, got, "transaction")["note"] != nil || txn.Note != nil {
			t.Fatalf("omitted note = %#v, want null", txn.Note)
		}
		if txn.CreatedAt == "" || txn.UpdatedAt == "" {
			t.Fatalf("created timestamps missing: %#v", txn)
		}

		var storedMerchant, storedDate, storedCategory string
		var storedAmount int64
		if err := db.QueryRowContext(context.Background(), `
			SELECT t.merchant, t.amount_hundredths, t.date, c.name
			FROM transactions AS t
			INNER JOIN categories AS c ON c.id = t.category_id
			WHERE t.id = ?
		`, txn.ID).Scan(&storedMerchant, &storedAmount, &storedDate, &storedCategory); err != nil {
			t.Fatalf("select stored created transaction: %v", err)
		}
		if storedMerchant != "Metro" || storedAmount != 2050 || storedDate != "2026-08-15" || storedCategory != "Groceries" {
			t.Fatalf("stored created transaction = (%q, %d, %q, %q)", storedMerchant, storedAmount, storedDate, storedCategory)
		}
	})

	t.Run("matched known merchant", func(t *testing.T) {
		session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
		groceries := createCategoryForMerchantTest(t, session, "Groceries")
		if result := callTool(t, session, "set_known_merchant", map[string]any{
			"merchant": "Metro",
			"category": "Groceries",
		}); result.IsError {
			t.Fatalf("set_known_merchant: %s", structuredJSON(t, result))
		}

		result := callTool(t, session, "add_transaction", map[string]any{
			"amount":   "20.00",
			"merchant": "metro",
		})
		if result.IsError {
			t.Fatalf("add_transaction matched known failed: %s", structuredJSON(t, result))
		}
		got := structuredObject(t, result)
		if got["ok"] != true || got["category_source"] != "known_merchant" || got["merchant_mapping_action"] != "matched" {
			t.Fatalf("matched known metadata = %s", structuredJSON(t, result))
		}
		txn := decodeTransaction(t, got["transaction"])
		if txn.Merchant != "metro" || txn.CategoryID != groceries.ID || txn.Category != "Groceries" {
			t.Fatalf("matched known transaction = %#v", txn)
		}
		if objectField(t, got, "transaction")["note"] != nil {
			t.Fatalf("omitted note = %#v, want null", got["transaction"])
		}
	})

	t.Run("matched provided", func(t *testing.T) {
		session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
		createCategoryForMerchantTest(t, session, "Groceries")
		if result := callTool(t, session, "set_known_merchant", map[string]any{
			"merchant": "Metro",
			"category": "Groceries",
		}); result.IsError {
			t.Fatalf("set_known_merchant: %s", structuredJSON(t, result))
		}

		result := callTool(t, session, "add_transaction", map[string]any{
			"amount":   "20.00",
			"merchant": "METRO",
			"category": "groceries",
		})
		if result.IsError {
			t.Fatalf("add_transaction matched provided failed: %s", structuredJSON(t, result))
		}
		got := structuredObject(t, result)
		if got["ok"] != true || got["category_source"] != "provided" || got["merchant_mapping_action"] != "matched" {
			t.Fatalf("matched provided metadata = %s", structuredJSON(t, result))
		}
		if decodeTransaction(t, got["transaction"]).Merchant != "METRO" {
			t.Fatalf("matched provided transaction = %s", structuredJSON(t, result))
		}
	})

	t.Run("preserved", func(t *testing.T) {
		session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
		createCategoryForMerchantTest(t, session, "Groceries")
		dining := createCategoryForMerchantTest(t, session, "Dining")
		if result := callTool(t, session, "set_known_merchant", map[string]any{
			"merchant": "Metro",
			"category": "Groceries",
		}); result.IsError {
			t.Fatalf("set_known_merchant: %s", structuredJSON(t, result))
		}

		result := callTool(t, session, "add_transaction", map[string]any{
			"amount":   "20.00",
			"merchant": "Metro",
			"category": "Dining",
			"date":     "2026-08-14",
		})
		if result.IsError {
			t.Fatalf("add_transaction preserved failed: %s", structuredJSON(t, result))
		}
		got := structuredObject(t, result)
		if got["ok"] != true || got["category_source"] != "provided" || got["merchant_mapping_action"] != "preserved" {
			t.Fatalf("preserved metadata = %s", structuredJSON(t, result))
		}
		txn := decodeTransaction(t, got["transaction"])
		if txn.CategoryID != dining.ID || txn.Category != "Dining" || txn.Date != "2026-08-14" {
			t.Fatalf("preserved transaction = %#v, want Dining on 2026-08-14", txn)
		}
	})

	t.Run("replaced_inactive", func(t *testing.T) {
		session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
		createCategoryForMerchantTest(t, session, "Health")
		groceries := createCategoryForMerchantTest(t, session, "Groceries")
		if result := callTool(t, session, "set_known_merchant", map[string]any{
			"merchant": "Shoppers",
			"category": "Health",
		}); result.IsError {
			t.Fatalf("set_known_merchant: %s", structuredJSON(t, result))
		}
		if result := callTool(t, session, "disable_category", map[string]any{"name": "Health"}); result.IsError {
			t.Fatalf("disable Health: %s", structuredJSON(t, result))
		}

		result := callTool(t, session, "add_transaction", map[string]any{
			"amount":   "20.00",
			"merchant": "SHOPPERS",
			"category": "Groceries",
		})
		if result.IsError {
			t.Fatalf("add_transaction replaced_inactive failed: %s", structuredJSON(t, result))
		}
		got := structuredObject(t, result)
		if got["ok"] != true || got["category_source"] != "provided" || got["merchant_mapping_action"] != "replaced_inactive" {
			t.Fatalf("replaced_inactive metadata = %s", structuredJSON(t, result))
		}
		txn := decodeTransaction(t, got["transaction"])
		if txn.Merchant != "SHOPPERS" || txn.CategoryID != groceries.ID || txn.Category != "Groceries" {
			t.Fatalf("replaced_inactive transaction = %#v", txn)
		}
	})
}

func TestAddTransactionSuppliedNoteIsTrimmed(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
	createCategoryForMerchantTest(t, session, "Groceries")

	result := callTool(t, session, "add_transaction", map[string]any{
		"amount":   "20.00",
		"merchant": "Metro",
		"category": "Groceries",
		"note":     " weekly groceries ",
	})
	if result.IsError {
		t.Fatalf("add_transaction note failed: %s", structuredJSON(t, result))
	}
	txn := objectField(t, structuredObject(t, result), "transaction")
	if txn["note"] != "weekly groceries" {
		t.Fatalf("supplied note = %#v, want weekly groceries", txn["note"])
	}
}

func TestAddTransactionInvalidInput(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)

	result := callTool(t, session, "add_transaction", map[string]any{
		"amount":   "-1",
		"merchant": " \t",
		"category": "   ",
		"date":     "2026-08-16",
		"note":     "bad\x00note",
	})
	if !result.IsError {
		t.Fatal("invalid add_transaction IsError = false, want true")
	}
	requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeInvalidInput,
		"",
		false,
		map[string]any{
			"fields": []contract.FieldIssue{
				{Field: "amount", Reason: "must be a positive amount with at most two decimal places"},
				{Field: "merchant", Reason: "must not be empty"},
				{Field: "category", Reason: "must not be empty"},
				{Field: "date", Reason: "must not be in the future"},
				{Field: "note", Reason: "must not contain NUL characters"},
			},
		},
	)))
}

func TestAddTransactionDomainErrors(t *testing.T) {
	t.Run("category_not_found", func(t *testing.T) {
		session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
		groceries := createCategoryForMerchantTest(t, session, "Groceries")

		result := callTool(t, session, "add_transaction", map[string]any{
			"amount":   "20.00",
			"merchant": "Metro",
			"category": "  Pharmacy  ",
		})
		if !result.IsError {
			t.Fatal("category_not_found IsError = false, want true")
		}
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeCategoryNotFound,
			"Category 'Pharmacy' does not exist.",
			false,
			map[string]any{
				"requested_category": "Pharmacy",
				"categories":         []contract.Category{groceries},
			},
		)))
	})

	t.Run("category_not_found empty list", func(t *testing.T) {
		session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
		result := callTool(t, session, "add_transaction", map[string]any{
			"amount":   "20.00",
			"merchant": "Metro",
			"category": "Pharmacy",
		})
		if !result.IsError {
			t.Fatal("empty-list category_not_found IsError = false, want true")
		}
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeCategoryNotFound,
			"Category 'Pharmacy' does not exist.",
			false,
			map[string]any{
				"requested_category": "Pharmacy",
				"categories":         []contract.Category{},
			},
		)))
	})

	t.Run("category_inactive", func(t *testing.T) {
		session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
		groceries := createCategoryForMerchantTest(t, session, "Groceries")
		createCategoryForMerchantTest(t, session, "Dining")
		disabled := callTool(t, session, "disable_category", map[string]any{"name": "Dining"})
		if disabled.IsError {
			t.Fatalf("disable Dining: %s", structuredJSON(t, disabled))
		}
		inactive := decodeCategory(t, structuredObject(t, disabled)["category"])

		result := callTool(t, session, "add_transaction", map[string]any{
			"amount":   "20.00",
			"merchant": "Metro",
			"category": "dining",
		})
		if !result.IsError {
			t.Fatal("category_inactive IsError = false, want true")
		}
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeCategoryInactive,
			"Category 'Dining' is inactive.",
			false,
			map[string]any{
				"category":          inactive,
				"active_categories": []contract.Category{groceries},
			},
		)))
	})

	t.Run("merchant_category_required", func(t *testing.T) {
		session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
		createCategoryForMerchantTest(t, session, "Groceries")

		result := callTool(t, session, "add_transaction", map[string]any{
			"amount":   "20.00",
			"merchant": "  Metro grocery store  ",
		})
		if !result.IsError {
			t.Fatal("merchant_category_required IsError = false, want true")
		}
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeMerchantCategoryRequired,
			"Merchant 'Metro grocery store' has no exact category mapping.",
			false,
			map[string]any{"merchant": "Metro grocery store"},
		)))
	})

	t.Run("merchant_category_inactive", func(t *testing.T) {
		session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
		alpha := createCategoryForMerchantTest(t, session, "Alpha")
		createCategoryForMerchantTest(t, session, "Health")
		if result := callTool(t, session, "set_known_merchant", map[string]any{
			"merchant": "Shoppers Drug Mart",
			"category": "Health",
		}); result.IsError {
			t.Fatalf("set_known_merchant: %s", structuredJSON(t, result))
		}
		if result := callTool(t, session, "disable_category", map[string]any{"name": "Health"}); result.IsError {
			t.Fatalf("disable Health: %s", structuredJSON(t, result))
		}
		listed := callTool(t, session, "list_known_merchants", map[string]any{})
		if listed.IsError {
			t.Fatalf("list_known_merchants: %s", structuredJSON(t, listed))
		}
		rows, ok := structuredObject(t, listed)["known_merchants"].([]any)
		if !ok || len(rows) != 1 {
			t.Fatalf("known merchants = %#v, want one inactive mapping", structuredObject(t, listed)["known_merchants"])
		}
		known := decodeKnownMerchant(t, rows[0])

		result := callTool(t, session, "add_transaction", map[string]any{
			"amount":   "20.00",
			"merchant": "shoppers drug mart",
		})
		if !result.IsError {
			t.Fatal("merchant_category_inactive IsError = false, want true")
		}
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeMerchantCategoryInactive,
			"Merchant 'Shoppers Drug Mart' maps to inactive category 'Health'.",
			false,
			map[string]any{
				"known_merchant":    known,
				"active_categories": []contract.Category{alpha},
			},
		)))
	})
}

func TestAddTransactionInternalError(t *testing.T) {
	db := openCategoryDB(t)
	var logs bytes.Buffer
	session := connectCategorySession(t, db, fixedTransactionNow, log.New(&logs, "", 0))
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	result := callTool(t, session, "add_transaction", map[string]any{
		"amount":   "20.00",
		"merchant": "Metro",
		"category": "Groceries",
	})
	if !result.IsError {
		t.Fatal("internal add_transaction IsError = false, want true")
	}
	requireStructuredEqual(t, result, contract.NewInternalErrorEnvelope())
	if leakedInternalError(structuredJSON(t, result)) || leakedInternalError(toolText(result)) {
		t.Fatalf("public payload leaked internal details: %s", structuredJSON(t, result))
	}
	if logs.Len() == 0 {
		t.Fatal("logger did not record the private cause")
	}
	if !strings.Contains(logs.String(), "sql:") && !strings.Contains(logs.String(), "database is closed") {
		t.Fatalf("logger = %q, want private database cause", logs.String())
	}
}

func TestUpdateRemoveTransactionToolDiscovery(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if got := listedToolNames(result.Tools); strings.Join(got, ",") != strings.Join(categoryToolNames, ",") {
		t.Fatalf("tools = %v, want %v", got, categoryToolNames)
	}
	if len(result.Tools) != 10 {
		t.Fatalf("tool count = %d, want 10", len(result.Tools))
	}

	updateTool := toolByName(t, result.Tools, "update_transaction")
	updateSchema := schemaObject(t, updateTool.InputSchema)
	if updateSchema["type"] != "object" {
		t.Fatalf("update_transaction input schema type = %v, want object", updateSchema["type"])
	}
	updateRequired, _ := updateSchema["required"].([]any)
	if !containsValue(updateRequired, "id") || len(updateRequired) != 1 {
		t.Fatalf("update_transaction required = %v, want only id", updateRequired)
	}
	updateProperties, _ := updateSchema["properties"].(map[string]any)
	idSchema, _ := updateProperties["id"].(map[string]any)
	if idSchema == nil || !schemaTypeContains(idSchema["type"], "integer") {
		t.Fatalf("update_transaction id schema = %#v, want integer", updateProperties["id"])
	}
	for _, field := range []string{"amount", "merchant", "category", "date", "note"} {
		if containsValue(updateRequired, field) {
			t.Fatalf("update_transaction required includes optional %s: %v", field, updateRequired)
		}
		property, _ := updateProperties[field].(map[string]any)
		if property == nil || !schemaTypeContains(property["type"], "string") {
			t.Fatalf("update_transaction %s schema = %#v, want string", field, updateProperties[field])
		}
	}
	noteSchema, _ := updateProperties["note"].(map[string]any)
	if !schemaTypeContains(noteSchema["type"], "null") {
		t.Fatalf("update_transaction note schema = %#v, want null accepted", updateProperties["note"])
	}

	removeTool := toolByName(t, result.Tools, "remove_transaction")
	removeSchema := schemaObject(t, removeTool.InputSchema)
	if removeSchema["type"] != "object" {
		t.Fatalf("remove_transaction input schema type = %v, want object", removeSchema["type"])
	}
	removeRequired, _ := removeSchema["required"].([]any)
	if !containsValue(removeRequired, "id") || len(removeRequired) != 1 {
		t.Fatalf("remove_transaction required = %v, want only id", removeRequired)
	}
	removeProperties, _ := removeSchema["properties"].(map[string]any)
	if _, ok := removeProperties["amount"]; ok {
		t.Fatalf("remove_transaction properties = %v, want only id", objectKeys(removeProperties))
	}
	removeID, _ := removeProperties["id"].(map[string]any)
	if removeID == nil || !schemaTypeContains(removeID["type"], "integer") {
		t.Fatalf("remove_transaction id schema = %#v, want integer", removeProperties["id"])
	}
}

func TestUpdateTransactionIndependentFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(*testing.T, *mcp.ClientSession)
		patch  map[string]any
		verify func(*testing.T, contract.Transaction, contract.Transaction)
	}{
		{
			name: "amount",
			patch: map[string]any{
				"amount": "23.50",
			},
			verify: func(t *testing.T, before, after contract.Transaction) {
				if after.Amount != "23.50" {
					t.Fatalf("amount = %q, want 23.50", after.Amount)
				}
				if after.Merchant != before.Merchant || after.Category != before.Category || after.Date != before.Date || derefNote(after.Note) != derefNote(before.Note) {
					t.Fatalf("amount patch changed other fields: before=%#v after=%#v", before, after)
				}
			},
		},
		{
			name: "merchant",
			patch: map[string]any{
				"merchant": "  Metro grocery store  ",
			},
			verify: func(t *testing.T, before, after contract.Transaction) {
				if after.Merchant != "Metro grocery store" {
					t.Fatalf("merchant = %q, want Metro grocery store", after.Merchant)
				}
				if after.CategoryID != before.CategoryID || after.Category != before.Category || after.Amount != before.Amount {
					t.Fatalf("merchant patch recategorized or changed amount: before=%#v after=%#v", before, after)
				}
			},
		},
		{
			name: "category",
			setup: func(t *testing.T, session *mcp.ClientSession) {
				createCategoryForMerchantTest(t, session, "Dining")
			},
			patch: map[string]any{
				"category": " dining ",
			},
			verify: func(t *testing.T, before, after contract.Transaction) {
				if after.Category != "Dining" || after.CategoryID == before.CategoryID {
					t.Fatalf("category = %#v, want Dining with a new category_id", after)
				}
				if after.Merchant != before.Merchant || after.Amount != before.Amount {
					t.Fatalf("category patch changed merchant or amount: before=%#v after=%#v", before, after)
				}
			},
		},
		{
			name: "date",
			patch: map[string]any{
				"date": "2026-08-13",
			},
			verify: func(t *testing.T, before, after contract.Transaction) {
				if after.Date != "2026-08-13" {
					t.Fatalf("date = %q, want 2026-08-13", after.Date)
				}
				if after.Amount != before.Amount || after.Merchant != before.Merchant {
					t.Fatalf("date patch changed amount or merchant: before=%#v after=%#v", before, after)
				}
			},
		},
		{
			name: "note",
			patch: map[string]any{
				"note": "  birthday cake  ",
			},
			verify: func(t *testing.T, _, after contract.Transaction) {
				if derefNote(after.Note) != "birthday cake" {
					t.Fatalf("note = %#v, want birthday cake", after.Note)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
			createCategoryForMerchantTest(t, session, "Groceries")
			if tc.setup != nil {
				tc.setup(t, session)
			}
			before := addTestTransaction(t, session, map[string]any{
				"amount":   "20.00",
				"merchant": "Metro",
				"category": "Groceries",
				"date":     "2026-08-14",
				"note":     "weekly groceries",
			})

			args := map[string]any{"id": before.ID}
			for key, value := range tc.patch {
				args[key] = value
			}
			result := callTool(t, session, "update_transaction", args)
			if result.IsError {
				t.Fatalf("update_transaction %s failed: %s", tc.name, structuredJSON(t, result))
			}
			got := structuredObject(t, result)
			if keys := objectKeys(got); strings.Join(keys, ",") != "ok,transaction" {
				t.Fatalf("update_transaction keys = %v, want [ok transaction]", keys)
			}
			if got["ok"] != true {
				t.Fatalf("ok = %v, want true", got["ok"])
			}
			after := decodeTransaction(t, got["transaction"])
			if after.ID != before.ID || after.CreatedAt != before.CreatedAt {
				t.Fatalf("identity changed: before=%#v after=%#v", before, after)
			}
			if tc.name != "date" && after.Date != before.Date {
				t.Fatalf("omitted date rewritten: before=%q after=%q", before.Date, after.Date)
			}
			tc.verify(t, before, after)
		})
	}
}

func TestUpdateTransactionNotePresence(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
	createCategoryForMerchantTest(t, session, "Groceries")
	seeded := addTestTransaction(t, session, map[string]any{
		"amount":   "20.00",
		"merchant": "Metro",
		"category": "Groceries",
		"note":     "weekly groceries",
	})

	omitted := callTool(t, session, "update_transaction", map[string]any{
		"id":     seeded.ID,
		"amount": "21.00",
	})
	if omitted.IsError {
		t.Fatalf("omitted-note update failed: %s", structuredJSON(t, omitted))
	}
	afterOmit := decodeTransaction(t, structuredObject(t, omitted)["transaction"])
	if afterOmit.Amount != "21.00" || derefNote(afterOmit.Note) != "weekly groceries" {
		t.Fatalf("omitted note changed stored note: %#v", afterOmit)
	}

	cleared := callTool(t, session, "update_transaction", map[string]any{
		"id":   seeded.ID,
		"note": nil,
	})
	if cleared.IsError {
		t.Fatalf("note:null update failed: %s", structuredJSON(t, cleared))
	}
	got := structuredObject(t, cleared)
	if objectField(t, got, "transaction")["note"] != nil {
		t.Fatalf("note:null response note = %#v, want null", objectField(t, got, "transaction")["note"])
	}
	if decodeTransaction(t, got["transaction"]).Note != nil {
		t.Fatalf("note:null decoded note = %#v, want nil", decodeTransaction(t, got["transaction"]).Note)
	}
}

func TestUpdateTransactionInvalidInput(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
	createCategoryForMerchantTest(t, session, "Groceries")
	seeded := addTestTransaction(t, session, map[string]any{
		"amount":   "20.00",
		"merchant": "Metro",
		"category": "Groceries",
	})

	t.Run("no mutable field", func(t *testing.T) {
		result := callTool(t, session, "update_transaction", map[string]any{"id": seeded.ID})
		if !result.IsError {
			t.Fatal("id-only update IsError = false, want true")
		}
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeInvalidInput,
			"",
			false,
			map[string]any{
				"fields": []contract.FieldIssue{
					{Field: "id", Reason: "at least one of amount, merchant, category, date, or note must be supplied"},
				},
			},
		)))
	})

	t.Run("amount null", func(t *testing.T) {
		result := callTool(t, session, "update_transaction", map[string]any{
			"id":     seeded.ID,
			"amount": nil,
		})
		if !result.IsError {
			t.Fatal("amount:null IsError = false, want true")
		}
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeInvalidInput,
			"",
			false,
			map[string]any{
				"fields": []contract.FieldIssue{
					{Field: "amount", Reason: "must not be null"},
				},
			},
		)))
	})

	t.Run("zero id and amount null", func(t *testing.T) {
		result := callTool(t, session, "update_transaction", map[string]any{
			"id":     int64(0),
			"amount": nil,
		})
		if !result.IsError {
			t.Fatal("id:0 amount:null IsError = false, want true")
		}
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeInvalidInput,
			"",
			false,
			map[string]any{
				"fields": []contract.FieldIssue{
					{Field: "id", Reason: "must be a positive integer"},
					{Field: "amount", Reason: "must not be null"},
				},
			},
		)))
	})

	t.Run("semantic fields", func(t *testing.T) {
		result := callTool(t, session, "update_transaction", map[string]any{
			"id":       seeded.ID,
			"amount":   "-1",
			"merchant": " \t",
			"category": "   ",
			"date":     "2026-08-16",
			"note":     "bad\x00note",
		})
		if !result.IsError {
			t.Fatal("invalid update_transaction IsError = false, want true")
		}
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeInvalidInput,
			"",
			false,
			map[string]any{
				"fields": []contract.FieldIssue{
					{Field: "amount", Reason: "must be a positive amount with at most two decimal places"},
					{Field: "merchant", Reason: "must not be empty"},
					{Field: "category", Reason: "must not be empty"},
					{Field: "date", Reason: "must not be in the future"},
					{Field: "note", Reason: "must not contain NUL characters"},
				},
			},
		)))
	})
}

func TestUpdateTransactionDomainErrors(t *testing.T) {
	t.Run("transaction_not_found", func(t *testing.T) {
		session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
		result := callTool(t, session, "update_transaction", map[string]any{
			"id":     int64(42),
			"amount": "23.50",
		})
		if !result.IsError {
			t.Fatal("missing update IsError = false, want true")
		}
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeTransactionNotFound,
			"Transaction 42 was not found.",
			false,
			map[string]any{"id": 42},
		)))
	})

	t.Run("category_not_found", func(t *testing.T) {
		session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
		groceries := createCategoryForMerchantTest(t, session, "Groceries")
		seeded := addTestTransaction(t, session, map[string]any{
			"amount":   "20.00",
			"merchant": "Metro",
			"category": "Groceries",
		})

		result := callTool(t, session, "update_transaction", map[string]any{
			"id":       seeded.ID,
			"category": "  Pharmacy  ",
		})
		if !result.IsError {
			t.Fatal("category_not_found IsError = false, want true")
		}
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeCategoryNotFound,
			"Category 'Pharmacy' does not exist.",
			false,
			map[string]any{
				"requested_category": "Pharmacy",
				"categories":         []contract.Category{groceries},
			},
		)))
	})

	t.Run("category_inactive", func(t *testing.T) {
		session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
		groceries := createCategoryForMerchantTest(t, session, "Groceries")
		createCategoryForMerchantTest(t, session, "Dining")
		seeded := addTestTransaction(t, session, map[string]any{
			"amount":   "20.00",
			"merchant": "Metro",
			"category": "Groceries",
		})
		disabled := callTool(t, session, "disable_category", map[string]any{"name": "Dining"})
		if disabled.IsError {
			t.Fatalf("disable Dining: %s", structuredJSON(t, disabled))
		}
		inactive := decodeCategory(t, structuredObject(t, disabled)["category"])

		result := callTool(t, session, "update_transaction", map[string]any{
			"id":       seeded.ID,
			"category": "dining",
		})
		if !result.IsError {
			t.Fatal("category_inactive IsError = false, want true")
		}
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeCategoryInactive,
			"Category 'Dining' is inactive.",
			false,
			map[string]any{
				"category":          inactive,
				"active_categories": []contract.Category{groceries},
			},
		)))
	})
}

func TestRemoveTransactionSuccess(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
	createCategoryForMerchantTest(t, session, "Groceries")
	seeded := addTestTransaction(t, session, map[string]any{
		"amount":   "20.00",
		"merchant": "Metro",
		"category": "Groceries",
		"date":     "2026-08-14",
		"note":     "weekly groceries",
	})

	result := callTool(t, session, "remove_transaction", map[string]any{"id": seeded.ID})
	if result.IsError {
		t.Fatalf("remove_transaction failed: %s", structuredJSON(t, result))
	}
	got := structuredObject(t, result)
	if keys := objectKeys(got); strings.Join(keys, ",") != "ok,removed_transaction" {
		t.Fatalf("remove_transaction keys = %v, want [ok removed_transaction]", keys)
	}
	if got["ok"] != true {
		t.Fatalf("ok = %v, want true", got["ok"])
	}
	removed := decodeTransaction(t, got["removed_transaction"])
	if mustJSON(t, removed) != mustJSON(t, seeded) {
		t.Fatalf("removed_transaction = %#v, want %#v", removed, seeded)
	}
}

func TestRemoveTransactionNotFound(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
	createCategoryForMerchantTest(t, session, "Groceries")
	seeded := addTestTransaction(t, session, map[string]any{
		"amount":   "20.00",
		"merchant": "Metro",
		"category": "Groceries",
	})

	t.Run("missing", func(t *testing.T) {
		result := callTool(t, session, "remove_transaction", map[string]any{"id": int64(42)})
		if !result.IsError {
			t.Fatal("missing remove IsError = false, want true")
		}
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeTransactionNotFound,
			"Transaction 42 was not found.",
			false,
			map[string]any{"id": 42},
		)))
	})

	t.Run("repeated", func(t *testing.T) {
		first := callTool(t, session, "remove_transaction", map[string]any{"id": seeded.ID})
		if first.IsError {
			t.Fatalf("first remove failed: %s", structuredJSON(t, first))
		}
		result := callTool(t, session, "remove_transaction", map[string]any{"id": seeded.ID})
		if !result.IsError {
			t.Fatal("repeated remove IsError = false, want true")
		}
		requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
			contract.ErrorCodeTransactionNotFound,
			"Transaction "+strconv.FormatInt(seeded.ID, 10)+" was not found.",
			false,
			map[string]any{"id": seeded.ID},
		)))
	})
}

func TestUpdateRemoveTransactionInternalError(t *testing.T) {
	db := openCategoryDB(t)
	var logs bytes.Buffer
	session := connectCategorySession(t, db, fixedTransactionNow, log.New(&logs, "", 0))
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{name: "update_transaction", args: map[string]any{"id": int64(1), "amount": "20.00"}},
		{name: "remove_transaction", args: map[string]any{"id": int64(1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs.Reset()
			result := callTool(t, session, tc.name, tc.args)
			if !result.IsError {
				t.Fatal("IsError = false, want true")
			}
			requireStructuredEqual(t, result, contract.NewInternalErrorEnvelope())
			if leakedInternalError(structuredJSON(t, result)) || leakedInternalError(toolText(result)) {
				t.Fatalf("public payload leaked internal details: %s", structuredJSON(t, result))
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

func fixedTransactionNow() time.Time {
	return time.Date(2026, time.August, 15, 12, 0, 0, 0, time.FixedZone("Toronto", -4*60*60))
}

func addTestTransaction(t *testing.T, session *mcp.ClientSession, args map[string]any) contract.Transaction {
	t.Helper()
	result := callTool(t, session, "add_transaction", args)
	if result.IsError {
		t.Fatalf("add_transaction failed: %s", structuredJSON(t, result))
	}
	return decodeTransaction(t, structuredObject(t, result)["transaction"])
}

func toolByName(t *testing.T, tools []*mcp.Tool, name string) *mcp.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("%s is not discoverable", name)
	return nil
}

func derefNote(note *string) string {
	if note == nil {
		return ""
	}
	return *note
}

func decodeTransaction(t *testing.T, value any) contract.Transaction {
	t.Helper()
	var txn contract.Transaction
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal transaction: %v", err)
	}
	if err := json.Unmarshal(raw, &txn); err != nil {
		t.Fatalf("decode transaction: %v", err)
	}
	return txn
}
