package recurring_test

import (
	"context"
	"testing"

	"github.com/jordanp2002/Local-Ledger/internal/recurring"
)

func TestListRecurringTransactionsEmpty(t *testing.T) {
	store, _, _ := openRecurringStore(t)
	ctx := context.Background()

	items, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if items == nil {
		t.Fatal("List() returned nil, want non-nil empty slice")
	}
	if len(items) != 0 {
		t.Fatalf("List() len = %d, want 0", len(items))
	}
}

func TestListRecurringTransactionsDeterministicOrdering(t *testing.T) {
	store, catStore, _ := openRecurringStore(t)
	ctx := context.Background()

	cat := mustCreateCategory(t, ctx, catStore, "General")

	res1, _, _ := store.Create(ctx, recurring.CreateInput{
		Merchant:   "Spotify",
		Amount:     "10.99",
		Category:   cat.Name,
		DayOfMonth: 15,
	})

	res2, _, _ := store.Create(ctx, recurring.CreateInput{
		Merchant:   "Rent",
		Amount:     "1500.00",
		Category:   cat.Name,
		DayOfMonth: 1,
	})
	res3, _, _ := store.Create(ctx, recurring.CreateInput{
		Merchant:   "apple music",
		Amount:     "10.99",
		Category:   cat.Name,
		DayOfMonth: 15,
	})
	res4, _, _ := store.Create(ctx, recurring.CreateInput{
		Merchant:   "Spotify",
		Amount:     "15.99",
		Category:   cat.Name,
		DayOfMonth: 15,
	})
	res5, _, _ := store.Create(ctx, recurring.CreateInput{
		Merchant:   "Gym",
		Amount:     "50.00",
		Category:   cat.Name,
		DayOfMonth: 31,
	})

	res6, _, _ := store.Create(ctx, recurring.CreateInput{
		Merchant:   "AAA",
		Amount:     "100.00",
		Category:   cat.Name,
		DayOfMonth: 1,
	})
	_, _, err := store.Disable(ctx, res6.RecurringTransaction.ID)
	if err != nil {
		t.Fatalf("Disable() error = %v", err)
	}

	items, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	wantIDs := []int64{
		res2.RecurringTransaction.ID,
		res3.RecurringTransaction.ID,
		res1.RecurringTransaction.ID,
		res4.RecurringTransaction.ID,
		res5.RecurringTransaction.ID,
		res6.RecurringTransaction.ID,
	}

	if len(items) != len(wantIDs) {
		t.Fatalf("List() len = %d, want %d", len(items), len(wantIDs))
	}
	for i, wantID := range wantIDs {
		if items[i].ID != wantID {
			t.Errorf("items[%d].ID = %d, want %d (%s)", i, items[i].ID, wantID, items[i].Merchant)
		}
	}
}

func TestListRecurringTransactionsReflectsCategoryRename(t *testing.T) {
	store, catStore, _ := openRecurringStore(t)
	ctx := context.Background()

	mustCreateCategory(t, ctx, catStore, "Streaming")

	res, _, err := store.Create(ctx, recurring.CreateInput{
		Merchant:   "Netflix",
		Amount:     "22.99",
		Category:   "Streaming",
		DayOfMonth: 15,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	cat, _, changed, err := catStore.Rename(ctx, "Streaming", "Entertainment")
	if err != nil || !changed {
		t.Fatalf("Rename() error = %v, changed = %v", err, changed)
	}

	items, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("List() len = %d, want 1", len(items))
	}

	item := items[0]
	if item.ID != res.RecurringTransaction.ID {
		t.Errorf("ID = %d, want %d", item.ID, res.RecurringTransaction.ID)
	}
	if item.CategoryID != cat.ID {
		t.Errorf("CategoryID = %d, want %d", item.CategoryID, cat.ID)
	}
	if item.Category != "Entertainment" {
		t.Errorf("Category = %q, want %q", item.Category, "Entertainment")
	}
	if !item.CategoryActive {
		t.Errorf("CategoryActive = false, want true")
	}
}

func TestListRecurringTransactionsWithDisabledCategory(t *testing.T) {
	store, catStore, _ := openRecurringStore(t)
	ctx := context.Background()

	mustCreateCategory(t, ctx, catStore, "Fitness")

	res, _, err := store.Create(ctx, recurring.CreateInput{
		Merchant:   "Gym",
		Amount:     "50.00",
		Category:   "Fitness",
		DayOfMonth: 1,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	mustDisableCategory(t, ctx, catStore, "Fitness")

	items, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("List() len = %d, want 1", len(items))
	}

	item := items[0]
	if item.ID != res.RecurringTransaction.ID {
		t.Errorf("ID = %d, want %d", item.ID, res.RecurringTransaction.ID)
	}
	if item.Category != "Fitness" {
		t.Errorf("Category = %q, want %q", item.Category, "Fitness")
	}
	if item.CategoryActive {
		t.Errorf("CategoryActive = true, want false")
	}
}
