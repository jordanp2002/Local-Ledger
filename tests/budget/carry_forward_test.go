package budget_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/budget"
	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

const frozenSourceTimestamp = "2020-01-01T00:00:00.000Z"

func TestCreateCarryForwardCopiesImmediatelyPreviousMonthIndependently(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 4, 15, 12, 0))
	dining := createCategory(t, ctx, categories, "Dining")
	groceries := createCategory(t, ctx, categories, "Groceries")
	insertBudget(t, ctx, db, groceries.ID, "2026-03", "500.00")
	insertBudget(t, ctx, db, dining.ID, "2026-03", "150.00")
	setBudgetTimestamps(t, ctx, db, "2026-03", frozenSourceTimestamp)
	sourceBefore := listStoredBudgets(t, ctx, db, "2026-03")

	result, fields, err := store.CreateCarryForward(ctx, "2026-04", nil)
	if err != nil || len(fields) != 0 {
		t.Fatalf("CreateCarryForward() = %#v fields %#v error %v", result, fields, err)
	}
	if result.CreationMode != "carry_forward" || result.Month != "2026-04" || result.TotalBudget != "650.00" {
		t.Fatalf("result header = %#v, want carry_forward 2026-04 650.00", result)
	}
	if result.SourceMonth == nil || *result.SourceMonth != "2026-03" {
		t.Fatalf("source month = %#v, want 2026-03", result.SourceMonth)
	}
	if result.Budgets == nil || len(result.Budgets) != 2 {
		t.Fatalf("budgets = %#v, want two non-nil rows", result.Budgets)
	}
	if result.Budgets[0].Category != "Dining" || result.Budgets[1].Category != "Groceries" {
		t.Fatalf("copied names = %#v, want Dining, Groceries", result.Budgets)
	}
	sourceIDs := map[int64]struct{}{
		sourceBefore[0].ID: {},
		sourceBefore[1].ID: {},
	}
	if _, ok := sourceIDs[result.Budgets[0].ID]; ok {
		t.Fatalf("copied Dining ID %d reused a source ID", result.Budgets[0].ID)
	}
	if _, ok := sourceIDs[result.Budgets[1].ID]; ok {
		t.Fatalf("copied Groceries ID %d reused a source ID", result.Budgets[1].ID)
	}
	if result.Budgets[0].CreatedAt == frozenSourceTimestamp || result.Budgets[1].CreatedAt == frozenSourceTimestamp {
		t.Fatalf("copied timestamps reused source timestamp: %#v", result.Budgets)
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-03"), sourceBefore) {
		t.Fatalf("source month changed after copy")
	}
	if got := countBudgetRows(t, ctx, db, "2026-04"); got != 2 {
		t.Fatalf("target row count = %d, want 2", got)
	}
}

func TestCreateCarryForwardSkipsEmptyInterveningMonths(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 4, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	insertBudget(t, ctx, db, groceries.ID, "2026-01", "80.00")

	result, fields, err := store.CreateCarryForward(ctx, "2026-04", nil)
	if err != nil || len(fields) != 0 {
		t.Fatalf("CreateCarryForward() = %#v fields %#v error %v", result, fields, err)
	}
	if result.SourceMonth == nil || *result.SourceMonth != "2026-01" {
		t.Fatalf("source month = %#v, want 2026-01", result.SourceMonth)
	}
	if result.TotalBudget != "80.00" || len(result.Budgets) != 1 || result.Budgets[0].Category != "Groceries" {
		t.Fatalf("copied snapshot = %#v, want January Groceries 80.00", result)
	}
	if got := countBudgetRows(t, ctx, db, "2026-02"); got != 0 {
		t.Fatalf("February rows = %d, want 0", got)
	}
	if got := countBudgetRows(t, ctx, db, "2026-03"); got != 0 {
		t.Fatalf("March rows = %d, want 0", got)
	}
}

