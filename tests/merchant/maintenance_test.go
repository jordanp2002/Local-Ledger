package merchant_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/jordanp2002/local-finance-mcp/internal/merchant"
)

func TestRenamePreservesMappingIdentityAndTransactionText(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openStores(t)
	dining := mustCreateCategory(t, ctx, categories, "Dining")
	created, err := store.Set(ctx, "Metro", "Dining")
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	transactionID := insertMerchantTransaction(t, ctx, db, "Metro", dining.ID)
	const frozen = "2020-01-01T00:00:00.000Z"
	if _, err := db.ExecContext(ctx, `UPDATE known_merchants SET updated_at = ? WHERE id = ?`, frozen, created.KnownMerchant.ID); err != nil {
		t.Fatalf("freeze updated_at: %v", err)
	}

	renamed, previousMerchant, changed, err := store.Rename(ctx, " metro ", " Metro Grocery ")
	if err != nil {
		t.Fatalf("Rename() error = %v", err)
	}
	if !changed || previousMerchant != "Metro" {
		t.Fatalf("Rename() previous=%q changed=%v, want Metro/true", previousMerchant, changed)
	}
	if renamed.ID != created.KnownMerchant.ID || renamed.Merchant != "Metro Grocery" || renamed.CategoryID != dining.ID || renamed.CreatedAt != created.KnownMerchant.CreatedAt {
		t.Fatalf("Rename() = %#v, want preserved mapping identity/category", renamed)
	}
	if renamed.UpdatedAt == frozen {
		t.Fatal("Rename() updated_at unchanged")
	}

	var gotMerchant string
	if err := db.QueryRowContext(ctx, `SELECT merchant FROM transactions WHERE id = ?`, transactionID).Scan(&gotMerchant); err != nil {
		t.Fatalf("load transaction: %v", err)
	}
	if gotMerchant != "Metro" {
		t.Fatalf("historical transaction merchant = %q, want Metro", gotMerchant)
	}
}

