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

func TestRecurringTransactionJSONShape(t *testing.T) {
	got, err := json.Marshal(contract.RecurringTransaction{
		ID:             1,
		Merchant:       "Netflix",
		Amount:         "22.99",
		CategoryID:     3,
		Category:       "Entertainment",
		CategoryActive: true,
		DayOfMonth:     15,
		Note:           nil,
		Active:         true,
		CreatedAt:      "2026-08-30T12:00:00Z",
		UpdatedAt:      "2026-08-30T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("marshal recurring transaction: %v", err)
	}

	want := `{"id":1,"merchant":"Netflix","amount":"22.99","category_id":3,"category":"Entertainment","category_active":true,"day_of_month":15,"note":null,"active":true,"created_at":"2026-08-30T12:00:00Z","updated_at":"2026-08-30T12:00:00Z"}`
	if string(got) != want {
		t.Fatalf("recurring transaction JSON = %s, want %s", got, want)
	}
}

func TestDueTransactionJSONShape(t *testing.T) {
	got, err := json.Marshal(contract.DueTransaction{
		RecurringTransactionID: 1,
		Merchant:               "Rent",
		Amount:                 "1500.00",
		CategoryID:             1,
		Category:               "Housing",
		DueDate:                "2026-08-01",
		Note:                   nil,
	})
	if err != nil {
		t.Fatalf("marshal due transaction: %v", err)
	}

	want := `{"recurring_transaction_id":1,"merchant":"Rent","amount":"1500.00","category_id":1,"category":"Housing","due_date":"2026-08-01","note":null}`
	if string(got) != want {
		t.Fatalf("due transaction JSON = %s, want %s", got, want)
	}
}

func TestDueTransactionJSONShapeWithNote(t *testing.T) {
	note := "Monthly subscription"
	got, err := json.Marshal(contract.DueTransaction{
		RecurringTransactionID: 2,
		Merchant:               "Netflix",
		Amount:                 "22.99",
		CategoryID:             3,
		Category:               "Entertainment",
		DueDate:                "2026-08-15",
		Note:                   &note,
	})
	if err != nil {
		t.Fatalf("marshal due transaction with note: %v", err)
	}

	want := `{"recurring_transaction_id":2,"merchant":"Netflix","amount":"22.99","category_id":3,"category":"Entertainment","due_date":"2026-08-15","note":"Monthly subscription"}`
	if string(got) != want {
		t.Fatalf("due transaction JSON = %s, want %s", got, want)
	}
}

func TestBlockedDueTransactionJSONShape(t *testing.T) {
	got, err := json.Marshal(contract.BlockedDueTransaction{
		RecurringTransactionID: 4,
		Merchant:               "Gym",
		Category:               "Fitness",
		DueDate:                "2026-08-30",
		Reason:                 "category_inactive",
	})
	if err != nil {
		t.Fatalf("marshal blocked due transaction: %v", err)
	}

	want := `{"recurring_transaction_id":4,"merchant":"Gym","category":"Fitness","due_date":"2026-08-30","reason":"category_inactive"}`
	if string(got) != want {
		t.Fatalf("blocked due transaction JSON = %s, want %s", got, want)
	}
}