func TestCreateCarryForwardPrefersLatestEarlierMonthWhenSeveralExist(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 4, 15, 12, 0))
	january := createCategory(t, ctx, categories, "JanuaryOnly")
	march := createCategory(t, ctx, categories, "MarchOnly")
	insertBudget(t, ctx, db, january.ID, "2026-01", "10.00")
	insertBudget(t, ctx, db, march.ID, "2026-03", "25.00")
	januaryBefore := listStoredBudgets(t, ctx, db, "2026-01")
	marchBefore := listStoredBudgets(t, ctx, db, "2026-03")

	result, fields, err := store.CreateCarryForward(ctx, "2026-04", nil)
	if err != nil || len(fields) != 0 {
		t.Fatalf("CreateCarryForward() = %#v fields %#v error %v", result, fields, err)
	}
	if result.SourceMonth == nil || *result.SourceMonth != "2026-03" {
		t.Fatalf("source month = %#v, want 2026-03", result.SourceMonth)
	}
	if len(result.Budgets) != 1 || result.Budgets[0].Category != "MarchOnly" || result.Budgets[0].Amount != "25.00" {
		t.Fatalf("copied snapshot = %#v, want MarchOnly 25.00", result.Budgets)
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-01"), januaryBefore) {
		t.Fatalf("January changed after April copy")
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-03"), marchBefore) {
		t.Fatalf("March changed after April copy")
	}
}

func TestCreateCarryForwardOmitsInactiveSourceCategories(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 4, 15, 12, 0))
	dining := createCategory(t, ctx, categories, "Dining")
	groceries := createCategory(t, ctx, categories, "Groceries")
	insertBudget(t, ctx, db, dining.ID, "2026-01", "40.00")
	insertBudget(t, ctx, db, groceries.ID, "2026-01", "60.00")
	if _, changed, _, err := categories.Disable(ctx, dining.Name); err != nil || !changed {
		t.Fatalf("Disable(%q) = changed %v, error %v", dining.Name, changed, err)
	}

	result, fields, err := store.CreateCarryForward(ctx, "2026-04", nil)
	if err != nil || len(fields) != 0 {
		t.Fatalf("CreateCarryForward() = %#v fields %#v error %v", result, fields, err)
	}
	if len(result.Budgets) != 1 || result.Budgets[0].Category != "Groceries" || result.Budgets[0].Amount != "60.00" {
		t.Fatalf("copied snapshot = %#v, want only active Groceries", result.Budgets)
	}
	if result.TotalBudget != "60.00" {
		t.Fatalf("total_budget = %q, want 60.00", result.TotalBudget)
	}
	if got := countBudgetRows(t, ctx, db, "2026-01"); got != 2 {
		t.Fatalf("January source rows = %d, want 2", got)
	}
}

func TestCreateCarryForwardAllInactiveSourceIsEmptyEvenWithValidOverride(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 4, 15, 12, 0))
	dining := createCategory(t, ctx, categories, "Dining")
	groceries := createCategory(t, ctx, categories, "Groceries")
	insertBudget(t, ctx, db, dining.ID, "2026-01", "40.00")
	if _, changed, _, err := categories.Disable(ctx, dining.Name); err != nil || !changed {
		t.Fatalf("Disable(%q) = changed %v, error %v", dining.Name, changed, err)
	}

	_, fields, err := store.CreateCarryForward(ctx, "2026-04", []budget.Allocation{
		{Category: groceries.Name, Amount: "10.00"},
	})
	if len(fields) != 0 {
		t.Fatalf("CreateCarryForward() fields = %#v, want none", fields)
	}
	var empty *budget.SourceEmptyError
	if !errors.As(err, &empty) {
		t.Fatalf("CreateCarryForward() error = %v, want *SourceEmptyError", err)
	}
	if empty.Month != "2026-04" || empty.SourceMonth != "2026-01" {
		t.Fatalf("SourceEmptyError = %#v, want month 2026-04 source 2026-01", empty)
	}
	if !errors.Is(err, budget.ErrSourceEmpty) {
		t.Fatalf("SourceEmptyError should wrap ErrSourceEmpty: %v", err)
	}
	if got := countBudgetRows(t, ctx, db, "2026-04"); got != 0 {
		t.Fatalf("April rows after source empty = %d, want 0", got)
	}
	if got := countBudgetRows(t, ctx, db, "2026-01"); got != 1 {
		t.Fatalf("January rows after source empty = %d, want 1", got)
	}
}

func TestCreateCarryForwardNoEarlierSnapshotIsSourceNotFound(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 4, 15, 12, 0))
	createCategory(t, ctx, categories, "Groceries")

	_, fields, err := store.CreateCarryForward(ctx, "2026-04", nil)
	if len(fields) != 0 {
		t.Fatalf("CreateCarryForward() fields = %#v, want none", fields)
	}
	var missing *budget.SourceNotFoundError
	if !errors.As(err, &missing) || missing.Month != "2026-04" {
		t.Fatalf("CreateCarryForward() error = %#v, want SourceNotFoundError month 2026-04", err)
	}
	if got := countBudgetRows(t, ctx, db, "2026-04"); got != 0 {
		t.Fatalf("April rows after source not found = %d, want 0", got)
	}
}