func TestRenameAllowsInactiveMappingAndCasingOnlyNames(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openStores(t)
	health := mustCreateCategory(t, ctx, categories, "Health")
	created, err := store.Set(ctx, "Shoppers", "Health")
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if _, changed, _, err := categories.Disable(ctx, "Health"); err != nil || !changed {
		t.Fatalf("Disable(Health) = changed=%v err=%v", changed, err)
	}

	renamed, previousMerchant, changed, err := store.Rename(ctx, "shoppers", " Pharmacy ")
	if err != nil {
		t.Fatalf("Rename(inactive) error = %v", err)
	}
	if !changed || previousMerchant != "Shoppers" || renamed.ID != created.KnownMerchant.ID || renamed.Merchant != "Pharmacy" || renamed.CategoryID != health.ID || renamed.CategoryActive {
		t.Fatalf("Rename(inactive) = (%#v, %q, %v), want same inactive mapping", renamed, previousMerchant, changed)
	}

	groceries := mustCreateCategory(t, ctx, categories, "Groceries")
	casing, err := store.Set(ctx, "Metro", groceries.Name)
	if err != nil {
		t.Fatalf("Set(Metro) error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE known_merchants SET updated_at = ? WHERE id = ?`, "2020-01-01T00:00:00.000Z", casing.KnownMerchant.ID); err != nil {
		t.Fatalf("freeze casing mapping: %v", err)
	}
	noOp, previousMerchant, changed, err := store.Rename(ctx, "metro", "Metro")
	if err != nil {
		t.Fatalf("Rename(no-op) error = %v", err)
	}
	if changed || previousMerchant != "Metro" || noOp.UpdatedAt != "2020-01-01T00:00:00.000Z" {
		t.Fatalf("Rename(no-op) = (%#v, %q, %v), want unchanged timestamp", noOp, previousMerchant, changed)
	}

	updated, previousMerchant, changed, err := store.Rename(ctx, "metro", "metro")
	if err != nil {
		t.Fatalf("Rename(casing) error = %v", err)
	}
	if !changed || previousMerchant != "Metro" || updated.Merchant != "metro" || updated.ID != casing.KnownMerchant.ID {
		t.Fatalf("Rename(casing) = (%#v, %q, %v), want casing-only update", updated, previousMerchant, changed)
	}
}

func TestRenameMissingAndCollisionsDoNotWrite(t *testing.T) {
	ctx := context.Background()
	store, categories, _ := openStores(t)
	mustCreateCategory(t, ctx, categories, "Groceries")
	first, err := store.Set(ctx, "Metro", "Groceries")
	if err != nil {
		t.Fatalf("Set(Metro) error = %v", err)
	}
	second, err := store.Set(ctx, "Shoppers", "Groceries")
	if err != nil {
		t.Fatalf("Set(Shoppers) error = %v", err)
	}

	_, _, _, err = store.Rename(ctx, "Missing", "New Merchant")
	var missing *merchant.NotFoundError
	if !errors.As(err, &missing) || !errors.Is(err, merchant.ErrNotFound) || missing.Requested != "Missing" {
		t.Fatalf("Rename(missing) error = %v, want NotFoundError", err)
	}

	_, _, _, err = store.Rename(ctx, "Metro", " shoppers ")
	var collision *merchant.AlreadyExistsError
	if !errors.As(err, &collision) || !errors.Is(err, merchant.ErrAlreadyExists) {
		t.Fatalf("Rename(collision) error = %v, want AlreadyExistsError", err)
	}
	if collision.KnownMerchant != second.KnownMerchant {
		t.Fatalf("collision mapping = %#v, want %#v", collision.KnownMerchant, second.KnownMerchant)
	}

	var gotMerchant string
	if err := store.DB.QueryRowContext(ctx, `SELECT merchant FROM known_merchants WHERE id = ?`, first.KnownMerchant.ID).Scan(&gotMerchant); err != nil {
		t.Fatalf("load source mapping: %v", err)
	}
	if gotMerchant != "Metro" {
		t.Fatalf("source mapping merchant = %q, want Metro", gotMerchant)
	}
}

func TestRenameValidationCollectsFieldsInInputOrder(t *testing.T) {
	store, _, _ := openStores(t)
	_, _, _, err := store.Rename(context.Background(), " \t ", "\x00")
	var validation *merchant.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("Rename(invalid) error = %v, want ValidationError", err)
	}
	want := []contract.FieldIssue{
		{Field: "merchant", Reason: "must not be empty"},
		{Field: "new_merchant", Reason: "must not contain NUL characters"},
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

func TestRemoveReturnsCanonicalRecordAndPreservesHistory(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openStores(t)
	groceries := mustCreateCategory(t, ctx, categories, "Groceries")
	health := mustCreateCategory(t, ctx, categories, "Health")
	created, err := store.Set(ctx, "Metro", "Groceries")
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	inactiveCreated, err := store.Set(ctx, "Shoppers", "Health")
	if err != nil {
		t.Fatalf("Set(Shoppers) error = %v", err)
	}
	if _, changed, _, err := categories.Disable(ctx, health.Name); err != nil || !changed {
		t.Fatalf("Disable(Health) = changed=%v err=%v", changed, err)
	}
	insertMerchantTransaction(t, ctx, db, "Metro", groceries.ID)
	if _, err := db.ExecContext(ctx, `INSERT INTO budgets (category_id, month, amount_hundredths) VALUES (?, ?, ?)`, groceries.ID, "2026-08", 50000); err != nil {
		t.Fatalf("insert budget: %v", err)
	}
	inactiveRemoved, err := store.Remove(ctx, " shoppers ")
	if err != nil {
		t.Fatalf("Remove(inactive mapping) error = %v", err)
	}
	wantInactive := inactiveCreated.KnownMerchant
	wantInactive.CategoryActive = false
	if inactiveRemoved != wantInactive || inactiveRemoved.CategoryActive {
		t.Fatalf("Remove(inactive mapping) = %#v, want canonical inactive mapping %#v", inactiveRemoved, wantInactive)
	}

	removed, err := store.Remove(ctx, " metro ")
	if err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if removed != created.KnownMerchant {
		t.Fatalf("Remove() = %#v, want pre-delete canonical %#v", removed, created.KnownMerchant)
	}
	var count int64
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM known_merchants`).Scan(&count); err != nil {
		t.Fatalf("count mappings: %v", err)
	}
	if count != 0 {
		t.Fatalf("known merchant count = %d, want 0", count)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM categories`).Scan(&count); err != nil {
		t.Fatalf("count categories: %v", err)
	}
	if count != 2 {
		t.Fatalf("category count = %d, want 2", count)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM budgets`).Scan(&count); err != nil {
		t.Fatalf("count budgets: %v", err)
	}
	if count != 1 {
		t.Fatalf("budget count = %d, want 1", count)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM transactions`).Scan(&count); err != nil {
		t.Fatalf("count transactions: %v", err)
	}
	if count != 1 {
		t.Fatalf("transaction count = %d, want 1", count)
	}

	_, err = store.Remove(ctx, "METRO")
	var missing *merchant.NotFoundError
	if !errors.As(err, &missing) || !errors.Is(err, merchant.ErrNotFound) || missing.Requested != "METRO" {
		t.Fatalf("Remove(repeated) error = %v, want NotFoundError", err)
	}
}

func TestRemoveRollsBackOnDeleteFailure(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openStores(t)
	groceries := mustCreateCategory(t, ctx, categories, "Groceries")
	created, err := store.Set(ctx, "Metro", "Groceries")
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TRIGGER fail_merchant_delete BEFORE DELETE ON known_merchants
		BEGIN SELECT RAISE(ABORT, 'test boom'); END
	`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err = store.Remove(ctx, "Metro")
	if err == nil {
		t.Fatal("Remove() error = nil, want trigger failure")
	}
	var gotID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM known_merchants WHERE merchant = ?`, "Metro").Scan(&gotID); err != nil {
		t.Fatalf("load mapping after rollback: %v", err)
	}
	if gotID != created.KnownMerchant.ID || groceries.ID == 0 {
		t.Fatalf("mapping id after rollback = %d, want %d", gotID, created.KnownMerchant.ID)
	}
}

func insertMerchantTransaction(t *testing.T, ctx context.Context, db *sql.DB, merchantName string, categoryID int64) int64 {
	t.Helper()
	result, err := db.ExecContext(ctx, `
		INSERT INTO transactions (merchant, amount_hundredths, date, category_id)
		VALUES (?, ?, ?, ?)
	`, merchantName, 1250, "2026-08-14", categoryID)
	if err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("transaction LastInsertId: %v", err)
	}
	return id
}
