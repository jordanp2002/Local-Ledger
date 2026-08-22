package server_test

import (
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

func TestAddTransactionIdempotentReplayAndConflict(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
	createCategoryForMerchantTest(t, session, "Groceries")

	args := map[string]any{
		"amount":          "20.00",
		"merchant":        "Metro",
		"category":        "Groceries",
		"idempotency_key": "expense-2026-08-19-001",
	}
	first := callTool(t, session, "add_transaction", args)
	if first.IsError {
		t.Fatalf("first add_transaction: %s", structuredJSON(t, first))
	}
	got := structuredObject(t, first)
	if got["idempotent_replay"] != false {
		t.Fatalf("first write replay = %v, want false", got["idempotent_replay"])
	}
	original := decodeTransaction(t, got["transaction"])

	replay := callTool(t, session, "add_transaction", args)
	if replay.IsError {
		t.Fatalf("replay add_transaction: %s", structuredJSON(t, replay))
	}
	replayed := structuredObject(t, replay)
	if replayed["idempotent_replay"] != true {
		t.Fatalf("replay flag = %v, want true", replayed["idempotent_replay"])
	}
	if decodeTransaction(t, replayed["transaction"]).ID != original.ID {
		t.Fatal("replay changed the transaction id")
	}

	mismatch := callTool(t, session, "add_transaction", map[string]any{
		"amount":          "21.00",
		"merchant":        "Metro",
		"category":        "Groceries",
		"idempotency_key": "expense-2026-08-19-001",
	})
	if !mismatch.IsError {
		t.Fatal("payload mismatch IsError = false, want true")
	}
	requireStructuredEqual(t, mismatch, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeIdempotencyConflict,
		"The idempotency key has already been used for a different transaction request.",
		false,
		map[string]any{
			"idempotency_key": "expense-2026-08-19-001",
			"reason":          "payload_mismatch",
		},
	)))
}

func TestAddTransactionRemovedKeyConflict(t *testing.T) {
	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
	createCategoryForMerchantTest(t, session, "Groceries")

	args := map[string]any{
		"amount":          "20.00",
		"merchant":        "Metro",
		"category":        "Groceries",
		"idempotency_key": "expense-1",
	}
	first := callTool(t, session, "add_transaction", args)
	if first.IsError {
		t.Fatalf("first add_transaction: %s", structuredJSON(t, first))
	}
	id := decodeTransaction(t, structuredObject(t, first)["transaction"]).ID
	if result := callTool(t, session, "remove_transaction", map[string]any{"id": id}); result.IsError {
		t.Fatalf("remove_transaction: %s", structuredJSON(t, result))
	}

	result := callTool(t, session, "add_transaction", args)
	if !result.IsError {
		t.Fatal("retired-key replay IsError = false, want true")
	}
	requireStructuredEqual(t, result, contract.NewErrorEnvelope(contract.NewError(
		contract.ErrorCodeIdempotencyConflict,
		"The original transaction was removed; this idempotency key cannot be reused.",
		false,
		map[string]any{
			"idempotency_key": "expense-1",
			"reason":          "transaction_removed",
		},
	)))
}
