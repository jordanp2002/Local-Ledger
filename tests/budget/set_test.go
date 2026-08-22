package budget_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/budget"
	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

const frozenSetTimestamp = "2020-01-01T00:00:00.000Z"

func TestSetReplacesExistingCategoryAndPreservesIdentity(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	insertBudget(t, ctx, db, groceries.ID, "2026-08", "100.00")
	setBudgetTimestamps(t, ctx, db, "2026-08", frozenSetTimestamp)
	before := storedBudgetByCategory(t, listStoredBudgets(t, ctx, db, "2026-08"), groceries.ID)

	result, fields, err := store.Set(ctx, "2026-08", []budget.Allocation{
		{Category: " groceries ", Amount: "300"},
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Set() = %#v fields %#v error %v", result, fields, err)
	}
	if result.Month != "2026-08" {
		t.Fatalf("Set() month = %q, want 2026-08", result.Month)
	}
	if result.Changes == nil || len(result.Changes) != 1 {
		t.Fatalf("Set() changes = %#v, want one non-nil change", result.Changes)
	}
	change := result.Changes[0]
	if change.Created {
		t.Fatal("Set() created = true, want false for replace")
	}
	if change.Budget.ID != before.ID || change.Budget.CategoryID != groceries.ID || change.Budget.Category != "Groceries" {
		t.Fatalf("replaced identity = %#v, want id=%d category=Groceries", change.Budget, before.ID)
	}
	if change.Budget.Month != "2026-08" || change.Budget.Amount != "300.00" {
		t.Fatalf("replaced month/amount = (%q, %q), want (2026-08, 300.00)", change.Budget.Month, change.Budget.Amount)
	}
	if change.Budget.CreatedAt != frozenSetTimestamp {
		t.Fatalf("created_at = %q, want preserved %q", change.Budget.CreatedAt, frozenSetTimestamp)
	}
	if change.Budget.UpdatedAt == "" || change.Budget.UpdatedAt == frozenSetTimestamp {
		t.Fatalf("updated_at = %q, want advanced from %q", change.Budget.UpdatedAt, frozenSetTimestamp)
	}

	after := storedBudgetByCategory(t, listStoredBudgets(t, ctx, db, "2026-08"), groceries.ID)
	if after.ID != before.ID || after.CategoryID != before.CategoryID || after.CreatedAt != before.CreatedAt {
		t.Fatalf("stored identity = %#v, want preserved %#v", after, before)
	}
	if after.AmountHundredths != 30000 {
		t.Fatalf("stored amount_hundredths = %d, want 30000", after.AmountHundredths)
	}
	if after.UpdatedAt == before.UpdatedAt {
		t.Fatal("stored updated_at did not advance")
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 1 {
		t.Fatalf("August rows = %d, want 1", got)
	}
}

func TestSetInsertsMissingActiveCategory(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	dining := createCategory(t, ctx, categories, "Dining")
	insertBudget(t, ctx, db, groceries.ID, "2026-08", "100.00")
	setBudgetTimestamps(t, ctx, db, "2026-08", frozenSetTimestamp)
	groceriesBefore := storedBudgetByCategory(t, listStoredBudgets(t, ctx, db, "2026-08"), groceries.ID)

	result, fields, err := store.Set(ctx, "2026-08", []budget.Allocation{
		{Category: " dining ", Amount: "75"},
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Set() = %#v fields %#v error %v", result, fields, err)
	}
	if result.Changes == nil || len(result.Changes) != 1 {
		t.Fatalf("Set() changes = %#v, want one non-nil change", result.Changes)
	}
	change := result.Changes[0]
	if !change.Created {
		t.Fatal("Set() created = false, want true for insert")
	}
	if change.Budget.ID == groceriesBefore.ID || change.Budget.CategoryID != dining.ID || change.Budget.Category != "Dining" {
		t.Fatalf("inserted row = %#v, want new Dining id", change.Budget)
	}
	if change.Budget.Month != "2026-08" || change.Budget.Amount != "75.00" {
		t.Fatalf("inserted month/amount = (%q, %q), want (2026-08, 75.00)", change.Budget.Month, change.Budget.Amount)
	}
	if change.Budget.CreatedAt == "" || change.Budget.UpdatedAt == "" {
		t.Fatalf("inserted timestamps = %#v, want schema defaults", change.Budget)
	}

	if !reflect.DeepEqual(storedBudgetByCategory(t, listStoredBudgets(t, ctx, db, "2026-08"), groceries.ID), groceriesBefore) {
		t.Fatal("unchanged Groceries row was rewritten")
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 2 {
		t.Fatalf("August rows = %d, want 2", got)
	}
}

func TestSetReplacesAndInsertsInOneCommit(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	dining := createCategory(t, ctx, categories, "Dining")
	insertBudget(t, ctx, db, groceries.ID, "2026-08", "100.00")
	setBudgetTimestamps(t, ctx, db, "2026-08", frozenSetTimestamp)
	groceriesBefore := storedBudgetByCategory(t, listStoredBudgets(t, ctx, db, "2026-08"), groceries.ID)

	result, fields, err := store.Set(ctx, "2026-08", []budget.Allocation{
		{Category: "GROCERIES", Amount: "200.00"},
		{Category: "Dining", Amount: "50.00"},
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Set() = %#v fields %#v error %v", result, fields, err)
	}
	if result.Changes == nil || len(result.Changes) != 2 {
		t.Fatalf("Set() changes = %#v, want two non-nil changes", result.Changes)
	}
	if result.Changes[0].Budget.Category != "Dining" || !result.Changes[0].Created {
		t.Fatalf("first change = %#v, want inserted Dining", result.Changes[0])
	}
	if result.Changes[1].Budget.Category != "Groceries" || result.Changes[1].Created {
		t.Fatalf("second change = %#v, want replaced Groceries", result.Changes[1])
	}
	if result.Changes[1].Budget.ID != groceriesBefore.ID || result.Changes[1].Budget.Amount != "200.00" {
		t.Fatalf("replaced Groceries = %#v, want id=%d amount 200.00", result.Changes[1].Budget, groceriesBefore.ID)
	}
	if result.Changes[0].Budget.CategoryID != dining.ID || result.Changes[0].Budget.Amount != "50.00" {
		t.Fatalf("inserted Dining = %#v, want category %d amount 50.00", result.Changes[0].Budget, dining.ID)
	}

	stored := listStoredBudgets(t, ctx, db, "2026-08")
	if len(stored) != 2 {
		t.Fatalf("stored August rows = %#v, want 2", stored)
	}
	afterGroceries := storedBudgetByCategory(t, stored, groceries.ID)
	if afterGroceries.ID != groceriesBefore.ID || afterGroceries.CreatedAt != groceriesBefore.CreatedAt {
		t.Fatalf("replaced Groceries identity = %#v, want %#v", afterGroceries, groceriesBefore)
	}
	if afterGroceries.AmountHundredths != 20000 {
		t.Fatalf("replaced Groceries amount = %d, want 20000", afterGroceries.AmountHundredths)
	}
	if storedBudgetByCategory(t, stored, dining.ID).AmountHundredths != 5000 {
		t.Fatalf("inserted Dining amount = %#v, want 5000", stored)
	}
}

func TestSetSameAmountReplacementAdvancesUpdatedAt(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	insertBudget(t, ctx, db, groceries.ID, "2026-08", "100.00")
	setBudgetTimestamps(t, ctx, db, "2026-08", frozenSetTimestamp)
	before := storedBudgetByCategory(t, listStoredBudgets(t, ctx, db, "2026-08"), groceries.ID)

	result, fields, err := store.Set(ctx, "2026-08", []budget.Allocation{
		{Category: "Groceries", Amount: "100.00"},
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Set() = %#v fields %#v error %v", result, fields, err)
	}
	if result.Changes == nil || len(result.Changes) != 1 || result.Changes[0].Created {
		t.Fatalf("Set() changes = %#v, want one created=false change", result.Changes)
	}
	if result.Changes[0].Budget.ID != before.ID || result.Changes[0].Budget.Amount != "100.00" {
		t.Fatalf("same-amount row = %#v, want id=%d amount 100.00", result.Changes[0].Budget, before.ID)
	}
	if result.Changes[0].Budget.CreatedAt != frozenSetTimestamp {
		t.Fatalf("created_at = %q, want preserved %q", result.Changes[0].Budget.CreatedAt, frozenSetTimestamp)
	}
	if result.Changes[0].Budget.UpdatedAt == frozenSetTimestamp {
		t.Fatal("same-amount replacement left updated_at unchanged")
	}

	after := storedBudgetByCategory(t, listStoredBudgets(t, ctx, db, "2026-08"), groceries.ID)
	if after.AmountHundredths != before.AmountHundredths || after.CreatedAt != before.CreatedAt || after.ID != before.ID {
		t.Fatalf("same-amount stored row = %#v, want identity and amount from %#v", after, before)
	}
	if after.UpdatedAt == before.UpdatedAt {
		t.Fatal("stored updated_at did not advance on same-amount replacement")
	}
}

func TestSetAcceptsZeroAllocationsAndNormalizes(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	zeroA := createCategory(t, ctx, categories, "ZeroA")
	zeroB := createCategory(t, ctx, categories, "ZeroB")
	zeroC := createCategory(t, ctx, categories, "ZeroC")
	insertBudget(t, ctx, db, zeroA.ID, "2026-08", "10.00")

	result, fields, err := store.Set(ctx, "2026-08", []budget.Allocation{
		{Category: zeroA.Name, Amount: "0"},
		{Category: zeroB.Name, Amount: "0.0"},
		{Category: zeroC.Name, Amount: "0.00"},
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Set() = %#v fields %#v error %v", result, fields, err)
	}
	if result.Changes == nil || len(result.Changes) != 3 {
		t.Fatalf("Set() changes = %#v, want three non-nil changes", result.Changes)
	}
	for _, change := range result.Changes {
		if change.Budget.Amount != "0.00" {
			t.Fatalf("amount = %q, want 0.00", change.Budget.Amount)
		}
	}
	if result.Changes[0].Created || !result.Changes[1].Created || !result.Changes[2].Created {
		t.Fatalf("created flags = %#v, want replace then two inserts", result.Changes)
	}
	if got := budgetAmounts(t, ctx, db, "2026-08"); !reflect.DeepEqual(got, []int64{0, 0, 0}) {
		t.Fatalf("stored amount_hundredths = %#v, want three zeros", got)
	}
}

func TestSetMissingMonthIsNotFoundAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")

	_, fields, err := store.Set(ctx, "2026-08", []budget.Allocation{
		{Category: "Groceries", Amount: "1.00"},
	})
	if len(fields) != 0 {
		t.Fatalf("Set() fields = %#v, want none", fields)
	}
	var notFound *budget.NotFoundError
	if !errors.As(err, &notFound) || notFound.Month != "2026-08" {
		t.Fatalf("Set() error = %#v, want NotFoundError month 2026-08", err)
	}
	if notFound.LatestEarlierMonth != nil {
		t.Fatalf("LatestEarlierMonth = %#v, want nil", notFound.LatestEarlierMonth)
	}
	if !errors.Is(err, budget.ErrNotFound) || !errors.Is(err, budget.ErrMonthlyBudgetNotFound) {
		t.Fatalf("NotFoundError should wrap ErrNotFound: %v", err)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
		t.Fatalf("August rows after missing month = %d, want 0", got)
	}
}

func TestSetLatestEarlierMonthIsLatestEarlierSnapshot(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	january := createCategory(t, ctx, categories, "JanuaryOnly")
	march := createCategory(t, ctx, categories, "MarchOnly")
	insertBudget(t, ctx, db, january.ID, "2026-01", "10.00")
	insertBudget(t, ctx, db, march.ID, "2026-03", "25.00")
	januaryBefore := listStoredBudgets(t, ctx, db, "2026-01")
	marchBefore := listStoredBudgets(t, ctx, db, "2026-03")

	_, fields, err := store.Set(ctx, "2026-08", []budget.Allocation{
		{Category: march.Name, Amount: "1.00"},
	})
	if len(fields) != 0 {
		t.Fatalf("Set() fields = %#v, want none", fields)
	}
	var notFound *budget.NotFoundError
	if !errors.As(err, &notFound) || notFound.Month != "2026-08" {
		t.Fatalf("Set() error = %#v, want NotFoundError month 2026-08", err)
	}
	if notFound.LatestEarlierMonth == nil || *notFound.LatestEarlierMonth != "2026-03" {
		t.Fatalf("LatestEarlierMonth = %#v, want 2026-03", notFound.LatestEarlierMonth)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
		t.Fatalf("August rows after missing month = %d, want 0", got)
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-01"), januaryBefore) {
		t.Fatal("January changed after missing-month Set")
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-03"), marchBefore) {
		t.Fatal("March changed after missing-month Set")
	}
}

func TestSetMissingSnapshotPrecedesCategoryStateErrors(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	existing := createCategory(t, ctx, categories, "Existing")
	inactive := createCategory(t, ctx, categories, "Dining")
	insertBudget(t, ctx, db, existing.ID, "2026-07", "10.00")
	if _, changed, _, err := categories.Disable(ctx, inactive.Name); err != nil || !changed {
		t.Fatalf("Disable(%q) = changed %v, error %v", inactive.Name, changed, err)
	}

	_, fields, err := store.Set(ctx, "2026-08", []budget.Allocation{
		{Category: "Missing", Amount: "1.00"},
	})
	if len(fields) != 0 {
		t.Fatalf("Set() fields = %#v, want none", fields)
	}
	var notFound *budget.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("Set() error = %v, want *NotFoundError before category_not_found", err)
	}
	if notFound.LatestEarlierMonth == nil || *notFound.LatestEarlierMonth != "2026-07" {
		t.Fatalf("LatestEarlierMonth = %#v, want 2026-07", notFound.LatestEarlierMonth)
	}
	var missing *budget.CategoryNotFoundError
	if errors.As(err, &missing) {
		t.Fatalf("Set() error = %v, want missing snapshot before category_not_found", err)
	}

	_, fields, err = store.Set(ctx, "2026-08", []budget.Allocation{
		{Category: "dining", Amount: "1.00"},
	})
	if len(fields) != 0 {
		t.Fatalf("inactive-on-empty fields = %#v, want none", fields)
	}
	if !errors.As(err, &notFound) {
		t.Fatalf("inactive-on-empty error = %v, want *NotFoundError before category_inactive", err)
	}
	var inactiveErr *budget.CategoryInactiveError
	if errors.As(err, &inactiveErr) {
		t.Fatalf("inactive-on-empty error = %v, want missing snapshot before category_inactive", err)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
		t.Fatalf("August rows after precedence check = %d, want 0", got)
	}
}

func TestSetCategoryErrorsWriteNothingAndKeepRecoveryContext(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	alpha := createCategory(t, ctx, categories, "Alpha")
	inactive := createCategory(t, ctx, categories, "Dining")
	beta := createCategory(t, ctx, categories, "beta")
	insertBudget(t, ctx, db, alpha.ID, "2026-08", "10.00")
	setBudgetTimestamps(t, ctx, db, "2026-08", frozenSetTimestamp)
	before := listStoredBudgets(t, ctx, db, "2026-08")
	if _, changed, _, err := categories.Disable(ctx, inactive.Name); err != nil || !changed {
		t.Fatalf("Disable(%q) = changed %v, error %v", inactive.Name, changed, err)
	}

	_, fields, err := store.Set(ctx, "2026-08", []budget.Allocation{
		{Category: " Pharmacy ", Amount: "1.00"},
		{Category: "dining", Amount: "2.00"},
	})
	if len(fields) != 0 {
		t.Fatalf("missing-category fields = %#v, want none", fields)
	}
	var missing *budget.CategoryNotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("missing category error = %v, want *CategoryNotFoundError", err)
	}
	if missing.Requested != "Pharmacy" || missing.ActiveCategories == nil || len(missing.ActiveCategories) != 2 {
		t.Fatalf("missing recovery = %#v, want trimmed request and two active categories", missing)
	}
	if missing.ActiveCategories[0].Name != alpha.Name || missing.ActiveCategories[1].Name != beta.Name {
		t.Fatalf("missing active recovery order = %#v, want Alpha, beta", missing.ActiveCategories)
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-08"), before) {
		t.Fatal("August snapshot changed after missing category")
	}

	_, fields, err = store.Set(ctx, "2026-08", []budget.Allocation{
		{Category: "dining", Amount: "2.00"},
		{Category: "Missing", Amount: "1.00"},
	})
	if len(fields) != 0 {
		t.Fatalf("inactive-category fields = %#v, want none", fields)
	}
	var inactiveErr *budget.CategoryInactiveError
	if !errors.As(err, &inactiveErr) {
		t.Fatalf("inactive category error = %v, want *CategoryInactiveError", err)
	}
	if inactiveErr.Category.ID != inactive.ID || inactiveErr.Category.Name != inactive.Name || inactiveErr.Category.Active {
		t.Fatalf("inactive recovery = %#v, want canonical inactive Dining", inactiveErr)
	}
	if inactiveErr.ActiveCategories == nil || len(inactiveErr.ActiveCategories) != 2 {
		t.Fatalf("inactive active list = %#v, want two categories", inactiveErr.ActiveCategories)
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-08"), before) {
		t.Fatal("August snapshot changed after inactive category")
	}
}

func TestSetSemanticFailuresWriteNothing(t *testing.T) {
	ctx := context.Background()
	now := torontoTime(t, 2026, 8, 15, 12, 0)

	t.Run("empty allocations", func(t *testing.T) {
		store, categories, db := openBudgetStore(t, now)
		existing := createCategory(t, ctx, categories, "Existing")
		insertBudget(t, ctx, db, existing.ID, "2026-08", "10.00")
		before := listStoredBudgets(t, ctx, db, "2026-08")

		_, fields, err := store.Set(ctx, "2026-08", nil)
		if err != nil {
			t.Fatalf("Set(nil) error = %v, want semantic issue", err)
		}
		want := []contract.FieldIssue{{Field: "budgets", Reason: "must contain at least one allocation"}}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("nil allocations fields = %#v, want %#v", fields, want)
		}

		_, fields, err = store.Set(ctx, "2026-08", []budget.Allocation{})
		if err != nil {
			t.Fatalf("Set(empty) error = %v, want semantic issue", err)
		}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("empty allocations fields = %#v, want %#v", fields, want)
		}
		if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-08"), before) {
			t.Fatal("August snapshot changed after empty allocations")
		}
	})

	t.Run("invalid month and allocations", func(t *testing.T) {
		store, _, db := openBudgetStore(t, now)
		_, fields, err := store.Set(ctx, "not-a-month", []budget.Allocation{
			{Category: " \t", Amount: "-1"},
			{Category: "Food\x00", Amount: "1.234"},
		})
		if err != nil {
			t.Fatalf("Set() error = %v, want semantic issues", err)
		}
		want := []contract.FieldIssue{
			{Field: "month", Reason: "must be a valid YYYY-MM month"},
			{Field: "budgets[0].category", Reason: "must not be empty"},
			{Field: "budgets[0].amount", Reason: "must be a non-negative amount with at most two decimal places"},
			{Field: "budgets[1].category", Reason: "must not contain NUL characters"},
			{Field: "budgets[1].amount", Reason: "must be a non-negative amount with at most two decimal places"},
		}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("validation fields = %#v, want %#v", fields, want)
		}
		if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
			t.Fatalf("budget rows after validation failure = %d, want 0", got)
		}
	})

	t.Run("empty allocations after invalid month", func(t *testing.T) {
		store, _, db := openBudgetStore(t, now)
		_, fields, err := store.Set(ctx, "not-a-month", nil)
		if err != nil {
			t.Fatalf("Set() error = %v, want semantic issues", err)
		}
		want := []contract.FieldIssue{
			{Field: "month", Reason: "must be a valid YYYY-MM month"},
			{Field: "budgets", Reason: "must contain at least one allocation"},
		}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("empty+month fields = %#v, want %#v", fields, want)
		}
		if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
			t.Fatalf("budget rows after empty+month failure = %d, want 0", got)
		}
	})

	t.Run("duplicate categories", func(t *testing.T) {
		store, categories, db := openBudgetStore(t, now)
		existing := createCategory(t, ctx, categories, "Existing")
		insertBudget(t, ctx, db, existing.ID, "2026-08", "10.00")
		before := listStoredBudgets(t, ctx, db, "2026-08")

		_, fields, err := store.Set(ctx, "2026-08", []budget.Allocation{
			{Category: "Groceries", Amount: "1"},
			{Category: "gROCERIES", Amount: "2"},
			{Category: "É", Amount: "3"},
			{Category: "é", Amount: "4"},
		})
		if err != nil {
			t.Fatalf("Set() error = %v, want semantic issues", err)
		}
		want := []contract.FieldIssue{{Field: "budgets[1].category", Reason: "must not repeat a category"}}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("duplicate fields = %#v, want %#v", fields, want)
		}
		if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-08"), before) {
			t.Fatal("August snapshot changed after duplicate categories")
		}
	})

	t.Run("invalid amounts", func(t *testing.T) {
		invalidAmounts := []string{"", " ", "-1", "+1", "1e2", " 1.00", "1,00", "1.234", "92233720368547758.08"}
		for _, amount := range invalidAmounts {
			store, categories, db := openBudgetStore(t, now)
			existing := createCategory(t, ctx, categories, "Existing")
			insertBudget(t, ctx, db, existing.ID, "2026-08", "10.00")
			before := listStoredBudgets(t, ctx, db, "2026-08")

			_, fields, err := store.Set(ctx, "2026-08", []budget.Allocation{
				{Category: "Groceries", Amount: amount},
			})
			if err != nil {
				t.Fatalf("amount %q error = %v, want semantic issue", amount, err)
			}
			want := []contract.FieldIssue{{
				Field:  "budgets[0].amount",
				Reason: "must be a non-negative amount with at most two decimal places",
			}}
			if !reflect.DeepEqual(fields, want) {
				t.Fatalf("amount %q fields = %#v, want %#v", amount, fields, want)
			}
			if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-08"), before) {
				t.Fatalf("August snapshot changed after invalid amount %q", amount)
			}
		}
	})

	t.Run("request total overflow", func(t *testing.T) {
		store, categories, db := openBudgetStore(t, now)
		existing := createCategory(t, ctx, categories, "Existing")
		insertBudget(t, ctx, db, existing.ID, "2026-08", "10.00")
		before := listStoredBudgets(t, ctx, db, "2026-08")

		_, fields, err := store.Set(ctx, "2026-08", []budget.Allocation{
			{Category: "One", Amount: "92233720368547758.07"},
			{Category: "Two", Amount: "92233720368547758.07"},
		})
		if err != nil {
			t.Fatalf("Set() error = %v, want semantic issue", err)
		}
		want := []contract.FieldIssue{{Field: "budgets", Reason: "total must fit the supported amount range"}}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("overflow fields = %#v, want %#v", fields, want)
		}
		if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-08"), before) {
			t.Fatal("August snapshot changed after request-total overflow")
		}
	})
}

