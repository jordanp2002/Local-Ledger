package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
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
	if len(result.Tools) != 8 {
		t.Fatalf("tool count = %d, want 8", len(result.Tools))
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

func fixedTransactionNow() time.Time {
	return time.Date(2026, time.August, 15, 12, 0, 0, 0, time.FixedZone("Toronto", -4*60*60))
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
