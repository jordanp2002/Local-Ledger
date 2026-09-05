package category_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/jordanp2002/Local-Ledger/internal/category"
	"github.com/jordanp2002/Local-Ledger/internal/contract"
)

func TestListEmptyDatabase(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)

	got, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if got == nil {
		t.Fatal("List() = nil, want empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("List() = %#v, want empty", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal list: %v", err)
	}
	if string(encoded) != "[]" {
		t.Fatalf("List() JSON = %s, want []", encoded)
	}
}

func TestCreateTrimsASCIIWhitespace(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)

	cat, created, reactivated, err := store.Create(ctx, " \t\n\r\v\fGroceries \t\n\r\v\f")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !created || reactivated {
		t.Fatalf("Create() created=%v reactivated=%v, want created=true reactivated=false", created, reactivated)
	}
	if cat.Name != "Groceries" {
		t.Fatalf("Create() name = %q, want Groceries", cat.Name)
	}
	if cat.ID <= 0 {
		t.Fatalf("Create() id = %d, want positive", cat.ID)
	}
	if !cat.Active {
		t.Fatal("Create() active = false, want true")
	}
	if cat.CreatedAt == "" || cat.UpdatedAt == "" {
		t.Fatalf("Create() timestamps = (%q, %q), want SQLite defaults", cat.CreatedAt, cat.UpdatedAt)
	}
}

func TestCreateRejectsEmptyAndWhitespaceOnlyNames(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)

	names := []string{"", "   ", "\t", "\n", "\r", "\v", "\f", " \t\n\r\v\f "}
	for _, name := range names {
		t.Run(nameLabel(name), func(t *testing.T) {
			cat, created, reactivated, err := store.Create(ctx, name)
			if !errors.Is(err, category.ErrInvalidName) {
				t.Fatalf("Create(%q) error = %v, want ErrInvalidName", name, err)
			}
			if created || reactivated {
				t.Fatalf("Create(%q) created=%v reactivated=%v, want both false", name, created, reactivated)
			}
			if cat != (contract.Category{}) {
				t.Fatalf("Create(%q) category = %#v, want zero value", name, cat)
			}
		})
	}

	if got := countRows(t, ctx, store.DB, `SELECT count(*) FROM categories`); got != 0 {
		t.Fatalf("categories after invalid creates = %d, want 0", got)
	}
}

func TestCreateRejectsNamesContainingNUL(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)

	for _, name := range []string{"\x00", " \x00 ", "Food\x00Test"} {
		cat, created, reactivated, err := store.Create(ctx, name)
		if !errors.Is(err, category.ErrNameContainsNUL) {
			t.Fatalf("Create(%q) error = %v, want ErrNameContainsNUL", name, err)
		}
		if !errors.Is(err, category.ErrInvalidName) {
			t.Fatalf("Create(%q) error = %v, want ErrInvalidName family", name, err)
		}
		if created || reactivated || cat != (contract.Category{}) {
			t.Fatalf("Create(%q) = (%#v, %v, %v), want zero result", name, cat, created, reactivated)
		}
	}

	if got := countRows(t, ctx, store.DB, `SELECT count(*) FROM categories`); got != 0 {
		t.Fatalf("categories after NUL creates = %d, want 0", got)
	}
}

func TestCreateActiveDuplicateDifferentCasing(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	original := mustCreate(t, ctx, store, "Groceries")

	cat, created, reactivated, err := store.Create(ctx, "gROCERIES")
	if !errors.Is(err, category.ErrAlreadyExists) {
		t.Fatalf("Create(duplicate) error = %v, want ErrAlreadyExists", err)
	}
	if created || reactivated {
		t.Fatalf("Create(duplicate) created=%v reactivated=%v, want both false", created, reactivated)
	}
	if cat.ID != original.ID || cat.Name != "Groceries" || !cat.Active {
		t.Fatalf("Create(duplicate) = %#v, want canonical %#v", cat, original)
	}
	if got := countRows(t, ctx, store.DB, `SELECT count(*) FROM categories`); got != 1 {
		t.Fatalf("category count = %d, want 1", got)
	}
}