func TestCreateCarryForwardExistingTargetPrecedesSourceAndCategoryErrors(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 4, 15, 12, 0))
	existing := createCategory(t, ctx, categories, "Existing")
	insertBudget(t, ctx, db, existing.ID, "2026-04", "5.00")

	_, fields, err := store.CreateCarryForward(ctx, "2026-04", []budget.Allocation{
		{Category: "Missing", Amount: "1.00"},
	})
	if len(fields) != 0 {
		t.Fatalf("existing-target fields = %#v, want none", fields)
	}
	var alreadyExists *budget.AlreadyExistsError
	if !errors.As(err, &alreadyExists) || alreadyExists.Month != "2026-04" {
		t.Fatalf("existing-target error = %v, want AlreadyExistsError for 2026-04", err)
	}
	if got := countBudgetRows(t, ctx, db, "2026-04"); got != 1 {
		t.Fatalf("April rows after already-exists = %d, want 1", got)
	}

	insertBudget(t, ctx, db, existing.ID, "2026-01", "9.00")
	_, fields, err = store.CreateCarryForward(ctx, "2026-04", []budget.Allocation{
		{Category: "Missing", Amount: "1.00"},
	})
	if len(fields) != 0 {
		t.Fatalf("existing-target-with-source fields = %#v, want none", fields)
	}
	if !errors.As(err, &alreadyExists) {
		t.Fatalf("existing-target-with-source error = %v, want AlreadyExistsError before category errors", err)
	}
}

func TestCreateCarryForwardOverrideReplacesCopiedAmountAndAddsMissingActiveCategory(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 4, 15, 12, 0))
	dining := createCategory(t, ctx, categories, "Dining")
	groceries := createCategory(t, ctx, categories, "Groceries")
	health := createCategory(t, ctx, categories, "Health")
	insertBudget(t, ctx, db, groceries.ID, "2026-03", "100.00")
	insertBudget(t, ctx, db, dining.ID, "2026-03", "50.00")
	sourceBefore := listStoredBudgets(t, ctx, db, "2026-03")

	result, fields, err := store.CreateCarryForward(ctx, "2026-04", []budget.Allocation{
		{Category: " dining ", Amount: "80.00"},
		{Category: "health", Amount: "25"},
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("CreateCarryForward() = %#v fields %#v error %v", result, fields, err)
	}
	if result.TotalBudget != "205.00" {
		t.Fatalf("total_budget = %q, want 205.00", result.TotalBudget)
	}
	gotNames := make([]string, 0, len(result.Budgets))
	gotAmounts := make([]string, 0, len(result.Budgets))
	gotIDs := make([]int64, 0, len(result.Budgets))
	for _, row := range result.Budgets {
		gotNames = append(gotNames, row.Category)
		gotAmounts = append(gotAmounts, row.Amount)
		gotIDs = append(gotIDs, row.CategoryID)
	}
	if !reflect.DeepEqual(gotNames, []string{"Dining", "Groceries", "Health"}) {
		t.Fatalf("merged names = %#v, want Dining, Groceries, Health", gotNames)
	}
	if !reflect.DeepEqual(gotAmounts, []string{"80.00", "100.00", "25.00"}) {
		t.Fatalf("merged amounts = %#v, want replaced Dining and added Health", gotAmounts)
	}
	if !reflect.DeepEqual(gotIDs, []int64{dining.ID, groceries.ID, health.ID}) {
		t.Fatalf("merged category IDs = %#v, want canonical IDs", gotIDs)
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-03"), sourceBefore) {
		t.Fatalf("source month changed after override merge")
	}
}

