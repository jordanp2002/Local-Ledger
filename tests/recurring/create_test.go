package recurring_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/recurring"
)

func TestCreateRecurringTransactionSuccess(t *testing.T) {
	store, catStore, db := openRecurringStore(t)
	ctx := context.Background()

	ent := mustCreateCategory(t, ctx, catStore, "Entertainment")

	res, issues, err := store.Create(ctx, recurring.CreateInput{
		Merchant:   "Netflix",
		Amount:     "22.99",
		Category:   "entertainment", // case-insensitive match
		DayOfMonth: 15,
		Note:       stringPointer("Monthly subscription"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("Create() unexpected field issues = %v", issues)
	}

	tmpl := res.RecurringTransaction
	if tmpl.ID <= 0 {
		t.Fatalf("ID = %d, want positive", tmpl.ID)
	}
	if tmpl.Merchant != "Netflix" {
		t.Errorf("Merchant = %q, want %q", tmpl.Merchant, "Netflix")
	}
	if tmpl.Amount != "22.99" {
		t.Errorf("Amount = %q, want %q", tmpl.Amount, "22.99")
	}
	if tmpl.CategoryID != ent.ID {
		t.Errorf("CategoryID = %d, want %d", tmpl.CategoryID, ent.ID)
	}
	if tmpl.Category != "Entertainment" {
		t.Errorf("Category = %q, want %q", tmpl.Category, "Entertainment")
	}
	if !tmpl.CategoryActive {
		t.Errorf("CategoryActive = false, want true")
	}
	if tmpl.DayOfMonth != 15 {
		t.Errorf("DayOfMonth = %d, want 15", tmpl.DayOfMonth)
	}
	if tmpl.Note == nil || *tmpl.Note != "Monthly subscription" {
		t.Errorf("Note = %v, want %q", tmpl.Note, "Monthly subscription")
	}
	if !tmpl.Active {
		t.Errorf("Active = false, want true")
	}
	const wantTimestamp = "2026-08-30T12:00:00.000Z"
	if tmpl.CreatedAt != wantTimestamp || tmpl.UpdatedAt != wantTimestamp {
		t.Errorf("timestamps = (%q, %q), want (%q, %q)", tmpl.CreatedAt, tmpl.UpdatedAt, wantTimestamp, wantTimestamp)
	}
	if count := countRows(t, ctx, db, "SELECT count(*) FROM transactions"); count != 0 {
		t.Fatalf("transactions count = %d, want 0", count)
	}
	if count := countRows(t, ctx, db, "SELECT count(*) FROM budgets"); count != 0 {
		t.Fatalf("budgets count = %d, want 0", count)
	}
	if count := countRows(t, ctx, db, "SELECT count(*) FROM known_merchants"); count != 0 {
		t.Fatalf("known_merchants count = %d, want 0", count)
	}
}

func TestCreateRecurringTransactionAmountAndNoteNormalization(t *testing.T) {
	store, catStore, _ := openRecurringStore(t)
	ctx := context.Background()

	mustCreateCategory(t, ctx, catStore, "Utilities")

	tests := []struct {
		name       string
		amount     string
		note       *string
		wantAmount string
		wantNote   *string
	}{
		{
			name:       "integer amount normalized to two decimals",
			amount:     "10",
			note:       stringPointer("  Water bill  "),
			wantAmount: "10.00",
			wantNote:   stringPointer("Water bill"),
		},
		{
			name:       "one decimal normalized to two decimals",
			amount:     "5.5",
			note:       stringPointer(""),
			wantAmount: "5.50",
			wantNote:   nil,
		},
		{
			name:       "whitespace only note becomes null",
			amount:     "100.00",
			note:       stringPointer("   \t  "),
			wantAmount: "100.00",
			wantNote:   nil,
		},
		{
			name:       "nil note remains null",
			amount:     "50.25",
			note:       nil,
			wantAmount: "50.25",
			wantNote:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, issues, err := store.Create(ctx, recurring.CreateInput{
				Merchant:   "City Utilities",
				Amount:     tt.amount,
				Category:   "Utilities",
				DayOfMonth: 1,
				Note:       tt.note,
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if len(issues) != 0 {
				t.Fatalf("Create() unexpected field issues = %v", issues)
			}
			if res.RecurringTransaction.Amount != tt.wantAmount {
				t.Errorf("Amount = %q, want %q", res.RecurringTransaction.Amount, tt.wantAmount)
			}
			if tt.wantNote == nil && res.RecurringTransaction.Note != nil {
				t.Errorf("Note = %v, want nil", *res.RecurringTransaction.Note)
			} else if tt.wantNote != nil {
				if res.RecurringTransaction.Note == nil {
					t.Fatalf("Note = nil, want %q", *tt.wantNote)
				}
				if *res.RecurringTransaction.Note != *tt.wantNote {
					t.Errorf("Note = %q, want %q", *res.RecurringTransaction.Note, *tt.wantNote)
				}
			}
		})
	}
}

func TestCreateRecurringTransactionIdenticalTemplatesAllowed(t *testing.T) {
	store, catStore, _ := openRecurringStore(t)
	ctx := context.Background()

	mustCreateCategory(t, ctx, catStore, "Entertainment")

	input := recurring.CreateInput{
		Merchant:   "Netflix",
		Amount:     "22.99",
		Category:   "Entertainment",
		DayOfMonth: 15,
		Note:       stringPointer("Account 1"),
	}

	res1, issues1, err1 := store.Create(ctx, input)
	if err1 != nil || len(issues1) != 0 {
		t.Fatalf("first Create() error = %v, issues = %v", err1, issues1)
	}

	res2, issues2, err2 := store.Create(ctx, input)
	if err2 != nil || len(issues2) != 0 {
		t.Fatalf("second Create() error = %v, issues = %v", err2, issues2)
	}

	if res1.RecurringTransaction.ID == res2.RecurringTransaction.ID {
		t.Fatalf("second template must have distinct ID, got %d for both", res1.RecurringTransaction.ID)
	}
}

func TestCreateRecurringTransactionMissingCategory(t *testing.T) {
	store, catStore, _ := openRecurringStore(t)
	ctx := context.Background()

	groceries := mustCreateCategory(t, ctx, catStore, "Groceries")

	_, issues, err := store.Create(ctx, recurring.CreateInput{
		Merchant:   "Netflix",
		Amount:     "22.99",
		Category:   "Entertainment",
		DayOfMonth: 15,
	})
	if err == nil {
		t.Fatal("Create() error = nil, want category not found")
	}
	if len(issues) != 0 {
		t.Fatalf("unexpected field issues = %v", issues)
	}

	var notFound *recurring.CategoryNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("error = %v, want *recurring.CategoryNotFoundError", err)
	}
	if notFound.Requested != "Entertainment" {
		t.Errorf("Requested = %q, want %q", notFound.Requested, "Entertainment")
	}
	if len(notFound.ActiveCategories) != 1 || notFound.ActiveCategories[0].Name != groceries.Name {
		t.Errorf("ActiveCategories = %v, want [%v]", notFound.ActiveCategories, groceries.Name)
	}
}

func TestCreateRecurringTransactionInactiveCategory(t *testing.T) {
	store, catStore, _ := openRecurringStore(t)
	ctx := context.Background()

	ent := mustCreateCategory(t, ctx, catStore, "Entertainment")
	mustCreateCategory(t, ctx, catStore, "Groceries")
	mustDisableCategory(t, ctx, catStore, "Entertainment")

	_, issues, err := store.Create(ctx, recurring.CreateInput{
		Merchant:   "Netflix",
		Amount:     "22.99",
		Category:   "Entertainment",
		DayOfMonth: 15,
	})
	if err == nil {
		t.Fatal("Create() error = nil, want category inactive")
	}
	if len(issues) != 0 {
		t.Fatalf("unexpected field issues = %v", issues)
	}

	var inactive *recurring.CategoryInactiveError
	if !errors.As(err, &inactive) {
		t.Fatalf("error = %v, want *recurring.CategoryInactiveError", err)
	}
	if inactive.Category.ID != ent.ID || inactive.Category.Name != "Entertainment" {
		t.Errorf("Category = %v, want %q", inactive.Category, "Entertainment")
	}
	if len(inactive.ActiveCategories) != 1 || inactive.ActiveCategories[0].Name != "Groceries" {
		t.Errorf("ActiveCategories = %v, want [Groceries]", inactive.ActiveCategories)
	}
}

func TestCreateRecurringTransactionFieldValidation(t *testing.T) {
	store, catStore, _ := openRecurringStore(t)
	ctx := context.Background()

	mustCreateCategory(t, ctx, catStore, "General")

	tests := []struct {
		name       string
		input      recurring.CreateInput
		wantIssues []contract.FieldIssue
	}{
		{
			name: "empty merchant",
			input: recurring.CreateInput{
				Merchant:   "",
				Amount:     "10.00",
				Category:   "General",
				DayOfMonth: 1,
			},
			wantIssues: []contract.FieldIssue{
				{Field: "merchant", Reason: "must not be empty"},
			},
		},
		{
			name: "whitespace merchant",
			input: recurring.CreateInput{
				Merchant:   "   ",
				Amount:     "10.00",
				Category:   "General",
				DayOfMonth: 1,
			},
			wantIssues: []contract.FieldIssue{
				{Field: "merchant", Reason: "must not be empty"},
			},
		},
		{
			name: "nul character in merchant",
			input: recurring.CreateInput{
				Merchant:   "Net\x00flix",
				Amount:     "10.00",
				Category:   "General",
				DayOfMonth: 1,
			},
			wantIssues: []contract.FieldIssue{
				{Field: "merchant", Reason: "must not contain NUL characters"},
			},
		},
		{
			name: "zero amount",
			input: recurring.CreateInput{
				Merchant:   "Netflix",
				Amount:     "0.00",
				Category:   "General",
				DayOfMonth: 1,
			},
			wantIssues: []contract.FieldIssue{
				{Field: "amount", Reason: "must be greater than zero"},
			},
		},
		{
			name: "negative amount",
			input: recurring.CreateInput{
				Merchant:   "Netflix",
				Amount:     "-10.00",
				Category:   "General",
				DayOfMonth: 1,
			},
			wantIssues: []contract.FieldIssue{
				{Field: "amount", Reason: "must be a positive amount with at most two decimal places"},
			},
		},
		{
			name: "three decimal places",
			input: recurring.CreateInput{
				Merchant:   "Netflix",
				Amount:     "10.999",
				Category:   "General",
				DayOfMonth: 1,
			},
			wantIssues: []contract.FieldIssue{
				{Field: "amount", Reason: "must be a positive amount with at most two decimal places"},
			},
		},
		{
			name: "empty category",
			input: recurring.CreateInput{
				Merchant:   "Netflix",
				Amount:     "10.00",
				Category:   "",
				DayOfMonth: 1,
			},
			wantIssues: []contract.FieldIssue{
				{Field: "category", Reason: "must not be empty"},
			},
		},
		{
			name: "nul category",
			input: recurring.CreateInput{
				Merchant:   "Netflix",
				Amount:     "10.00",
				Category:   "Gen\x00eral",
				DayOfMonth: 1,
			},
			wantIssues: []contract.FieldIssue{
				{Field: "category", Reason: "must not contain NUL characters"},
			},
		},
		{
			name: "day of month 0",
			input: recurring.CreateInput{
				Merchant:   "Netflix",
				Amount:     "10.00",
				Category:   "General",
				DayOfMonth: 0,
			},
			wantIssues: []contract.FieldIssue{
				{Field: "day_of_month", Reason: "must be an integer between 1 and 31"},
			},
		},
		{
			name: "day of month 32",
			input: recurring.CreateInput{
				Merchant:   "Netflix",
				Amount:     "10.00",
				Category:   "General",
				DayOfMonth: 32,
			},
			wantIssues: []contract.FieldIssue{
				{Field: "day_of_month", Reason: "must be an integer between 1 and 31"},
			},
		},
		{
			name: "negative day of month",
			input: recurring.CreateInput{
				Merchant:   "Netflix",
				Amount:     "10.00",
				Category:   "General",
				DayOfMonth: -1,
			},
			wantIssues: []contract.FieldIssue{
				{Field: "day_of_month", Reason: "must be an integer between 1 and 31"},
			},
		},
		{
			name: "nul character in note",
			input: recurring.CreateInput{
				Merchant:   "Netflix",
				Amount:     "10.00",
				Category:   "General",
				DayOfMonth: 1,
				Note:       stringPointer("My\x00note"),
			},
			wantIssues: []contract.FieldIssue{
				{Field: "note", Reason: "must not contain NUL characters"},
			},
		},
		{
			name: "multiple invalid fields returned in order",
			input: recurring.CreateInput{
				Merchant:   "",
				Amount:     "0.00",
				Category:   "",
				DayOfMonth: 50,
				Note:       stringPointer("NUL\x00note"),
			},
			wantIssues: []contract.FieldIssue{
				{Field: "merchant", Reason: "must not be empty"},
				{Field: "amount", Reason: "must be greater than zero"},
				{Field: "category", Reason: "must not be empty"},
				{Field: "day_of_month", Reason: "must be an integer between 1 and 31"},
				{Field: "note", Reason: "must not contain NUL characters"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, issues, err := store.Create(ctx, tt.input)
			if err != nil {
				t.Fatalf("Create() error = %v, want nil", err)
			}
			if !reflect.DeepEqual(issues, tt.wantIssues) {
				t.Fatalf("issues = %v, want %v", issues, tt.wantIssues)
			}
		})
	}
}
