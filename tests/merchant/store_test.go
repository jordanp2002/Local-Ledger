package merchant_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/jordanp2002/Local-Ledger/internal/category"
	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/database"
	"github.com/jordanp2002/Local-Ledger/internal/merchant"
)

func TestSetCreateNoOpAndReplacePreserveIdentity(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openStores(t)
	groceries := mustCreateCategory(t, ctx, categories, "Groceries")
	dining := mustCreateCategory(t, ctx, categories, "Dining")

	created, err := store.Set(ctx, "  METRO  ", "groceries")
	if err != nil {
		t.Fatalf("Set(create): %v", err)
	}
	if !created.Created || created.PreviousCategory != nil {
		t.Fatalf("Set(create) = %#v, want created with no previous category", created)
	}
	if created.KnownMerchant.Merchant != "METRO" || created.KnownMerchant.Category != "Groceries" || !created.KnownMerchant.CategoryActive {
		t.Fatalf("Set(create) = %#v, want canonical METRO/Groceries", created)
	}
	if created.KnownMerchant.CategoryID != groceries.ID {
		t.Fatalf("Set(create) category id = %d, want %d", created.KnownMerchant.CategoryID, groceries.ID)
	}

	const frozenUpdatedAt = "2020-01-01T00:00:00.000Z"
	if _, err := db.ExecContext(ctx, `UPDATE known_merchants SET updated_at = ? WHERE id = ?`, frozenUpdatedAt, created.KnownMerchant.ID); err != nil {
		t.Fatalf("freeze updated_at: %v", err)
	}

	noOp, err := store.Set(ctx, "metro", "GROCERIES")
	if err != nil {
		t.Fatalf("Set(no-op): %v", err)
	}
	if noOp.Created || noOp.PreviousCategory != nil {
		t.Fatalf("Set(no-op) = %#v, want created=false previous_category=nil", noOp)
	}
	if noOp.KnownMerchant.ID != created.KnownMerchant.ID || noOp.KnownMerchant.Merchant != "METRO" || noOp.KnownMerchant.UpdatedAt != frozenUpdatedAt {
		t.Fatalf("Set(no-op) = %#v, want original identity and timestamp", noOp)
	}

	replaced, err := store.Set(ctx, "metro", "dining")
	if err != nil {
		t.Fatalf("Set(replace): %v", err)
	}
	if replaced.Created || replaced.PreviousCategory == nil || *replaced.PreviousCategory != "Groceries" {
		t.Fatalf("Set(replace) = %#v, want previous_category Groceries", replaced)
	}
	if replaced.KnownMerchant.ID != created.KnownMerchant.ID || replaced.KnownMerchant.Merchant != "METRO" || replaced.KnownMerchant.CategoryID != dining.ID {
		t.Fatalf("Set(replace) = %#v, want same mapping with Dining", replaced)
	}
	if replaced.KnownMerchant.CreatedAt != created.KnownMerchant.CreatedAt {
		t.Fatalf("Set(replace) created_at = %q, want %q", replaced.KnownMerchant.CreatedAt, created.KnownMerchant.CreatedAt)
	}
	if replaced.KnownMerchant.UpdatedAt == frozenUpdatedAt {
		t.Fatal("Set(replace) left updated_at unchanged")
	}
}

