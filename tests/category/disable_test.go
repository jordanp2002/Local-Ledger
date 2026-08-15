package category_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/category"
	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

func TestDisableMissingName(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	mustCreate(t, ctx, store, "Groceries")

	cat, changed, removed, err := store.Disable(ctx, "Pharmacy")
	if !errors.Is(err, category.ErrNotFound) {
		t.Fatalf("Disable(missing) error = %v, want ErrNotFound", err)
	}
	if changed || removed != nil || cat != (contract.Category{}) {
		t.Fatalf("Disable(missing) = (%#v, %v, %#v), want zero result", cat, changed, removed)
	}
	if got := countRows(t, ctx, store.DB, `SELECT count(*) FROM categories`); got != 1 {
		t.Fatalf("category count after missing disable = %d, want 1", got)
	}

	_, _, _, err = store.Disable(ctx, " \t ")
	if !errors.Is(err, category.ErrInvalidName) {
		t.Fatalf("Disable(whitespace) error = %v, want ErrInvalidName", err)
	}
}

func TestDisableFirstThenIdempotent(t *testing.T) {
	ctx := context.Background()
	now := torontoClock(t, 2026, 8, 15, 12, 0)
	store := storeWithNow(t, now)
	original := mustCreate(t, ctx, store, "Dining")

	first, changed, removed, err := store.Disable(ctx, "Dining")
	if err != nil {
		t.Fatalf("first Disable() error = %v", err)
	}
	if !changed {
		t.Fatal("first Disable() changed = false, want true")
	}
	if removed != nil {
		t.Fatalf("first Disable() removed = %#v, want nil", removed)
	}
	if first.ID != original.ID || first.Active {
		t.Fatalf("first Disable() = %#v, want inactive id=%d", first, original.ID)
	}

	const frozen = "2020-01-01T00:00:00.000Z"
	setUpdatedAt(t, ctx, store.DB, original.ID, frozen)

	second, changed, removed, err := store.Disable(ctx, "dining")
	if err != nil {
		t.Fatalf("second Disable() error = %v", err)
	}
	if changed {
		t.Fatal("second Disable() changed = true, want false")
	}
	if removed != nil {
		t.Fatalf("second Disable() removed = %#v, want nil", removed)
	}
	if second.UpdatedAt != frozen {
		t.Fatalf("second Disable() updated_at = %q, want unchanged %q", second.UpdatedAt, frozen)
	}
	if second.ID != original.ID || second.Active {
		t.Fatalf("second Disable() = %#v, want inactive original row", second)
	}
}

func TestDisableUsesNonUTCLocalClock(t *testing.T) {
	ctx := context.Background()
	now := torontoClock(t, 2026, 8, 15, 12, 0)
	store := storeWithNow(t, now)
	cat := mustCreate(t, ctx, store, "Groceries")
	insertBudget(t, ctx, store.DB, cat.ID, "2026-07", "100.00")
	insertBudget(t, ctx, store.DB, cat.ID, "2026-08", "200.00")
	insertBudget(t, ctx, store.DB, cat.ID, "2026-09", "300.00")

	_, changed, removed, err := store.Disable(ctx, "Groceries")
	if err != nil {
		t.Fatalf("Disable() error = %v", err)
	}
	if !changed {
		t.Fatal("Disable() changed = false, want true")
	}
	if removed == nil || removed.Month != "2026-08" || removed.Amount != "200.00" {
		t.Fatalf("Disable() removed = %#v, want August 200.00", removed)
	}
	if budgetExists(t, ctx, store.DB, cat.ID, "2026-08") {
		t.Fatal("current-month August budget is still present")
	}
	if !budgetExists(t, ctx, store.DB, cat.ID, "2026-07") || !budgetExists(t, ctx, store.DB, cat.ID, "2026-09") {
		t.Fatal("non-current budgets were removed")
	}
}