func TestSetAfterDisablingLastCurrentMonthRowIsNotFound(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	dining := createCategory(t, ctx, categories, "Dining")
	insertBudget(t, ctx, db, groceries.ID, "2026-07", "80.00")
	insertBudget(t, ctx, db, groceries.ID, "2026-08", "100.00")
	julyBefore := listStoredBudgets(t, ctx, db, "2026-07")

	if _, changed, removed, err := categories.Disable(ctx, groceries.Name); err != nil || !changed || removed == nil {
		t.Fatalf("Disable(%q) = changed %v removed %#v error %v", groceries.Name, changed, removed, err)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
		t.Fatalf("August rows after disable = %d, want 0", got)
	}

	_, fields, err := store.Set(ctx, "2026-08", []budget.Allocation{
		{Category: dining.Name, Amount: "25.00"},
	})
	if len(fields) != 0 {
		t.Fatalf("Set() fields = %#v, want none", fields)
	}
	var notFound *budget.NotFoundError
	if !errors.As(err, &notFound) || notFound.Month != "2026-08" {
		t.Fatalf("Set() error = %#v, want NotFoundError month 2026-08", err)
	}
	if notFound.LatestEarlierMonth == nil || *notFound.LatestEarlierMonth != "2026-07" {
		t.Fatalf("LatestEarlierMonth = %#v, want 2026-07", notFound.LatestEarlierMonth)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
		t.Fatalf("August rows after Set = %d, want 0", got)
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-07"), julyBefore) {
		t.Fatal("July changed after disable-then-Set")
	}
}