func TestSetValidatesBeforeCategoryLookupAndChecksInactiveBeforeNoOp(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openStores(t)
	health := mustCreateCategory(t, ctx, categories, "Health")
	mustCreateCategory(t, ctx, categories, "Groceries")

	created, err := store.Set(ctx, "Metro", "Health")
	if err != nil {
		t.Fatalf("Set(initial): %v", err)
	}
	const frozenUpdatedAt = "2020-01-01T00:00:00.000Z"
	if _, err := db.ExecContext(ctx, `UPDATE known_merchants SET updated_at = ? WHERE id = ?`, frozenUpdatedAt, created.KnownMerchant.ID); err != nil {
		t.Fatalf("freeze updated_at: %v", err)
	}
	if _, _, _, err := categories.Disable(ctx, "health"); err != nil {
		t.Fatalf("Disable(health): %v", err)
	}

	_, err = store.Set(ctx, "metro", "health")
	var inactiveErr *merchant.CategoryInactiveError
	if !errors.As(err, &inactiveErr) || !errors.Is(err, merchant.ErrCategoryInactive) {
		t.Fatalf("Set(inactive same mapping) error = %v, want CategoryInactiveError", err)
	}
	if inactiveErr.Category.ID != health.ID || inactiveErr.Category.Name != "Health" || inactiveErr.Category.Active {
		t.Fatalf("inactive error category = %#v, want canonical inactive Health", inactiveErr.Category)
	}
	var categoryID int64
	var updatedAt string
	if err := db.QueryRowContext(ctx, `SELECT category_id, updated_at FROM known_merchants WHERE id = ?`, created.KnownMerchant.ID).Scan(&categoryID, &updatedAt); err != nil {
		t.Fatalf("reload mapping: %v", err)
	}
	if categoryID != health.ID || updatedAt != frozenUpdatedAt {
		t.Fatalf("mapping after inactive no-op = (%d, %q), want unchanged (%d, %q)", categoryID, updatedAt, health.ID, frozenUpdatedAt)
	}

	_, err = store.Set(ctx, "", "missing")
	var validationErr *merchant.ValidationError
	if !errors.As(err, &validationErr) || len(validationErr.Fields) != 1 || validationErr.Fields[0] != (contract.FieldIssue{Field: "merchant", Reason: "must not be empty"}) {
		t.Fatalf("Set(invalid + missing) error = %#v, want merchant-only validation", err)
	}
}

func TestSetCanReplaceInactiveMapping(t *testing.T) {
	ctx := context.Background()
	store, categories, _ := openStores(t)
	mustCreateCategory(t, ctx, categories, "Health")
	groceries := mustCreateCategory(t, ctx, categories, "Groceries")
	created, err := store.Set(ctx, "Metro", "Health")
	if err != nil {
		t.Fatalf("Set(initial): %v", err)
	}
	if _, _, _, err := categories.Disable(ctx, "Health"); err != nil {
		t.Fatalf("Disable(Health): %v", err)
	}

	replaced, err := store.Set(ctx, "metro", "Groceries")
	if err != nil {
		t.Fatalf("Set(replace inactive): %v", err)
	}
	if replaced.PreviousCategory == nil || *replaced.PreviousCategory != "Health" {
		t.Fatalf("previous_category = %v, want Health", replaced.PreviousCategory)
	}
	if replaced.KnownMerchant.ID != created.KnownMerchant.ID || replaced.KnownMerchant.CategoryID != groceries.ID || !replaced.KnownMerchant.CategoryActive {
		t.Fatalf("replacement = %#v, want same mapping id and active Groceries", replaced)
	}
}