func TestDisableMonthBoundaryDoesNotConvertToUTC(t *testing.T) {
	ctx := context.Background()
	toronto := torontoClock(t, 2026, 8, 31, 23, 30)
	utc := toronto.UTC()
	if category.LocalMonth(toronto) != "2026-08" || category.LocalMonth(utc) != "2026-09" {
		t.Fatalf("LocalMonth(toronto)=%q LocalMonth(utc)=%q", category.LocalMonth(toronto), category.LocalMonth(utc))
	}

	t.Run("toronto", func(t *testing.T) {
		store := storeWithNow(t, toronto)
		cat := mustCreate(t, ctx, store, "Groceries")
		insertBudget(t, ctx, store.DB, cat.ID, "2026-08", "80.00")
		insertBudget(t, ctx, store.DB, cat.ID, "2026-09", "90.00")

		_, _, removed, err := store.Disable(ctx, "Groceries")
		if err != nil {
			t.Fatalf("Disable() error = %v", err)
		}
		if removed == nil || removed.Month != "2026-08" {
			t.Fatalf("Toronto Disable() removed = %#v, want 2026-08", removed)
		}
		if budgetExists(t, ctx, store.DB, cat.ID, "2026-08") {
			t.Fatal("Toronto Disable() deleted using UTC month")
		}
		if !budgetExists(t, ctx, store.DB, cat.ID, "2026-09") {
			t.Fatal("Toronto Disable() removed the September budget")
		}
	})

	t.Run("utc", func(t *testing.T) {
		store := storeWithNow(t, utc)
		cat := mustCreate(t, ctx, store, "Groceries")
		insertBudget(t, ctx, store.DB, cat.ID, "2026-08", "80.00")
		insertBudget(t, ctx, store.DB, cat.ID, "2026-09", "90.00")

		_, _, removed, err := store.Disable(ctx, "Groceries")
		if err != nil {
			t.Fatalf("Disable() error = %v", err)
		}
		if removed == nil || removed.Month != "2026-09" {
			t.Fatalf("UTC Disable() removed = %#v, want 2026-09", removed)
		}
		if budgetExists(t, ctx, store.DB, cat.ID, "2026-09") {
			t.Fatal("UTC Disable() left the September budget")
		}
		if !budgetExists(t, ctx, store.DB, cat.ID, "2026-08") {
			t.Fatal("UTC Disable() removed the August budget")
		}
	})
}

func TestDisableReturnsCanonicalRemovedBudgetOrNil(t *testing.T) {
	ctx := context.Background()
	now := torontoClock(t, 2026, 8, 15, 12, 0)

	t.Run("present", func(t *testing.T) {
		store := storeWithNow(t, now)
		cat := mustCreate(t, ctx, store, "Groceries")
		budgetID := insertBudget(t, ctx, store.DB, cat.ID, "2026-08", "500.00")
		_, amount, createdAt, updatedAt := loadBudget(t, ctx, store.DB, cat.ID, "2026-08")

		disabled, changed, removed, err := store.Disable(ctx, "Groceries")
		if err != nil {
			t.Fatalf("Disable() error = %v", err)
		}
		if !changed || disabled.Active {
			t.Fatalf("Disable() changed=%v active=%v", changed, disabled.Active)
		}
		if removed == nil {
			t.Fatal("Disable() removed = nil, want budget")
		}
		if removed.ID != budgetID || removed.CategoryID != cat.ID || removed.Category != "Groceries" {
			t.Fatalf("removed identity = %#v, want id=%d category=Groceries", removed, budgetID)
		}
		if removed.Month != "2026-08" || removed.Amount != amount || amount != "500.00" {
			t.Fatalf("removed month/amount = (%q, %q), want (2026-08, 500.00)", removed.Month, removed.Amount)
		}
		if removed.CreatedAt != createdAt || removed.UpdatedAt != updatedAt {
			t.Fatalf("removed timestamps = (%q, %q), want (%q, %q)", removed.CreatedAt, removed.UpdatedAt, createdAt, updatedAt)
		}
		if budgetExists(t, ctx, store.DB, cat.ID, "2026-08") {
			t.Fatal("removed budget is still stored")
		}
	})

	t.Run("absent", func(t *testing.T) {
		store := storeWithNow(t, now)
		mustCreate(t, ctx, store, "Groceries")

		_, changed, removed, err := store.Disable(ctx, "Groceries")
		if err != nil {
			t.Fatalf("Disable() error = %v", err)
		}
		if !changed {
			t.Fatal("Disable() changed = false, want true")
		}
		if removed != nil {
			t.Fatalf("Disable() removed = %#v, want nil", removed)
		}
	})
}

