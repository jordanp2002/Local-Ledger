package contract_test

import (
	"encoding/json"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

func TestCategoryJSONShape(t *testing.T) {
	got, err := json.Marshal(contract.Category{
		ID:        1,
		Name:      "Groceries",
		Active:    true,
		CreatedAt: "2026-08-14T14:30:00Z",
		UpdatedAt: "2026-08-14T14:30:00Z",
	})
	if err != nil {
		t.Fatalf("marshal category: %v", err)
	}

	want := `{"id":1,"name":"Groceries","active":true,"created_at":"2026-08-14T14:30:00Z","updated_at":"2026-08-14T14:30:00Z"}`
	if string(got) != want {
		t.Fatalf("category JSON = %s, want %s", got, want)
	}
}

func TestBudgetJSONShapeAndNormalizedAmount(t *testing.T) {
	got, err := json.Marshal(contract.Budget{
		ID:         1,
		Month:      "2026-08",
		CategoryID: 1,
		Category:   "Groceries",
		Amount:     "500.00",
		CreatedAt:  "2026-08-14T14:30:00Z",
		UpdatedAt:  "2026-08-14T14:30:00Z",
	})
	if err != nil {
		t.Fatalf("marshal budget: %v", err)
	}

	want := `{"id":1,"month":"2026-08","category_id":1,"category":"Groceries","amount":"500.00","created_at":"2026-08-14T14:30:00Z","updated_at":"2026-08-14T14:30:00Z"}`
	if string(got) != want {
		t.Fatalf("budget JSON = %s, want %s", got, want)
	}
}

func TestTransactionJSONShapeUsesExplicitNullNote(t *testing.T) {
	got, err := json.Marshal(contract.Transaction{
		ID:         1,
		Amount:     "20.00",
		Merchant:   "Metro",
		Date:       "2026-08-14",
		CategoryID: 1,
		Category:   "Groceries",
		Note:       nil,
		CreatedAt:  "2026-08-14T14:30:00Z",
		UpdatedAt:  "2026-08-14T14:30:00Z",
	})
	if err != nil {
		t.Fatalf("marshal transaction: %v", err)
	}

	want := `{"id":1,"amount":"20.00","merchant":"Metro","date":"2026-08-14","category_id":1,"category":"Groceries","note":null,"created_at":"2026-08-14T14:30:00Z","updated_at":"2026-08-14T14:30:00Z"}`
	if string(got) != want {
		t.Fatalf("transaction JSON = %s, want %s", got, want)
	}
}

func TestKnownMerchantJSONShape(t *testing.T) {
	got, err := json.Marshal(contract.KnownMerchant{
		ID:             1,
		Merchant:       "Metro",
		CategoryID:     1,
		Category:       "Groceries",
		CategoryActive: true,
		CreatedAt:      "2026-08-14T14:30:00Z",
		UpdatedAt:      "2026-08-14T14:30:00Z",
	})
	if err != nil {
		t.Fatalf("marshal known merchant: %v", err)
	}

	want := `{"id":1,"merchant":"Metro","category_id":1,"category":"Groceries","category_active":true,"created_at":"2026-08-14T14:30:00Z","updated_at":"2026-08-14T14:30:00Z"}`
	if string(got) != want {
		t.Fatalf("known merchant JSON = %s, want %s", got, want)
	}
}

func TestTransactionJSONShapePreservesNonNullNote(t *testing.T) {
	note := "Weekly groceries"
	got, err := json.Marshal(contract.Transaction{Note: &note})
	if err != nil {
		t.Fatalf("marshal transaction: %v", err)
	}
	if string(got) != `{"id":0,"amount":"","merchant":"","date":"","category_id":0,"category":"","note":"Weekly groceries","created_at":"","updated_at":""}` {
		t.Fatalf("transaction JSON = %s, want note string and all fields", got)
	}
}