func TestSetRejectsResultingMonthTotalOverflowBeforeCommit(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	one := createCategory(t, ctx, categories, "One")
	two := createCategory(t, ctx, categories, "Two")
	insertBudget(t, ctx, db, one.ID, "2026-08", "92233720368547758.07")
	setBudgetTimestamps(t, ctx, db, "2026-08", frozenSetTimestamp)
	before := listStoredBudgets(t, ctx, db, "2026-08")

	_, fields, err := store.Set(ctx, "2026-08", []budget.Allocation{
		{Category: two.Name, Amount: "0.01"},
	})
	if err != nil {
		t.Fatalf("Set() error = %v, want merged overflow fields", err)
	}
	want := []contract.FieldIssue{{Field: "budgets", Reason: "total must fit the supported amount range"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("merged overflow fields = %#v, want %#v", fields, want)
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-08"), before) {
		t.Fatal("August snapshot changed after merged overflow")
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 1 {
		t.Fatalf("August rows after merged overflow = %d, want 1", got)
	}
}

func TestSetLeavesOtherMonthsUnchanged(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	health := createCategory(t, ctx, categories, "Health")
	insertBudget(t, ctx, db, groceries.ID, "2026-07", "10.00")
	insertBudget(t, ctx, db, health.ID, "2026-09", "20.00")
	insertBudget(t, ctx, db, groceries.ID, "2026-08", "30.00")
	julyBefore := listStoredBudgets(t, ctx, db, "2026-07")
	septemberBefore := listStoredBudgets(t, ctx, db, "2026-09")

	result, fields, err := store.Set(ctx, "2026-08", []budget.Allocation{
		{Category: health.Name, Amount: "40.00"},
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Set() = %#v fields %#v error %v", result, fields, err)
	}
	if result.Changes == nil || len(result.Changes) != 1 || result.Changes[0].Budget.Category != "Health" {
		t.Fatalf("Set() changes = %#v, want inserted Health", result.Changes)
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-07"), julyBefore) {
		t.Fatal("July changed after August Set")
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-09"), septemberBefore) {
		t.Fatal("September changed after August Set")
	}
}

func TestSetRollsBackAllWritesOnFailure(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	first := createCategory(t, ctx, categories, "First")
	second := createCategory(t, ctx, categories, "Second")
	insertBudget(t, ctx, db, first.ID, "2026-08", "1.00")
	setBudgetTimestamps(t, ctx, db, "2026-08", frozenSetTimestamp)
	before := listStoredBudgets(t, ctx, db, "2026-08")
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TRIGGER fail_first_budget_update
		BEFORE UPDATE ON budgets
		WHEN OLD.category_id = %d AND OLD.month = '2026-08'
		BEGIN
			SELECT RAISE(ABORT, 'test budget update failure');
		END
	`, first.ID)); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	_, fields, err := store.Set(ctx, "2026-08", []budget.Allocation{
		{Category: first.Name, Amount: "9.00"},
		{Category: second.Name, Amount: "2.00"},
	})
	if len(fields) != 0 {
		t.Fatalf("rollback fields = %#v, want none", fields)
	}
	if err == nil {
		t.Fatal("Set() error = nil, want trigger failure")
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-08"), before) {
		t.Fatal("August snapshot changed after rollback")
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 1 {
		t.Fatalf("August rows after rollback = %d, want 1", got)
	}
}

func TestSetChangesOrderedByCategoryNoCaseThenID(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	banana := createCategory(t, ctx, categories, "banana")
	apple := createCategory(t, ctx, categories, "Apple")
	dining := createCategory(t, ctx, categories, "Dining")
	keep := createCategory(t, ctx, categories, "Keep")
	insertBudget(t, ctx, db, keep.ID, "2026-08", "1.00")

	result, fields, err := store.Set(ctx, "2026-08", []budget.Allocation{
		{Category: banana.Name, Amount: "1.5"},
		{Category: dining.Name, Amount: "0"},
		{Category: apple.Name, Amount: "500"},
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Set() = %#v fields %#v error %v", result, fields, err)
	}
	if result.Changes == nil {
		t.Fatal("Set() changes = nil, want non-nil slice")
	}
	if len(result.Changes) != 3 {
		t.Fatalf("Set() changes = %#v, want three rows", result.Changes)
	}
	gotNames := []string{result.Changes[0].Budget.Category, result.Changes[1].Budget.Category, result.Changes[2].Budget.Category}
	gotAmounts := []string{result.Changes[0].Budget.Amount, result.Changes[1].Budget.Amount, result.Changes[2].Budget.Amount}
	if !reflect.DeepEqual(gotNames, []string{"Apple", "banana", "Dining"}) {
		t.Fatalf("change category order = %#v, want Apple, banana, Dining", gotNames)
	}
	if !reflect.DeepEqual(gotAmounts, []string{"500.00", "1.50", "0.00"}) {
		t.Fatalf("change amounts = %#v, want normalized values", gotAmounts)
	}
	if result.Changes[0].Budget.CategoryID != apple.ID || result.Changes[1].Budget.CategoryID != banana.ID || result.Changes[2].Budget.CategoryID != dining.ID {
		t.Fatalf("change category IDs = %#v, want canonical IDs", result.Changes)
	}
	if !result.Changes[0].Created || !result.Changes[1].Created || !result.Changes[2].Created {
		t.Fatalf("created flags = %#v, want all inserts", result.Changes)
	}
	if result.Changes[0].Budget.ID > result.Changes[1].Budget.ID && result.Changes[0].Budget.Category == result.Changes[1].Budget.Category {
		t.Fatalf("tie-break order = %#v, want budget ID ascending for equal names", result.Changes)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 4 {
		t.Fatalf("August rows = %d, want 4 including unchanged Keep", got)
	}
}

func TestSetChangesSliceNeverNil(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	insertBudget(t, ctx, db, groceries.ID, "2026-08", "10.00")

	result, fields, err := store.Set(ctx, "2026-08", []budget.Allocation{
		{Category: groceries.Name, Amount: "11.00"},
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Set() = %#v fields %#v error %v", result, fields, err)
	}
	if result.Changes == nil {
		t.Fatal("Set() changes = nil, want non-nil slice")
	}
}

func TestSetCapturesInjectedNowOnce(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	existing := createCategory(t, ctx, categories, "Existing")
	insertBudget(t, ctx, db, existing.ID, "2026-08", "10.00")
	called := 0
	store.Now = func() time.Time {
		called++
		return torontoTime(t, 2026, 8, 15, 12, 0)
	}

	_, fields, err := store.Set(ctx, "2026-08", []budget.Allocation{{Category: "Missing", Amount: "1"}})
	if len(fields) != 0 {
		t.Fatalf("Set() fields = %#v, want none", fields)
	}
	var missing *budget.CategoryNotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("Set() error = %v, want missing category", err)
	}
	if called != 1 {
		t.Fatalf("Now() calls = %d, want 1", called)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 1 {
		t.Fatalf("August rows after missing category = %d, want 1", got)
	}
}

func TestSetUpdatesExistingPastMonth(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	insertBudget(t, ctx, db, groceries.ID, "2026-07", "10.00")
	insertBudget(t, ctx, db, groceries.ID, "2026-08", "20.00")
	augustBefore := listStoredBudgets(t, ctx, db, "2026-08")

	result, fields, err := store.Set(ctx, "2026-07", []budget.Allocation{
		{Category: groceries.Name, Amount: "99.00"},
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Set(2026-07) = %#v fields %#v error %v", result, fields, err)
	}
	if result.Month != "2026-07" || len(result.Changes) != 1 || result.Changes[0].Created || result.Changes[0].Budget.Amount != "99.00" {
		t.Fatalf("Set(2026-07) result = %#v, want July Groceries 99.00", result)
	}
	july := listStoredBudgets(t, ctx, db, "2026-07")
	if len(july) != 1 || july[0].AmountHundredths != 9900 {
		t.Fatalf("July rows = %#v, want 99.00", july)
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-08"), augustBefore) {
		t.Fatal("August changed after past-month Set")
	}
}

func TestSetRejectsFutureMonthBeforeWrite(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	insertBudget(t, ctx, db, groceries.ID, "2026-08", "20.00")
	insertBudget(t, ctx, db, groceries.ID, "2026-09", "30.00")
	augustBefore := listStoredBudgets(t, ctx, db, "2026-08")
	septemberBefore := listStoredBudgets(t, ctx, db, "2026-09")

	_, fields, err := store.Set(ctx, "2026-09", []budget.Allocation{
		{Category: groceries.Name, Amount: "99.00"},
	})
	if err != nil {
		t.Fatalf("Set(2026-09) error = %v, want semantic issue", err)
	}
	want := []contract.FieldIssue{{Field: "month", Reason: "must not be in the future"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("future-month fields = %#v, want %#v", fields, want)
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-08"), augustBefore) {
		t.Fatal("August changed after future Set")
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-09"), septemberBefore) {
		t.Fatal("September changed after future Set")
	}
}