func TestDisableReactivateSameMonthDoesNotRecreateBudget(t *testing.T) {
	ctx := context.Background()
	now := torontoClock(t, 2026, 8, 15, 12, 0)
	store := storeWithNow(t, now)
	cat := mustCreate(t, ctx, store, "Groceries")
	insertBudget(t, ctx, store.DB, cat.ID, "2026-08", "40.00")

	if _, _, removed, err := store.Disable(ctx, "Groceries"); err != nil || removed == nil {
		t.Fatalf("Disable() removed=%#v err=%v", removed, err)
	}

	if _, created, reactivated, err := store.Create(ctx, "Groceries"); err != nil || created || !reactivated {
		t.Fatalf("Create() created=%v reactivated=%v err=%v", created, reactivated, err)
	}
	if budgetExists(t, ctx, store.DB, cat.ID, "2026-08") {
		t.Fatal("same-month reactivation recreated the removed budget")
	}
}

func TestDisablePreservesEarlierBudgetOnly(t *testing.T) {
	ctx := context.Background()
	now := torontoClock(t, 2026, 8, 15, 12, 0)
	store := storeWithNow(t, now)
	cat := mustCreate(t, ctx, store, "Groceries")
	earlierID, earlierAmount, earlierCreated, earlierUpdated := seedLoadedBudget(t, ctx, store.DB, cat.ID, "2026-07", "125.50")

	_, changed, removed, err := store.Disable(ctx, "Groceries")
	if err != nil {
		t.Fatalf("Disable() error = %v", err)
	}
	if !changed {
		t.Fatal("Disable() changed = false, want true")
	}
	if removed != nil {
		t.Fatalf("Disable() removed = %#v, want nil when only an earlier budget exists", removed)
	}

	id, amount, createdAt, updatedAt := loadBudget(t, ctx, store.DB, cat.ID, "2026-07")
	if id != earlierID || amount != earlierAmount || createdAt != earlierCreated || updatedAt != earlierUpdated {
		t.Fatalf("earlier budget changed to (%d, %q, %q, %q)", id, amount, createdAt, updatedAt)
	}
}