func TestCreateReactivatesInactiveDuplicate(t *testing.T) {
	ctx := context.Background()
	now := torontoClock(t, 2026, 8, 15, 12, 0)
	store := storeWithNow(t, now)
	original := mustCreate(t, ctx, store, "Groceries")

	disabled, changed, _, err := store.Disable(ctx, "Groceries")
	if err != nil {
		t.Fatalf("Disable() error = %v", err)
	}
	if !changed || disabled.Active {
		t.Fatalf("Disable() changed=%v active=%v, want changed=true active=false", changed, disabled.Active)
	}

	setUpdatedAt(t, ctx, store.DB, original.ID, "2020-01-01T00:00:00.000Z")

	cat, created, reactivated, err := store.Create(ctx, "GROCERIES")
	if err != nil {
		t.Fatalf("Create(inactive) error = %v", err)
	}
	if created || !reactivated {
		t.Fatalf("Create(inactive) created=%v reactivated=%v, want created=false reactivated=true", created, reactivated)
	}
	if cat.ID != original.ID {
		t.Fatalf("reactivated id = %d, want %d", cat.ID, original.ID)
	}
	if cat.Name != "Groceries" {
		t.Fatalf("reactivated name = %q, want original Groceries", cat.Name)
	}
	if !cat.Active {
		t.Fatal("reactivated active = false, want true")
	}
	if cat.CreatedAt != original.CreatedAt {
		t.Fatalf("reactivated created_at = %q, want %q", cat.CreatedAt, original.CreatedAt)
	}
	if cat.UpdatedAt == "2020-01-01T00:00:00.000Z" {
		t.Fatal("reactivation did not advance updated_at")
	}
	if got := countRows(t, ctx, store.DB, `SELECT count(*) FROM categories`); got != 1 {
		t.Fatalf("category count = %d, want 1", got)
	}
}

func TestCreateReactivationDoesNotRestoreDeletedAllocation(t *testing.T) {
	ctx := context.Background()
	now := torontoClock(t, 2026, 8, 15, 12, 0)
	store := storeWithNow(t, now)
	cat := mustCreate(t, ctx, store, "Groceries")
	insertBudget(t, ctx, store.DB, cat.ID, "2026-08", "500.00")

	if _, _, removed, err := store.Disable(ctx, "Groceries"); err != nil {
		t.Fatalf("Disable() error = %v", err)
	} else if removed == nil {
		t.Fatal("Disable() removed = nil, want current-month budget")
	}

	reactivated, created, wasReactivated, err := store.Create(ctx, "groceries")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created || !wasReactivated || reactivated.ID != cat.ID {
		t.Fatalf("Create() created=%v reactivated=%v id=%d, want reactivate of %d", created, wasReactivated, reactivated.ID, cat.ID)
	}
	if budgetExists(t, ctx, store.DB, cat.ID, "2026-08") {
		t.Fatal("reactivation restored the deleted current-month budget")
	}
	if got := countRows(t, ctx, store.DB, `SELECT count(*) FROM budgets`); got != 0 {
		t.Fatalf("budget count after reactivation = %d, want 0", got)
	}
}

func TestListHidesInactiveAndOrdersCaseInsensitively(t *testing.T) {
	ctx := context.Background()
	now := torontoClock(t, 2026, 8, 15, 12, 0)
	store := storeWithNow(t, now)

	banana := mustCreate(t, ctx, store, "banana")
	cherry := mustCreate(t, ctx, store, "Cherry")
	apple := mustCreate(t, ctx, store, "Apple")
	dining := mustCreate(t, ctx, store, "Dining")

	if _, _, _, err := store.Disable(ctx, "Dining"); err != nil {
		t.Fatalf("Disable(Dining) error = %v", err)
	}

	got, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("List() = %#v, want 3 active rows", got)
	}
	if got[0].ID != apple.ID || got[0].Name != "Apple" {
		t.Fatalf("List()[0] = %#v, want Apple id=%d", got[0], apple.ID)
	}
	if got[1].ID != banana.ID || got[1].Name != "banana" {
		t.Fatalf("List()[1] = %#v, want banana id=%d", got[1], banana.ID)
	}
	if got[2].ID != cherry.ID || got[2].Name != "Cherry" {
		t.Fatalf("List()[2] = %#v, want Cherry id=%d", got[2], cherry.ID)
	}
	for _, cat := range got {
		if cat.Name == dining.Name || cat.ID == dining.ID {
			t.Fatalf("List() included inactive category %#v", cat)
		}
		if !cat.Active {
			t.Fatalf("List() included inactive row %#v", cat)
		}
	}
}

func nameLabel(name string) string {
	if name == "" {
		return "empty"
	}
	return strings.ReplaceAll(strconv.Quote(name), `"`, "")
}
