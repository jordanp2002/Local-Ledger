package recurring_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/recurring"
)

func TestDisableRecurringTransactionSuccessAndRepeated(t *testing.T) {
	store, catStore, db := openRecurringStore(t)
	ctx := context.Background()

	mustCreateCategory(t, ctx, catStore, "Entertainment")

	created, _, err := store.Create(ctx, recurring.CreateInput{
		Merchant:   "Netflix",
		Amount:     "22.99",
		Category:   "Entertainment",
		DayOfMonth: 15,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	tmplID := created.RecurringTransaction.ID

	res1, issues1, err1 := store.Disable(ctx, tmplID)
	if err1 != nil {
		t.Fatalf("first Disable() error = %v", err1)
	}
	if len(issues1) != 0 {
		t.Fatalf("first Disable() unexpected issues = %v", issues1)
	}
	if !res1.Changed {
		t.Errorf("first Disable() changed = false, want true")
	}
	if res1.RecurringTransaction.Active {
		t.Errorf("first Disable() Active = true, want false")
	}
	if res1.RecurringTransaction.UpdatedAt != "2026-08-30T12:00:00.000Z" {
		t.Errorf("first Disable() updated_at = %q, want injected clock timestamp", res1.RecurringTransaction.UpdatedAt)
	}

	res2, issues2, err2 := store.Disable(ctx, tmplID)
	if err2 != nil {
		t.Fatalf("second Disable() error = %v", err2)
	}
	if len(issues2) != 0 {
		t.Fatalf("second Disable() unexpected issues = %v", issues2)
	}
	if res2.Changed {
		t.Errorf("second Disable() changed = true, want false")
	}
	if res2.RecurringTransaction.Active {
		t.Errorf("second Disable() Active = true, want false")
	}
	if res2.RecurringTransaction.UpdatedAt != res1.RecurringTransaction.UpdatedAt {
		t.Errorf("repeated disable modified updated_at: %q vs %q", res2.RecurringTransaction.UpdatedAt, res1.RecurringTransaction.UpdatedAt)
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

func TestDisableRecurringTransactionMissingID(t *testing.T) {
	store, _, _ := openRecurringStore(t)
	ctx := context.Background()

	_, issues, err := store.Disable(ctx, 999999)
	if err == nil {
		t.Fatal("Disable() error = nil, want not found")
	}
	if len(issues) != 0 {
		t.Fatalf("unexpected issues = %v", issues)
	}

	var notFound *recurring.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("error = %v, want *recurring.NotFoundError", err)
	}
	if notFound.ID != 999999 {
		t.Errorf("notFound.ID = %d, want 999999", notFound.ID)
	}
}

func TestDisableRecurringTransactionInvalidID(t *testing.T) {
	store, _, _ := openRecurringStore(t)
	ctx := context.Background()

	for _, invalidID := range []int64{0, -1, -100} {
		_, issues, err := store.Disable(ctx, invalidID)
		if err != nil {
			t.Fatalf("Disable(%d) error = %v, want nil", invalidID, err)
		}
		wantIssues := []contract.FieldIssue{
			{Field: "id", Reason: "must be a positive integer"},
		}
		if !reflect.DeepEqual(issues, wantIssues) {
			t.Fatalf("Disable(%d) issues = %v, want %v", invalidID, issues, wantIssues)
		}
	}
}