func TestDisablePreservesEarlierBudgetsTransactionsAndMerchants(t *testing.T) {
	ctx := context.Background()
	now := torontoClock(t, 2026, 8, 15, 12, 0)
	store := storeWithNow(t, now)
	cat := mustCreate(t, ctx, store, "Groceries")

	earlierID, earlierAmount, earlierCreated, earlierUpdated := seedLoadedBudget(t, ctx, store.DB, cat.ID, "2026-07", "300.00")
	insertBudget(t, ctx, store.DB, cat.ID, "2026-08", "500.00")
	txID := insertTransaction(t, ctx, store.DB, "Metro", "12.50", "2026-07-14", cat.ID)
	merchantID := insertKnownMerchant(t, ctx, store.DB, "Metro", cat.ID)

	var txMerchant, txDate, txCreated, txUpdated string
	var txAmount, txCategoryID int64
	if err := store.DB.QueryRowContext(ctx, `
		SELECT merchant, amount_hundredths, date, category_id, created_at, updated_at
		FROM transactions WHERE id = ?
	`, txID).Scan(&txMerchant, &txAmount, &txDate, &txCategoryID, &txCreated, &txUpdated); err != nil {
		t.Fatalf("load transaction: %v", err)
	}

	var merchantName, merchantCreated, merchantUpdated string
	var merchantCategoryID int64
	if err := store.DB.QueryRowContext(ctx, `
		SELECT merchant, category_id, created_at, updated_at
		FROM known_merchants WHERE id = ?
	`, merchantID).Scan(&merchantName, &merchantCategoryID, &merchantCreated, &merchantUpdated); err != nil {
		t.Fatalf("load known merchant: %v", err)
	}

	_, _, removed, err := store.Disable(ctx, "Groceries")
	if err != nil {
		t.Fatalf("Disable() error = %v", err)
	}
	if removed == nil || removed.Month != "2026-08" {
		t.Fatalf("Disable() removed = %#v, want current-month budget", removed)
	}

	id, amount, createdAt, updatedAt := loadBudget(t, ctx, store.DB, cat.ID, "2026-07")
	if id != earlierID || amount != earlierAmount || createdAt != earlierCreated || updatedAt != earlierUpdated {
		t.Fatalf("earlier budget mutated: (%d, %q, %q, %q)", id, amount, createdAt, updatedAt)
	}
	if budgetExists(t, ctx, store.DB, cat.ID, "2026-08") {
		t.Fatal("current-month budget is still present")
	}

	var gotMerchant, gotDate, gotTxCreated, gotTxUpdated string
	var gotAmount, gotCategoryID int64
	if err := store.DB.QueryRowContext(ctx, `
		SELECT merchant, amount_hundredths, date, category_id, created_at, updated_at
		FROM transactions WHERE id = ?
	`, txID).Scan(&gotMerchant, &gotAmount, &gotDate, &gotCategoryID, &gotTxCreated, &gotTxUpdated); err != nil {
		t.Fatalf("reload transaction: %v", err)
	}
	if gotMerchant != txMerchant || gotAmount != txAmount || gotDate != txDate || gotCategoryID != txCategoryID || gotTxCreated != txCreated || gotTxUpdated != txUpdated {
		t.Fatal("transaction row changed after disable")
	}

	var gotKnown, gotKnownCreated, gotKnownUpdated string
	var gotKnownCategory int64
	if err := store.DB.QueryRowContext(ctx, `
		SELECT merchant, category_id, created_at, updated_at
		FROM known_merchants WHERE id = ?
	`, merchantID).Scan(&gotKnown, &gotKnownCategory, &gotKnownCreated, &gotKnownUpdated); err != nil {
		t.Fatalf("reload known merchant: %v", err)
	}
	if gotKnown != merchantName || gotKnownCategory != merchantCategoryID || gotKnownCreated != merchantCreated || gotKnownUpdated != merchantUpdated {
		t.Fatal("known_merchants row changed after disable")
	}
}

func TestDisableRollsBackWhenBudgetDeleteFails(t *testing.T) {
	ctx := context.Background()
	now := torontoClock(t, 2026, 8, 15, 12, 0)
	store := storeWithNow(t, now)
	cat := mustCreate(t, ctx, store, "Groceries")
	insertBudget(t, ctx, store.DB, cat.ID, "2026-08", "75.00")

	if _, err := store.DB.ExecContext(ctx, `
		CREATE TRIGGER fail_budget_delete BEFORE DELETE ON budgets
		BEGIN SELECT RAISE(ABORT, 'test boom'); END;
	`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, changed, removed, err := store.Disable(ctx, "Groceries")
	if err == nil {
		t.Fatal("Disable() error = nil, want trigger abort")
	}
	if changed {
		t.Fatal("failed Disable() changed = true, want false")
	}
	if removed != nil {
		t.Fatalf("failed Disable() removed = %#v, want nil", removed)
	}

	var active int64
	if err := store.DB.QueryRowContext(ctx, `SELECT active FROM categories WHERE id = ?`, cat.ID).Scan(&active); err != nil {
		t.Fatalf("query category active: %v", err)
	}
	if active != 1 {
		t.Fatalf("category active after failed disable = %d, want 1", active)
	}
	if !budgetExists(t, ctx, store.DB, cat.ID, "2026-08") {
		t.Fatal("budget was deleted despite trigger abort")
	}

	if _, err := store.DB.ExecContext(ctx, `DROP TRIGGER fail_budget_delete`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}
}

func seedLoadedBudget(t *testing.T, ctx context.Context, db *sql.DB, categoryID int64, month, amount string) (int64, string, string, string) {
	t.Helper()
	insertBudget(t, ctx, db, categoryID, month, amount)
	return loadBudget(t, ctx, db, categoryID, month)
}