func TestCreateCarryForwardOverrideCategoryErrorsWriteNothingAndKeepRequestOrder(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 4, 15, 12, 0))
	alpha := createCategory(t, ctx, categories, "Alpha")
	inactive := createCategory(t, ctx, categories, "Dining")
	beta := createCategory(t, ctx, categories, "beta")
	insertBudget(t, ctx, db, alpha.ID, "2026-03", "10.00")
	if _, changed, _, err := categories.Disable(ctx, inactive.Name); err != nil || !changed {
		t.Fatalf("Disable(%q) = changed %v, error %v", inactive.Name, changed, err)
	}

	_, fields, err := store.CreateCarryForward(ctx, "2026-04", []budget.Allocation{
		{Category: " Pharmacy ", Amount: "1.00"},
		{Category: "dining", Amount: "2.00"},
	})
	if len(fields) != 0 {
		t.Fatalf("missing-override fields = %#v, want none", fields)
	}
	var missing *budget.CategoryNotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("missing override error = %v, want *CategoryNotFoundError", err)
	}
	if missing.Requested != "Pharmacy" || missing.ActiveCategories == nil || len(missing.ActiveCategories) != 2 {
		t.Fatalf("missing recovery = %#v, want trimmed request and two active categories", missing)
	}
	if missing.ActiveCategories[0].Name != alpha.Name || missing.ActiveCategories[1].Name != beta.Name {
		t.Fatalf("missing active recovery order = %#v, want Alpha, beta", missing.ActiveCategories)
	}
	if got := countBudgetRows(t, ctx, db, "2026-04"); got != 0 {
		t.Fatalf("April rows after missing override = %d, want 0", got)
	}

	_, fields, err = store.CreateCarryForward(ctx, "2026-04", []budget.Allocation{
		{Category: "dining", Amount: "2.00"},
		{Category: "Missing", Amount: "1.00"},
	})
	if len(fields) != 0 {
		t.Fatalf("inactive-override fields = %#v, want none", fields)
	}
	var inactiveErr *budget.CategoryInactiveError
	if !errors.As(err, &inactiveErr) {
		t.Fatalf("inactive override error = %v, want *CategoryInactiveError", err)
	}
	if inactiveErr.Category.ID != inactive.ID || inactiveErr.Category.Name != inactive.Name || inactiveErr.Category.Active {
		t.Fatalf("inactive recovery = %#v, want canonical inactive Dining", inactiveErr)
	}
	if inactiveErr.ActiveCategories == nil || len(inactiveErr.ActiveCategories) != 2 {
		t.Fatalf("inactive active list = %#v, want two categories", inactiveErr.ActiveCategories)
	}
	if got := countBudgetRows(t, ctx, db, "2026-04"); got != 0 {
		t.Fatalf("April rows after inactive override = %d, want 0", got)
	}
}

func TestCreateCarryForwardPersistsZeroSourceAndOverrideAmounts(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 4, 15, 12, 0))
	keep := createCategory(t, ctx, categories, "Keep")
	zeroA := createCategory(t, ctx, categories, "ZeroA")
	zeroB := createCategory(t, ctx, categories, "ZeroB")
	zeroC := createCategory(t, ctx, categories, "ZeroC")
	insertBudget(t, ctx, db, keep.ID, "2026-03", "0")

	result, fields, err := store.CreateCarryForward(ctx, "2026-04", []budget.Allocation{
		{Category: zeroA.Name, Amount: "0"},
		{Category: zeroB.Name, Amount: "0.0"},
		{Category: zeroC.Name, Amount: "0.00"},
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("CreateCarryForward() = %#v fields %#v error %v", result, fields, err)
	}
	if result.TotalBudget != "0.00" {
		t.Fatalf("total_budget = %q, want 0.00", result.TotalBudget)
	}
	if len(result.Budgets) != 4 {
		t.Fatalf("budgets = %#v, want four zero rows", result.Budgets)
	}
	for _, row := range result.Budgets {
		if row.Amount != "0.00" {
			t.Fatalf("amount = %q, want 0.00", row.Amount)
		}
	}
	if got := budgetAmounts(t, ctx, db, "2026-04"); !reflect.DeepEqual(got, []int64{0, 0, 0, 0}) {
		t.Fatalf("stored amount_hundredths = %#v, want four zeros", got)
	}
}

