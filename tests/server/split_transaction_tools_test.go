package server_test

import (
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

func TestAddSplitTransactionToolReturnsCanonicalParent(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	splitTool := toolByName(t, tools.Tools, "add_split_transaction")
	splitSchema := schemaObject(t, splitTool.InputSchema)
	required, _ := splitSchema["required"].([]any)
	if !containsValue(required, "merchant") || !containsValue(required, "allocations") {
		t.Fatalf("add_split_transaction required = %v, want merchant and allocations", required)
	}
	if containsValue(required, "date") || containsValue(required, "note") || containsValue(required, "idempotency_key") {
		t.Fatalf("add_split_transaction required = %v, want date, note, and idempotency_key optional", required)
	}
	createCategoryForMerchantTest(t, session, "Groceries")
	createCategoryForMerchantTest(t, session, "Household")

	result := callTool(t, session, "add_split_transaction", map[string]any{
		"merchant": "Costco",
		"date":     "2026-08-14",
		"allocations": []map[string]any{
			{"category": "Household", "amount": "20.00"},
			{"category": "Groceries", "amount": "65.00"},
		},
	})
	if result.IsError {
		t.Fatalf("add_split_transaction failed: %s", structuredJSON(t, result))
	}
	got := structuredObject(t, result)
	if got["ok"] != true || got["category_source"] != "provided" || got["merchant_mapping_action"] != "preserved" || got["idempotent_replay"] != false {
		t.Fatalf("split metadata = %s", structuredJSON(t, result))
	}
	txn := decodeTransaction(t, got["transaction"])
	if txn.Amount != "85.00" || txn.Merchant != "Costco" || txn.CategoryID != nil || txn.Category != nil || len(txn.Allocations) != 2 {
		t.Fatalf("split transaction = %#v", txn)
	}
}

func TestUpdateSplitTransactionToolRejectsLegacyAmount(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
	createCategoryForMerchantTest(t, session, "Groceries")
	createCategoryForMerchantTest(t, session, "Household")
	added := callTool(t, session, "add_split_transaction", map[string]any{
		"merchant": "Costco",
		"date":     "2026-08-14",
		"allocations": []map[string]any{
			{"category": "Groceries", "amount": "65.00"},
			{"category": "Household", "amount": "20.00"},
		},
	})
	if added.IsError {
		t.Fatalf("add_split_transaction failed: %s", structuredJSON(t, added))
	}
	id := decodeTransaction(t, structuredObject(t, added)["transaction"]).ID
	result := callTool(t, session, "update_transaction", map[string]any{"id": id, "amount": "84.00"})
	if !result.IsError {
		t.Fatal("legacy split update IsError = false, want true")
	}
	requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeSplitTransactionRequiresAllocations,
		"This split transaction must be updated by supplying its complete allocations.",
		false,
		map[string]any{"id": id},
	)))
}

func TestUpdateSplitTransactionToolRejectsOneAllocation(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
	createCategoryForMerchantTest(t, session, "Groceries")
	createCategoryForMerchantTest(t, session, "Household")
	added := callTool(t, session, "add_split_transaction", map[string]any{
		"merchant": "Costco",
		"allocations": []map[string]any{
			{"category": "Groceries", "amount": "65.00"},
			{"category": "Household", "amount": "20.00"},
		},
	})
	id := decodeTransaction(t, structuredObject(t, added)["transaction"]).ID
	result := callTool(t, session, "update_transaction", map[string]any{
		"id":          id,
		"allocations": []map[string]any{{"category": "Groceries", "amount": "85.00"}},
	})
	if !result.IsError {
		t.Fatalf("one-allocation update succeeded: %s", structuredJSON(t, result))
	}
	errorPayload := objectField(t, structuredObject(t, result), "error")
	if errorPayload["code"] != string(contract.ErrorCodeInvalidInput) {
		t.Fatalf("one-allocation update error: %s", structuredJSON(t, result))
	}
}
