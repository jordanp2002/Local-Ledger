package budget_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/budget"
	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

func TestCreateExplicitKeepsIssue6FieldPathsThroughUnifiedCreate(t *testing.T) {
	ctx := context.Background()
	store, _, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.Create(ctx, budget.CreateInput{
		Month: "not-a-month",
		Budgets: []budget.Allocation{
			{Category: " \t", Amount: "-1"},
			{Category: "Food\x00", Amount: "1.234"},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want semantic issues", err)
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

func TestCreateCarryForwardTrueWithOmittedBudgetsAndOverridesIsValidShape(t *testing.T) {
	ctx := context.Background()
	store, _, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.Create(ctx, budget.CreateInput{
		Month:        "2026-08",
		CarryForward: boolPtr(true),
	})
	if len(fields) != 0 {
		t.Fatalf("Create() fields = %#v, want none for valid carry-forward shape", fields)
	}
	var missing *budget.SourceNotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("Create() error = %v, want *SourceNotFoundError", err)
	}
	if missing.Month != "2026-08" {
		t.Fatalf("SourceNotFoundError.Month = %q, want 2026-08", missing.Month)
	}
	if !errors.Is(err, budget.ErrSourceNotFound) {
		t.Fatalf("SourceNotFoundError should wrap ErrSourceNotFound: %v", err)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
		t.Fatalf("budget rows after source-not-found = %d, want 0", got)
	}
}

func TestCreateCarryForwardTrueWithEmptyBudgetsSliceIsValidShape(t *testing.T) {
	ctx := context.Background()
	store, _, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.Create(ctx, budget.CreateInput{
		Month:        "2026-08",
		Budgets:      []budget.Allocation{},
		CarryForward: boolPtr(true),
	})
	if len(fields) != 0 {
		t.Fatalf("Create() fields = %#v, want none for empty budgets slice", fields)
	}
	var missing *budget.SourceNotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("Create() error = %v, want *SourceNotFoundError", err)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
		t.Fatalf("budget rows after empty-budgets carry-forward = %d, want 0", got)
	}
}

func TestCreateRejectsCarryForwardCombinedWithNonEmptyBudgets(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")

	_, fields, err := store.Create(ctx, budget.CreateInput{
		Month:        "2026-08",
		Budgets:      []budget.Allocation{{Category: "Groceries", Amount: "1.00"}},
		CarryForward: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want semantic issue", err)
	}
	want := []contract.FieldIssue{{Field: "budgets", Reason: "cannot be combined with carry_forward"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("combination fields = %#v, want %#v", fields, want)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
		t.Fatalf("budget rows after combination failure = %d, want 0", got)
	}
}

func TestCreateRejectsCarryForwardFalseEvenWhenBudgetsValid(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")

	_, fields, err := store.Create(ctx, budget.CreateInput{
		Month:        "2026-08",
		Budgets:      []budget.Allocation{{Category: "Groceries", Amount: "1.00"}},
		CarryForward: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want semantic issue", err)
	}
	want := []contract.FieldIssue{{Field: "carry_forward", Reason: "must be true when supplied"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("carry_forward fields = %#v, want %#v", fields, want)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
		t.Fatalf("budget rows after carry_forward false = %d, want 0", got)
	}
}

func TestCreateRejectsNonEmptyOverridesWithoutCarryForward(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")

	_, fields, err := store.Create(ctx, budget.CreateInput{
		Month:     "2026-08",
		Budgets:   []budget.Allocation{{Category: "Groceries", Amount: "1.00"}},
		Overrides: []budget.Allocation{{Category: "Groceries", Amount: "2.00"}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want semantic issue", err)
	}
	want := []contract.FieldIssue{{Field: "overrides", Reason: "cannot be supplied unless carry_forward is true"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("overrides fields = %#v, want %#v", fields, want)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
		t.Fatalf("budget rows after illegal overrides = %d, want 0", got)
	}
}

func TestCreateMonthOnlyStillRequiresNonEmptyBudgets(t *testing.T) {
	ctx := context.Background()
	store, _, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.Create(ctx, budget.CreateInput{Month: "2026-08"})
	if err != nil {
		t.Fatalf("Create() error = %v, want semantic issue", err)
	}
	want := []contract.FieldIssue{{Field: "budgets", Reason: "must contain at least one allocation"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("month-only fields = %#v, want %#v", fields, want)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
		t.Fatalf("budget rows after month-only request = %d, want 0", got)
	}
}

func TestCreateReportsMultipleSemanticProblemsInStableFieldOrder(t *testing.T) {
	ctx := context.Background()
	store, _, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.Create(ctx, budget.CreateInput{
		Month:        "not-a-month",
		CarryForward: boolPtr(false),
		Overrides:    []budget.Allocation{{Category: "Dining", Amount: "1"}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want semantic issues", err)
	}
	want := []contract.FieldIssue{
		{Field: "month", Reason: "must be a valid YYYY-MM month"},
		{Field: "budgets", Reason: "must contain at least one allocation"},
		{Field: "carry_forward", Reason: "must be true when supplied"},
		{Field: "overrides", Reason: "cannot be supplied unless carry_forward is true"},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("multi-issue fields = %#v, want %#v", fields, want)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
		t.Fatalf("budget rows after multi-issue validation = %d, want 0", got)
	}
}

func TestCreateValidatesSelectedCarryForwardOverridesAfterMonthIssues(t *testing.T) {
	ctx := context.Background()
	store, _, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.Create(ctx, budget.CreateInput{
		Month:        "2026-09",
		CarryForward: boolPtr(true),
		Overrides: []budget.Allocation{
			{Category: " \t", Amount: "-1"},
			{Category: "Food\x00", Amount: "1.234"},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want semantic issues", err)
	}
	want := []contract.FieldIssue{
		{Field: "month", Reason: "must not be in the future"},
		{Field: "overrides[0].category", Reason: "must not be empty"},
		{Field: "overrides[0].amount", Reason: "must be a non-negative amount with at most two decimal places"},
		{Field: "overrides[1].category", Reason: "must not contain NUL characters"},
		{Field: "overrides[1].amount", Reason: "must be a non-negative amount with at most two decimal places"},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("override validation fields = %#v, want %#v", fields, want)
	}
	if got := countBudgetRows(t, ctx, db, "2026-09"); got != 0 {
		t.Fatalf("budget rows after override validation = %d, want 0", got)
	}
}

func TestCreateCarryForwardRejectsEmptyWhitespaceAndNULOverrideCategories(t *testing.T) {
	ctx := context.Background()
	store, _, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.CreateCarryForward(ctx, "2026-08", []budget.Allocation{
		{Category: "", Amount: "1"},
		{Category: " \t\n", Amount: "1"},
		{Category: "Food\x00", Amount: "1"},
	})
	if err != nil {
		t.Fatalf("CreateCarryForward() error = %v, want semantic issues", err)
	}
	want := []contract.FieldIssue{
		{Field: "overrides[0].category", Reason: "must not be empty"},
		{Field: "overrides[1].category", Reason: "must not be empty"},
		{Field: "overrides[2].category", Reason: "must not contain NUL characters"},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("override category fields = %#v, want %#v", fields, want)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
		t.Fatalf("budget rows after override category failure = %d, want 0", got)
	}
}

func TestCreateCarryForwardRejectsInvalidOverrideAmounts(t *testing.T) {
	ctx := context.Background()
	now := torontoTime(t, 2026, 8, 15, 12, 0)
	invalidAmounts := []string{"", " ", "-1", "+1", "1e2", " 1.00", "1,00", "1.234", "92233720368547758.08"}

	for _, amount := range invalidAmounts {
		t.Run(amount, func(t *testing.T) {
			store, _, db := openBudgetStore(t, now)
			_, fields, err := store.CreateCarryForward(ctx, "2026-08", []budget.Allocation{
				{Category: "Groceries", Amount: amount},
			})
			if err != nil {
				t.Fatalf("CreateCarryForward() error = %v, want semantic issue", err)
			}
			want := []contract.FieldIssue{{
				Field:  "overrides[0].amount",
				Reason: "must be a non-negative amount with at most two decimal places",
			}}
			if !reflect.DeepEqual(fields, want) {
				t.Fatalf("amount %q fields = %#v, want %#v", amount, fields, want)
			}
			if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
				t.Fatalf("budget rows after invalid amount %q = %d, want 0", amount, got)
			}
		})
	}
}

func TestCreateCarryForwardRejectsDuplicateOverrideCategoriesWithASCIINoCase(t *testing.T) {
	ctx := context.Background()
	store, _, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.CreateCarryForward(ctx, "2026-08", []budget.Allocation{
		{Category: "Groceries", Amount: "1"},
		{Category: "gROCERIES", Amount: "2"},
		{Category: "É", Amount: "3"},
		{Category: "é", Amount: "4"},
	})
	if err != nil {
		t.Fatalf("CreateCarryForward() error = %v, want semantic issues", err)
	}
	want := []contract.FieldIssue{{Field: "overrides[1].category", Reason: "must not repeat a category"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("duplicate override fields = %#v, want %#v", fields, want)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
		t.Fatalf("budget rows after duplicate overrides = %d, want 0", got)
	}
}

func TestCreateCarryForwardDoesNotFoldUnicodeBeyondNOCASE(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	acute := createCategory(t, ctx, categories, "É")
	insertBudget(t, ctx, db, acute.ID, "2026-07", "1.00")

	_, fields, err := store.CreateCarryForward(ctx, "2026-08", []budget.Allocation{
		{Category: "é", Amount: "2.00"},
	})
	if len(fields) != 0 {
		t.Fatalf("CreateCarryForward() fields = %#v, want none", fields)
	}
	var missing *budget.CategoryNotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("CreateCarryForward() error = %v, want *CategoryNotFoundError for unfolded é", err)
	}
	if missing.Requested != "é" {
		t.Fatalf("missing override category = %q, want é", missing.Requested)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
		t.Fatalf("budget rows after unicode mismatch = %d, want 0", got)
	}
}

func TestCreateCarryForwardRejectsOverrideOnlyOverflowBeforeTransaction(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	existing := createCategory(t, ctx, categories, "Existing")
	insertBudget(t, ctx, db, existing.ID, "2026-08", "10.00")

	_, fields, err := store.CreateCarryForward(ctx, "2026-08", []budget.Allocation{
		{Category: "One", Amount: "92233720368547758.07"},
		{Category: "Two", Amount: "92233720368547758.07"},
	})
	if err != nil {
		t.Fatalf("CreateCarryForward() error = %v, want semantic issue before already-exists", err)
	}
	want := []contract.FieldIssue{{Field: "overrides", Reason: "total must fit the supported amount range"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("override overflow fields = %#v, want %#v", fields, want)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 1 {
		t.Fatalf("budget rows after override overflow = %d, want existing 1", got)
	}
}

func TestCreateDoesNotValidateAllocationsWhenModeIsNotSelected(t *testing.T) {
	ctx := context.Background()
	store, _, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	_, fields, err := store.Create(ctx, budget.CreateInput{
		Month:        "2026-08",
		Budgets:      []budget.Allocation{{Category: " ", Amount: "-1"}},
		CarryForward: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want semantic issue", err)
	}
	want := []contract.FieldIssue{{Field: "carry_forward", Reason: "must be true when supplied"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("unselected-mode fields = %#v, want only carry_forward issue", fields)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
		t.Fatalf("budget rows after unselected mode = %d, want 0", got)
	}
}

func TestCreateCapturesInjectedNowOnceForCarryForward(t *testing.T) {
	ctx := context.Background()
	store, _, db := openBudgetStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	called := 0
	store.Now = func() time.Time {
		called++
		return torontoTime(t, 2026, 8, 15, 12, 0)
	}

	_, fields, err := store.CreateCarryForward(ctx, "2026-08", nil)
	if len(fields) != 0 {
		t.Fatalf("CreateCarryForward() fields = %#v, want none", fields)
	}
	var missing *budget.SourceNotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("CreateCarryForward() error = %v, want missing source", err)
	}
	if called != 1 {
		t.Fatalf("Now() calls = %d, want 1", called)
	}
	if got := countBudgetRows(t, ctx, db, "2026-08"); got != 0 {
		t.Fatalf("budget rows after missing source = %d, want 0", got)
	}
}

func TestCreateDerivesMonthFromLocalClockBeforeUTC(t *testing.T) {
	ctx := context.Background()
	// 2026-08-31 23:30 America/Toronto is 2026-09-01 03:30 UTC.
	now := torontoTime(t, 2026, 8, 31, 23, 30)
	store, categories, db := openBudgetStore(t, now)
	groceries := createCategory(t, ctx, categories, "Groceries")
	insertBudget(t, ctx, db, groceries.ID, "2026-07", "10.00")

	_, fields, err := store.CreateCarryForward(ctx, "2026-09", nil)
	if err != nil {
		t.Fatalf("CreateCarryForward(2026-09) error = %v, want semantic issue", err)
	}
	if !reflect.DeepEqual(fields, []contract.FieldIssue{{Field: "month", Reason: "must not be in the future"}}) {
		t.Fatalf("UTC-month fields = %#v, want future-month issue", fields)
	}

	result, fields, err := store.CreateCarryForward(ctx, "2026-08", nil)
	if err != nil || len(fields) != 0 {
		t.Fatalf("CreateCarryForward(2026-08) = %#v fields %#v error %v; want local August success", result, fields, err)
	}
	if result.Month != "2026-08" || result.CreationMode != "carry_forward" {
		t.Fatalf("local August result = %#v, want carry-forward of 2026-08", result)
	}
	if result.SourceMonth == nil || *result.SourceMonth != "2026-07" {
		t.Fatalf("source month = %#v, want 2026-07", result.SourceMonth)
	}
}