func TestCreateCarryForwardReturnsCanonicalTargetRowsAndCheckedTotal(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 4, 15, 12, 0))
	banana := createCategory(t, ctx, categories, "banana")
	apple := createCategory(t, ctx, categories, "Apple")
	dining := createCategory(t, ctx, categories, "Dining")
	insertBudget(t, ctx, db, banana.ID, "2026-03", "1.5")
	insertBudget(t, ctx, db, apple.ID, "2026-03", "500")
	insertBudget(t, ctx, db, dining.ID, "2026-03", "0")
	setBudgetTimestamps(t, ctx, db, "2026-03", frozenSourceTimestamp)

	result, fields, err := store.CreateCarryForward(ctx, "2026-04", nil)
	if err != nil || len(fields) != 0 {
		t.Fatalf("CreateCarryForward() = %#v fields %#v error %v", result, fields, err)
	}
	if result.Budgets == nil || len(result.Budgets) != 3 {
		t.Fatalf("budgets = %#v, want three non-nil rows", result.Budgets)
	}
	gotNames := []string{result.Budgets[0].Category, result.Budgets[1].Category, result.Budgets[2].Category}
	gotAmounts := []string{result.Budgets[0].Amount, result.Budgets[1].Amount, result.Budgets[2].Amount}
	if !reflect.DeepEqual(gotNames, []string{"Apple", "banana", "Dining"}) {
		t.Fatalf("budget category order = %#v, want Apple, banana, Dining", gotNames)
	}
	if !reflect.DeepEqual(gotAmounts, []string{"500.00", "1.50", "0.00"}) {
		t.Fatalf("budget amounts = %#v, want normalized values", gotAmounts)
	}
	if result.Budgets[0].CategoryID != apple.ID || result.Budgets[1].CategoryID != banana.ID || result.Budgets[2].CategoryID != dining.ID {
		t.Fatalf("budget category IDs = %#v, want canonical target IDs", result.Budgets)
	}
	for _, row := range result.Budgets {
		if row.ID <= 0 || row.Month != "2026-04" || row.CreatedAt == "" || row.UpdatedAt == "" || row.CreatedAt == frozenSourceTimestamp {
			t.Fatalf("canonical target row = %#v, want new target identity", row)
		}
	}

	var sum int64
	for _, amount := range budgetAmounts(t, ctx, db, "2026-04") {
		sum += amount
	}
	formatted, err := contract.FormatAmount(sum)
	if err != nil {
		t.Fatalf("FormatAmount(%d): %v", sum, err)
	}
	if result.TotalBudget != formatted || result.TotalBudget != "501.50" {
		t.Fatalf("total_budget = %q, want checked sum %q", result.TotalBudget, formatted)
	}
}