func TestSetUpdateFailurePreservesExistingMapping(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openStores(t)
	groceries := mustCreateCategory(t, ctx, categories, "Groceries")
	mustCreateCategory(t, ctx, categories, "Dining")
	created, err := store.Set(ctx, "Metro", "Groceries")
	if err != nil {
		t.Fatalf("Set(initial): %v", err)
	}
	const frozenUpdatedAt = "2020-01-01T00:00:00.000Z"
	if _, err := db.ExecContext(ctx, `UPDATE known_merchants SET updated_at = ? WHERE id = ?`, frozenUpdatedAt, created.KnownMerchant.ID); err != nil {
		t.Fatalf("freeze updated_at: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TRIGGER fail_merchant_update BEFORE UPDATE ON known_merchants
		BEGIN SELECT RAISE(ABORT, 'test boom'); END
	`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	if _, err := store.Set(ctx, "Metro", "Dining"); err == nil {
		t.Fatal("Set(replace) error = nil, want trigger failure")
	}
	var categoryID int64
	var updatedAt string
	if err := db.QueryRowContext(ctx, `SELECT category_id, updated_at FROM known_merchants WHERE id = ?`, created.KnownMerchant.ID).Scan(&categoryID, &updatedAt); err != nil {
		t.Fatalf("reload mapping: %v", err)
	}
	if categoryID != groceries.ID || updatedAt != frozenUpdatedAt {
		t.Fatalf("mapping after failed replace = (%d, %q), want (%d, %q)", categoryID, updatedAt, groceries.ID, frozenUpdatedAt)
	}
}

func TestListLiteralMerchantFilterPaginationAndInactiveVisibility(t *testing.T) {
	ctx := context.Background()
	store, categories, _ := openStores(t)
	mustCreateCategory(t, ctx, categories, "Alpha")
	for _, name := range []string{"banana", "Apple", "apple", "100% Off", "Shop_Mart", `Slash\Mart`} {
		if _, err := store.Set(ctx, name, "Alpha"); err != nil {
			t.Fatalf("Set(%q): %v", name, err)
		}
	}
	if _, _, _, err := categories.Disable(ctx, "Alpha"); err != nil {
		t.Fatalf("Disable(Alpha): %v", err)
	}

	all, issues, err := store.List(ctx, merchant.ListOptions{})
	if err != nil || len(issues) != 0 {
		t.Fatalf("List(all) = (%#v, %#v, %v), want success", all, issues, err)
	}
	if all.KnownMerchants == nil || len(all.KnownMerchants) != 5 {
		t.Fatalf("List(all) known_merchants = %#v, want five non-nil rows", all.KnownMerchants)
	}
	if all.Page.Limit != merchant.DefaultLimit || all.Page.Offset != 0 || all.Page.Total != 5 || all.Page.Returned != 5 || all.Page.HasMore {
		t.Fatalf("List(all) page = %#v, want effective default page", all.Page)
	}
	if all.KnownMerchants[0].Merchant != "100% Off" || all.KnownMerchants[1].Merchant != "Apple" || all.KnownMerchants[4].Merchant != `Slash\Mart` {
		t.Fatalf("List(all) ordering = %#v", all.KnownMerchants)
	}
	if all.KnownMerchants[0].CategoryActive {
		t.Fatal("List(all) hid inactive category state")
	}

	query := " % "
	wildcard, issues, err := store.List(ctx, merchant.ListOptions{Query: query})
	if err != nil || len(issues) != 0 || len(wildcard.KnownMerchants) != 1 || wildcard.KnownMerchants[0].Merchant != "100% Off" {
		t.Fatalf("List(literal %q) = (%#v, %#v, %v), want only 100%% Off", query, wildcard, issues, err)
	}
	underscore, issues, err := store.List(ctx, merchant.ListOptions{Query: "_"})
	if err != nil || len(issues) != 0 || len(underscore.KnownMerchants) != 1 || underscore.KnownMerchants[0].Merchant != "Shop_Mart" {
		t.Fatalf("List(literal _) = (%#v, %#v, %v), want only Shop_Mart", underscore, issues, err)
	}
	backslash, issues, err := store.List(ctx, merchant.ListOptions{Query: `\`})
	if err != nil || len(issues) != 0 || len(backslash.KnownMerchants) != 1 || backslash.KnownMerchants[0].Merchant != `Slash\Mart` {
		t.Fatalf("List(literal backslash) = (%#v, %#v, %v), want only Slash\\Mart", backslash, issues, err)
	}

	categoryQuery, issues, err := store.List(ctx, merchant.ListOptions{Query: "alpha"})
	if err != nil || len(issues) != 0 || len(categoryQuery.KnownMerchants) != 0 || categoryQuery.Page.Total != 0 {
		t.Fatalf("List(category-only query) = (%#v, %#v, %v), want no merchant rows", categoryQuery, issues, err)
	}

	limit := int64(2)
	offset := int64(2)
	page, issues, err := store.List(ctx, merchant.ListOptions{Limit: &limit, Offset: &offset})
	if err != nil || len(issues) != 0 || page.Page.Total != 5 || page.Page.Returned != 2 || !page.Page.HasMore || page.Page.Limit != limit || page.Page.Offset != offset {
		t.Fatalf("List(page) = (%#v, %#v, %v), want filtered metadata", page, issues, err)
	}

	beyond := int64(99)
	page, issues, err = store.List(ctx, merchant.ListOptions{Limit: &limit, Offset: &beyond})
	if err != nil || len(issues) != 0 || page.KnownMerchants == nil || len(page.KnownMerchants) != 0 || page.Page.Total != 5 || page.Page.Returned != 0 || page.Page.HasMore {
		t.Fatalf("List(beyond) = (%#v, %#v, %v), want empty page with total five", page, issues, err)
	}
	encoded, err := json.Marshal(page.KnownMerchants)
	if err != nil || string(encoded) != "[]" {
		t.Fatalf("empty page JSON = %s, %v; want []", encoded, err)
	}

	filteredLimit := int64(1)
	filtered, issues, err := store.List(ctx, merchant.ListOptions{Query: "a", Limit: &filteredLimit})
	if err != nil || len(issues) != 0 || filtered.Page.Total != 4 || filtered.Page.Returned != 1 || !filtered.Page.HasMore {
		t.Fatalf("List(filtered page) = (%#v, %#v, %v), want one of four with has_more", filtered, issues, err)
	}
}

func TestListCollectsValidationIssuesInRequestOrder(t *testing.T) {
	store, _, _ := openStores(t)
	limit := int64(0)
	offset := int64(-1)
	_, issues, err := store.List(context.Background(), merchant.ListOptions{Query: "bad\x00query", Limit: &limit, Offset: &offset})
	if err != nil {
		t.Fatalf("List(invalid) error = %v, want nil with field issues", err)
	}
	want := []contract.FieldIssue{
		{Field: "query", Reason: "must not contain NUL characters"},
		{Field: "limit", Reason: "must be between 1 and 200"},
		{Field: "offset", Reason: "must be zero or greater"},
	}
	if len(issues) != len(want) {
		t.Fatalf("List(invalid) issues = %#v, want %#v", issues, want)
	}
	for i := range want {
		if issues[i] != want[i] {
			t.Fatalf("List(invalid) issue[%d] = %#v, want %#v", i, issues[i], want[i])
		}
	}
}

func TestListPaginationBoundaries(t *testing.T) {
	store, _, _ := openStores(t)
	ctx := context.Background()
	zero := int64(0)
	one := int64(1)
	max := int64(200)

	for _, options := range []merchant.ListOptions{
		{Limit: &one},
		{Limit: &max},
		{Offset: &zero},
	} {
		if _, issues, err := store.List(ctx, options); err != nil || len(issues) != 0 {
			t.Fatalf("List(valid %#v) = issues %#v, error %v", options, issues, err)
		}
	}

	negative := int64(-1)
	tooLarge := int64(201)
	for _, tc := range []struct {
		options merchant.ListOptions
		field   string
	}{
		{options: merchant.ListOptions{Limit: &zero}, field: "limit"},
		{options: merchant.ListOptions{Limit: &negative}, field: "limit"},
		{options: merchant.ListOptions{Limit: &tooLarge}, field: "limit"},
		{options: merchant.ListOptions{Offset: &negative}, field: "offset"},
	} {
		_, issues, err := store.List(ctx, tc.options)
		if err != nil || len(issues) != 1 || issues[0].Field != tc.field {
			t.Fatalf("List(invalid %#v) = issues %#v, error %v; want %s issue", tc.options, issues, err, tc.field)
		}
	}
}

func openStores(t *testing.T) (*merchant.Store, *category.Store, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "finance.db")
	db, err := database.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			t.Errorf("close database: %v", err)
		}
	})
	return &merchant.Store{DB: db}, &category.Store{DB: db}, db
}

func mustCreateCategory(t *testing.T, ctx context.Context, store *category.Store, name string) contract.Category {
	t.Helper()
	cat, created, reactivated, err := store.Create(ctx, name)
	if err != nil {
		t.Fatalf("Create(%q): %v", name, err)
	}
	if !created || reactivated {
		t.Fatalf("Create(%q) created=%v reactivated=%v, want created=true reactivated=false", name, created, reactivated)
	}
	return cat
}
