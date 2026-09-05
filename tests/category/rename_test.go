package category_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jordanp2002/Local-Ledger/internal/category"
	"github.com/jordanp2002/Local-Ledger/internal/contract"
)

func TestRenamePreservesCategoryIdentityAndRelatedRows(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	dining := mustCreate(t, ctx, store, "Dining")

	budgetID := insertBudget(t, ctx, store.DB, dining.ID, "2026-07", "125.00")
	transactionID := insertTransaction(t, ctx, store.DB, "Restaurant", "23.50", "2026-07-14", dining.ID)
	merchantID := insertKnownMerchant(t, ctx, store.DB, "Restaurant", dining.ID)

	renamed, previousName, changed, err := store.Rename(ctx, " dining ", " Eating Out ")
	if err != nil {
		t.Fatalf("Rename() error = %v", err)
	}
	if !changed || previousName != "Dining" {
		t.Fatalf("Rename() previous=%q changed=%v, want Dining/true", previousName, changed)
	}
	if renamed.ID != dining.ID || renamed.Name != "Eating Out" || renamed.CreatedAt != dining.CreatedAt || !renamed.Active {
		t.Fatalf("Rename() = %#v, want renamed original category", renamed)
	}

	var gotBudgetID, gotBudgetCategoryID int64
	if err := store.DB.QueryRowContext(ctx, `SELECT id, category_id FROM budgets WHERE month = ?`, "2026-07").Scan(&gotBudgetID, &gotBudgetCategoryID); err != nil {
		t.Fatalf("load budget: %v", err)
	}
	if gotBudgetID != budgetID || gotBudgetCategoryID != dining.ID {
		t.Fatalf("budget = (%d, %d), want (%d, %d)", gotBudgetID, gotBudgetCategoryID, budgetID, dining.ID)
	}

	var gotTransactionID, gotTransactionCategoryID int64
	var gotMerchant string
	if err := store.DB.QueryRowContext(ctx, `
		SELECT t.id, t.merchant, a.category_id
		FROM transactions AS t
		INNER JOIN transaction_allocations AS a ON a.transaction_id = t.id
		WHERE t.id = ?
	`, transactionID).Scan(&gotTransactionID, &gotMerchant, &gotTransactionCategoryID); err != nil {
		t.Fatalf("load transaction: %v", err)
	}
	if gotTransactionID != transactionID || gotMerchant != "Restaurant" || gotTransactionCategoryID != dining.ID {
		t.Fatalf("transaction = (%d, %q, %d), want unchanged merchant/category", gotTransactionID, gotMerchant, gotTransactionCategoryID)
	}

	var gotMerchantID, gotMerchantCategoryID int64
	if err := store.DB.QueryRowContext(ctx, `SELECT id, category_id FROM known_merchants WHERE merchant = ?`, "Restaurant").Scan(&gotMerchantID, &gotMerchantCategoryID); err != nil {
		t.Fatalf("load known merchant: %v", err)
	}
	if gotMerchantID != merchantID || gotMerchantCategoryID != dining.ID {
		t.Fatalf("known merchant = (%d, %d), want (%d, %d)", gotMerchantID, gotMerchantCategoryID, merchantID, dining.ID)
	}
}

func TestRenameAllowsInactiveAndCasingOnlyNames(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	health := mustCreate(t, ctx, store, "Health")
	if _, changed, _, err := store.Disable(ctx, "Health"); err != nil || !changed {
		t.Fatalf("Disable(Health) = changed=%v err=%v", changed, err)
	}

	renamed, previousName, changed, err := store.Rename(ctx, "health", " Wellness ")
	if err != nil {
		t.Fatalf("Rename(inactive) error = %v", err)
	}
	if !changed || previousName != "Health" || renamed.ID != health.ID || renamed.Name != "Wellness" || renamed.Active {
		t.Fatalf("Rename(inactive) = (%#v, %q, %v), want same inactive identity", renamed, previousName, changed)
	}

	casing := mustCreate(t, ctx, store, "Transport")
	updated, previousName, changed, err := store.Rename(ctx, "transport", "transport")
	if err != nil {
		t.Fatalf("Rename(casing) error = %v", err)
	}
	if !changed || previousName != "Transport" || updated.Name != "transport" || updated.ID != casing.ID {
		t.Fatalf("Rename(casing) = (%#v, %q, %v), want casing-only update", updated, previousName, changed)
	}
}