func TestCreateCarryForwardRejectsMergedSnapshotOverflowBeforeCommit(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 4, 15, 12, 0))
	one := createCategory(t, ctx, categories, "One")
	two := createCategory(t, ctx, categories, "Two")
	insertBudget(t, ctx, db, one.ID, "2026-03", "92233720368547758.07")
	insertBudget(t, ctx, db, two.ID, "2026-03", "92233720368547758.07")
	sourceBefore := listStoredBudgets(t, ctx, db, "2026-03")

	_, fields, err := store.CreateCarryForward(ctx, "2026-04", nil)
	if err != nil {
		t.Fatalf("CreateCarryForward() error = %v, want merged overflow fields", err)
	}
	want := []contract.FieldIssue{{Field: "carry_forward", Reason: "total must fit the supported amount range"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("no-override merged overflow fields = %#v, want %#v", fields, want)
	}
	if got := countBudgetRows(t, ctx, db, "2026-04"); got != 0 {
		t.Fatalf("April rows after no-override overflow = %d, want 0", got)
	}

	three := createCategory(t, ctx, categories, "Three")
	_, fields, err = store.CreateCarryForward(ctx, "2026-04", []budget.Allocation{
		{Category: three.Name, Amount: "0.01"},
	})
	if err != nil {
		t.Fatalf("CreateCarryForward(override) error = %v, want merged overflow fields", err)
	}
	want = []contract.FieldIssue{{Field: "overrides", Reason: "total must fit the supported amount range"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("override merged overflow fields = %#v, want %#v", fields, want)
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-03"), sourceBefore) {
		t.Fatalf("source month changed after merged overflow")
	}
	if got := countBudgetRows(t, ctx, db, "2026-04"); got != 0 {
		t.Fatalf("April rows after override overflow = %d, want 0", got)
	}
}

func TestCreateCarryForwardRollsBackAllInsertsOnFailure(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 4, 15, 12, 0))
	first := createCategory(t, ctx, categories, "First")
	second := createCategory(t, ctx, categories, "Second")
	insertBudget(t, ctx, db, first.ID, "2026-03", "1.00")
	insertBudget(t, ctx, db, second.ID, "2026-03", "2.00")
	sourceBefore := listStoredBudgets(t, ctx, db, "2026-03")
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TRIGGER fail_second_budget_insert
		BEFORE INSERT ON budgets
		WHEN NEW.category_id = %d AND NEW.month = '2026-04'
		BEGIN
			SELECT RAISE(ABORT, 'test budget insert failure');
		END
	`, second.ID)); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	_, fields, err := store.CreateCarryForward(ctx, "2026-04", nil)
	if len(fields) != 0 {
		t.Fatalf("rollback fields = %#v, want none", fields)
	}
	if err == nil {
		t.Fatal("CreateCarryForward() error = nil, want trigger failure")
	}
	if got := countBudgetRows(t, ctx, db, "2026-04"); got != 0 {
		t.Fatalf("April rows after failed insert = %d, want 0", got)
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-03"), sourceBefore) {
		t.Fatalf("source month changed after rollback")
	}
}

func TestCreateCarryForwardLeavesOtherMonthsUnchanged(t *testing.T) {
	ctx := context.Background()
	store, categories, db := openBudgetStore(t, torontoTime(t, 2026, 4, 15, 12, 0))
	groceries := createCategory(t, ctx, categories, "Groceries")
	health := createCategory(t, ctx, categories, "Health")
	insertBudget(t, ctx, db, groceries.ID, "2026-01", "10.00")
	insertBudget(t, ctx, db, health.ID, "2026-03", "20.00")
	januaryBefore := listStoredBudgets(t, ctx, db, "2026-01")
	marchBefore := listStoredBudgets(t, ctx, db, "2026-03")

	result, fields, err := store.CreateCarryForward(ctx, "2026-04", nil)
	if err != nil || len(fields) != 0 {
		t.Fatalf("CreateCarryForward() = %#v fields %#v error %v", result, fields, err)
	}
	if result.SourceMonth == nil || *result.SourceMonth != "2026-03" {
		t.Fatalf("source month = %#v, want 2026-03", result.SourceMonth)
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-01"), januaryBefore) {
		t.Fatalf("January changed after April create")
	}
	if !reflect.DeepEqual(listStoredBudgets(t, ctx, db, "2026-03"), marchBefore) {
		t.Fatalf("March changed after April create")
	}
}

func TestCreateTreatsOmittedAndEmptyOverridesAsEquivalent(t *testing.T) {
	ctx := context.Background()
	now := torontoTime(t, 2026, 4, 15, 12, 0)

	storeOmitted, categoriesOmitted, dbOmitted := openBudgetStore(t, now)
	groceries := createCategory(t, ctx, categoriesOmitted, "Groceries")
	insertBudget(t, ctx, dbOmitted, groceries.ID, "2026-03", "12.50")
	omitted, fields, err := storeOmitted.Create(ctx, budget.CreateInput{
		Month:        "2026-04",
		CarryForward: boolPtr(true),
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("omitted overrides = %#v fields %#v error %v", omitted, fields, err)
	}

	storeEmpty, categoriesEmpty, dbEmpty := openBudgetStore(t, now)
	emptyGroceries := createCategory(t, ctx, categoriesEmpty, "Groceries")
	insertBudget(t, ctx, dbEmpty, emptyGroceries.ID, "2026-03", "12.50")
	empty, fields, err := storeEmpty.Create(ctx, budget.CreateInput{
		Month:        "2026-04",
		CarryForward: boolPtr(true),
		Overrides:    []budget.Allocation{},
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("empty overrides = %#v fields %#v error %v", empty, fields, err)
	}

	if omitted.CreationMode != empty.CreationMode || omitted.TotalBudget != empty.TotalBudget {
		t.Fatalf("omitted %#v vs empty %#v headers differ", omitted, empty)
	}
	if omitted.SourceMonth == nil || empty.SourceMonth == nil || *omitted.SourceMonth != *empty.SourceMonth {
		t.Fatalf("source months omitted %#v empty %#v", omitted.SourceMonth, empty.SourceMonth)
	}
	if len(omitted.Budgets) != 1 || len(empty.Budgets) != 1 || omitted.Budgets[0].Category != empty.Budgets[0].Category || omitted.Budgets[0].Amount != empty.Budgets[0].Amount {
		t.Fatalf("copied rows omitted %#v empty %#v", omitted.Budgets, empty.Budgets)
	}

	viaHelper, fields, err := storeEmpty.CreateCarryForward(ctx, "2026-04", nil)
	if len(fields) != 0 {
		t.Fatalf("second create fields = %#v, want already-exists path", fields)
	}
	var alreadyExists *budget.AlreadyExistsError
	if !errors.As(err, &alreadyExists) {
		t.Fatalf("second CreateCarryForward() = %#v error %v, want already exists", viaHelper, err)
	}
}
