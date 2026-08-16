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

func TestCreateExplicitReturnsCanonicalOrderedSnapshot(t *testing.T) {
	ctx := context.Background()
	now := torontoTime(t, 2026, 8, 15, 12, 0)
	store, categories, db := openBudgetStore(t, now)

	banana := createCategory(t, ctx, categories, "banana")
	apple := createCategory(t, ctx, categories, "Apple")
	dining := createCategory(t, ctx, categories, "Dining")

	result, fields, err := store.CreateExplicit(ctx, "2026-08", []budget.Allocation{
		{Category: " dining ", Amount: "0"},
		{Category: " BANANA ", Amount: "1.5"},
		{Category: "apple", Amount: "500"},
	})
	if err != nil {
		t.Fatalf("CreateExplicit() error = %v", err)
	}
	if len(fields) != 0 {
		t.Fatalf("CreateExplicit() fields = %#v, want none", fields)
	}
	if result.Month != "2026-08" || result.TotalBudget != "501.50" {
		t.Fatalf("CreateExplicit() result header = (%q, %q), want (2026-08, 501.50)", result.Month, result.TotalBudget)
	}
	if result.CreationMode != "explicit" || result.SourceMonth != nil {
		t.Fatalf("CreateExplicit() mode = (%q, %#v), want (explicit, nil)", result.CreationMode, result.SourceMonth)
	}
	if result.Budgets == nil || len(result.Budgets) != 3 {
		t.Fatalf("CreateExplicit() budgets = %#v, want three non-nil rows", result.Budgets)
	}

	gotNames := make([]string, 0, len(result.Budgets))
	gotAmounts := make([]string, 0, len(result.Budgets))
	for _, row := range result.Budgets {
		gotNames = append(gotNames, row.Category)
		gotAmounts = append(gotAmounts, row.Amount)
		if row.ID <= 0 || row.CategoryID <= 0 || row.Month != "2026-08" || row.CreatedAt == "" || row.UpdatedAt == "" {
			t.Fatalf("canonical budget row = %#v, want database identity and timestamps", row)
		}
	}
	if !reflect.DeepEqual(gotNames, []string{"Apple", "banana", "Dining"}) {
		t.Fatalf("budget category order = %#v, want Apple, banana, Dining", gotNames)
	}
	if !reflect.DeepEqual(gotAmounts, []string{"500.00", "1.50", "0.00"}) {
		t.Fatalf("budget amounts = %#v, want normalized values", gotAmounts)
	}
	if result.Budgets[0].CategoryID != apple.ID || result.Budgets[1].CategoryID != banana.ID || result.Budgets[2].CategoryID != dining.ID {
		t.Fatalf("budget category IDs = %#v, want canonical IDs", result.Budgets)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 3 {
		t.Fatalf("stored budget row count = %d, want 3", got)
	}
}

func TestCreateExplicitValidationOrderAndNoWrite(t *testing.T) {
	ctx := context.Background()
	store, _, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.CreateExplicit(ctx, "not-a-month", []budget.Allocation{
		{Category: " \t", Amount: "-1"},
		{Category: "Food\x00", Amount: "1.234"},
	})
	if err != nil {
		t.Fatalf("CreateExplicit() error = %v, want semantic issues", err)
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
}

func TestCreateExplicitRejectsDuplicateWithSQLiteASCIIEquality(t *testing.T) {
	ctx := context.Background()
	store, _, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.CreateExplicit(ctx, "2026-08", []budget.Allocation{
		{Category: "Groceries", Amount: "1"},
		{Category: "gROCERIES", Amount: "2"},
		{Category: "É", Amount: "3"},
		{Category: "é", Amount: "4"},
	})
	if err != nil {
		t.Fatalf("CreateExplicit() error = %v, want semantic issues", err)
	}
	want := []contract.FieldIssue{{Field: "budgets[1].category", Reason: "must not repeat a category"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("duplicate fields = %#v, want %#v", fields, want)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
		t.Fatalf("budget rows after duplicate failure = %d, want 0", got)
	}
}

func TestCreateExplicitReportsDuplicateBeforeLaterInvalidAmount(t *testing.T) {
	ctx := context.Background()
	store, _, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.CreateExplicit(ctx, "2026-08", []budget.Allocation{
		{Category: "Groceries", Amount: "1"},
		{Category: " groceries ", Amount: "not-an-amount"},
	})
	if err != nil {
		t.Fatalf("CreateExplicit() error = %v, want semantic issues", err)
	}
	want := []contract.FieldIssue{
		{Field: "budgets[1].category", Reason: "must not repeat a category"},
		{Field: "budgets[1].amount", Reason: "must be a non-negative amount with at most two decimal places"},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("duplicate/amount fields = %#v, want %#v", fields, want)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
		t.Fatalf("budget rows after validation failure = %d, want 0", got)
	}
}

func TestCreateExplicitRejectsTotalOverflowBeforeTransaction(t *testing.T) {
	ctx := context.Background()
	store, _, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.CreateExplicit(ctx, "2026-08", []budget.Allocation{
		{Category: "One", Amount: "92233720368547758.07"},
		{Category: "Two", Amount: "92233720368547758.07"},
	})
	if err != nil {
		t.Fatalf("CreateExplicit() error = %v, want semantic issue", err)
	}
	want := []contract.FieldIssue{{Field: "budgets", Reason: "total must fit the supported amount range"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("overflow fields = %#v, want %#v", fields, want)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
		t.Fatalf("budget rows after overflow failure = %d, want 0", got)
	}
}

func TestCreateExplicitRequiresCurrentLocalMonth(t *testing.T) {
	ctx := context.Background()
	store, _, db := openBudgetStore(t, torontoTime(t, 2026, 8, 31, 23, 30))

	_, fields, err := store.CreateExplicit(ctx, "2026-09", []budget.Allocation{{Category: "Groceries", Amount: "1"}})
	if err != nil {
		t.Fatalf("CreateExplicit() error = %v, want semantic issue", err)
	}
	want := []contract.FieldIssue{{Field: "month", Reason: "must equal the current local month"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("month fields = %#v, want %#v", fields, want)
	}
	if got := countBudgetRows(t, ctx, db, "2026-09"); got != 0 {
		t.Fatalf("budget rows after non-current month = %d, want 0", got)
	}
}

func TestCreateExplicitCapturesInjectedNowOnce(t *testing.T) {
	ctx := context.Background()
	store, _, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	called := 0
	store.Now = func() time.Time {
		called++
		return torontoTime(t, 2026, 8, 15, 12, 0)
	}

	_, fields, err := store.CreateExplicit(ctx, "2026-08", []budget.Allocation{{Category: "Missing", Amount: "1"}})
	if len(fields) != 0 {
		t.Fatalf("CreateExplicit() fields = %#v, want none", fields)
	}
	var missing *budget.CategoryNotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("CreateExplicit() error = %v, want missing category", err)
	}
	if called != 1 {
		t.Fatalf("Now() calls = %d, want 1", called)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
		t.Fatalf("budget rows after missing category = %d, want 0", got)
	}
}

func TestCreateExplicitCategoryErrorsCarrySameTransactionRecoveryContext(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	alpha := createCategory(t, ctx, categories, "Alpha")
	inactive := createCategory(t, ctx, categories, "Dining")
	beta := createCategory(t, ctx, categories, "beta")
	if _, changed, _, err := categories.Disable(ctx, inactive.Name); err != nil || !changed {
		t.Fatalf("Disable(%q) = changed %v, error %v", inactive.Name, changed, err)
	}

	_, _, err := store.CreateExplicit(ctx, "2026-08", []budget.Allocation{{Category: " Pharmacy ", Amount: "1"}})
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
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
		t.Fatalf("budget rows after missing category = %d, want 0", got)
	}

	_, _, err = store.CreateExplicit(ctx, "2026-08", []budget.Allocation{{Category: "dining", Amount: "1"}})
	var inactiveErr *budget.CategoryInactiveError
	if !errors.As(err, &inactiveErr) {
		t.Fatalf("inactive category error = %v, want *CategoryInactiveError", err)
	}
	if inactiveErr.Category.ID != inactive.ID || inactiveErr.Category.Name != inactive.Name || inactiveErr.Category.Active || inactiveErr.ActiveCategories == nil || len(inactiveErr.ActiveCategories) != 2 {
		t.Fatalf("inactive recovery = %#v, want canonical inactive category and active list", inactiveErr)
	}
}

func TestCreateExplicitExistingMonthPrecedesCategoryErrors(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	existing := createCategory(t, ctx, categories, "Existing")
	insertBudget(t, ctx, db, existing.ID, "2026-08", "10")

	_, fields, err := store.CreateExplicit(ctx, "2026-08", []budget.Allocation{{Category: "Missing", Amount: "1"}})
	if len(fields) != 0 {
		t.Fatalf("existing-month fields = %#v, want none", fields)
	}
	var alreadyExists *budget.AlreadyExistsError
	if !errors.As(err, &alreadyExists) {
		t.Fatalf("existing-month error = %v, want *AlreadyExistsError", err)
	}
	if alreadyExists.Month != "2026-08" {
		t.Fatalf("existing-month error month = %q, want 2026-08", alreadyExists.Month)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 1 {
		t.Fatalf("budget rows after existing-month failure = %d, want 1", got)
	}
}

func TestCreateExplicitRollsBackAllInsertsOnFailure(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	first := createCategory(t, ctx, categories, "First")
	second := createCategory(t, ctx, categories, "Second")
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TRIGGER fail_second_budget_insert
		BEFORE INSERT ON budgets
		WHEN NEW.category_id = %d
		BEGIN
			SELECT RAISE(ABORT, 'test budget insert failure');
		END
	`, second.ID)); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	_, fields, err := store.CreateExplicit(ctx, "2026-08", []budget.Allocation{
		{Category: first.Name, Amount: "1"},
		{Category: second.Name, Amount: "2"},
	})
	if len(fields) != 0 {
		t.Fatalf("rollback fields = %#v, want none", fields)
	}
	if err == nil {
		t.Fatal("CreateExplicit() error = nil, want trigger failure")
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
		t.Fatalf("budget rows after failed insert = %d, want 0", got)
	}
}

func TestCreateExplicitAcceptsZeroAndNormalizesAmounts(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	zero := createCategory(t, ctx, categories, "Zero")
	max := createCategory(t, ctx, categories, "Max")

	result, fields, err := store.CreateExplicit(ctx, "2026-08", []budget.Allocation{
		{Category: zero.Name, Amount: "0.0"},
		{Category: max.Name, Amount: "92233720368547758.07"},
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("CreateExplicit() = result %#v, fields %#v, error %v; want success", result, fields, err)
	}
	if result.TotalBudget != "92233720368547758.07" {
		t.Fatalf("total_budget = %q, want max amount", result.TotalBudget)
	}
	if got := budgetAmounts(t, ctx, db, "2026-08"); !reflect.DeepEqual(got, []int64{0, 9223372036854775807}) {
		t.Fatalf("stored amount_hundredths = %#v, want zero and MaxInt64", got)
	}
}