func TestRenameExactNoOpLeavesUpdatedAtUnchanged(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	created := mustCreate(t, ctx, store, "Dining")
	const frozen = "2020-01-01T00:00:00.000Z"
	setUpdatedAt(t, ctx, store.DB, created.ID, frozen)

	renamed, previousName, changed, err := store.Rename(ctx, " dining ", "Dining")
	if err != nil {
		t.Fatalf("Rename(no-op) error = %v", err)
	}
	if changed || previousName != "Dining" || renamed != (contract.Category{ID: created.ID, Name: "Dining", Active: true, CreatedAt: created.CreatedAt, UpdatedAt: frozen}) {
		t.Fatalf("Rename(no-op) = (%#v, %q, %v), want unchanged category", renamed, previousName, changed)
	}
}

func TestRenameMissingAndCollisionsDoNotWrite(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	dining := mustCreate(t, ctx, store, "Dining")
	groceries := mustCreate(t, ctx, store, "Groceries")

	_, _, _, err := store.Rename(ctx, "Missing", "New Name")
	if !errors.Is(err, category.ErrNotFound) {
		t.Fatalf("Rename(missing) error = %v, want ErrNotFound", err)
	}

	_, _, _, err = store.Rename(ctx, "Dining", " groceries ")
	var collision *category.AlreadyExistsError
	if !errors.As(err, &collision) || !errors.Is(err, category.ErrAlreadyExists) {
		t.Fatalf("Rename(active collision) error = %v, want AlreadyExistsError", err)
	}
	if collision.Category != groceries {
		t.Fatalf("collision category = %#v, want %#v", collision.Category, groceries)
	}
	assertCategoryName(t, ctx, store, dining.ID, "Dining")

	if _, _, _, err := store.Disable(ctx, "Groceries"); err != nil {
		t.Fatalf("Disable(Groceries): %v", err)
	}
	_, _, _, err = store.Rename(ctx, "Dining", " groceries ")
	if !errors.As(err, &collision) || collision.Category.Active {
		t.Fatalf("Rename(inactive collision) error = %v, collision = %#v", err, collision)
	}
	assertCategoryName(t, ctx, store, dining.ID, "Dining")
}

func TestRenameValidationCollectsFieldsInInputOrder(t *testing.T) {
	store := openStore(t)
	_, _, _, err := store.Rename(context.Background(), " \t ", "\x00")
	var validation *category.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("Rename(invalid) error = %v, want ValidationError", err)
	}
	want := []contract.FieldIssue{
		{Field: "category", Reason: "must not be empty"},
		{Field: "new_name", Reason: "must not contain NUL characters"},
	}
	if len(validation.Fields) != len(want) {
		t.Fatalf("validation fields = %#v, want %#v", validation.Fields, want)
	}
	for i := range want {
		if validation.Fields[i] != want[i] {
			t.Fatalf("validation field[%d] = %#v, want %#v", i, validation.Fields[i], want[i])
		}
	}
}

func TestRenameRollsBackOnUpdateFailure(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	created := mustCreate(t, ctx, store, "Dining")
	if _, err := store.DB.ExecContext(ctx, `
		CREATE TRIGGER fail_category_rename BEFORE UPDATE OF name ON categories
		BEGIN SELECT RAISE(ABORT, 'test boom'); END
	`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, _, _, err := store.Rename(ctx, "Dining", "Eating Out")
	if err == nil {
		t.Fatal("Rename() error = nil, want trigger failure")
	}
	assertCategoryName(t, ctx, store, created.ID, "Dining")
}

func assertCategoryName(t *testing.T, ctx context.Context, store *category.Store, id int64, want string) {
	t.Helper()
	var got string
	if err := store.DB.QueryRowContext(ctx, `SELECT name FROM categories WHERE id = ?`, id).Scan(&got); err != nil {
		t.Fatalf("load category %d: %v", id, err)
	}
	if got != want {
		t.Fatalf("category %d name = %q, want %q", id, got, want)
	}
}
