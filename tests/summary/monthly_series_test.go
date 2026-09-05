package summary_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/summary"
)

func TestSeriesValidationOrderAndRangeLimit(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	before := loadSnapshot(t, ctx, fx.db)

	tests := []struct {
		name  string
		input summary.SeriesInput
		want  []contract.FieldIssue
	}{
		{
			name: "malformed months before category",
			input: summary.SeriesInput{
				FromMonth: "2026-8",
				ToMonth:   "2026-13",
				Category:  stringPtr(" \t "),
			},
			want: []contract.FieldIssue{
				{Field: "from_month", Reason: "must be a valid YYYY-MM month"},
				{Field: "to_month", Reason: "must be a valid YYYY-MM month"},
				{Field: "category", Reason: "must not be empty"},
			},
		},
		{
			name: "order before category",
			input: summary.SeriesInput{
				FromMonth: "2026-08",
				ToMonth:   "2026-07",
				Category:  stringPtr(" \t "),
			},
			want: []contract.FieldIssue{
				{Field: "to_month", Reason: "must be on or after from_month"},
				{Field: "category", Reason: "must not be empty"},
			},
		},
		{
			name: "span before category",
			input: summary.SeriesInput{
				FromMonth: "2025-01",
				ToMonth:   "2027-01",
				Category:  stringPtr("Pharmacy\x00"),
			},
			want: []contract.FieldIssue{
				{Field: "to_month", Reason: "must be at most 24 months after from_month"},
				{Field: "category", Reason: "must not contain NUL characters"},
			},
		},
		{
			name: "category cannot combine with matrix",
			input: summary.SeriesInput{
				FromMonth:         "2026-08",
				ToMonth:           "2026-08",
				Category:          stringPtr("Groceries"),
				IncludeCategories: true,
			},
			want: []contract.FieldIssue{{Field: "category", Reason: "cannot be combined with include_categories"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, fields, err := fx.summary.Series(ctx, tt.input)
			if err != nil {
				t.Fatalf("Series() error = %v, want field issues", err)
			}
			if !reflect.DeepEqual(fields, tt.want) {
				t.Fatalf("Series(%#v) fields = %#v, want %#v", tt.input, fields, tt.want)
			}
		})
	}
	assertUnchanged(t, ctx, fx.db, before)
}

func TestSeriesAllowsEqualMonthsAndAtMost24ContiguousRows(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	createCategory(t, ctx, fx.categories, "Groceries")
	addTransaction(t, ctx, fx.transactions, "12.00", "Metro", "Groceries", "2026-08-14")

	one := mustSeries(t, ctx, fx.summary, summary.SeriesInput{
		FromMonth: "2026-08",
		ToMonth:   "2026-08",
	})
	if one.FromMonth != "2026-08" || one.ToMonth != "2026-08" || one.Category != nil || len(one.Months) != 1 {
		t.Fatalf("equal-month result = %#v", one)
	}
	if !reflect.DeepEqual(one.Months[0], summary.SeriesMonth{
		Month: "2026-08", TotalBaseBudget: nil, TotalSinkingFundOpening: nil, TotalRolloverAdjustment: nil, TotalBudget: nil, TotalSpending: "12.00", Remaining: nil,
		SpentOfBudget: nil, TransactionCount: 1,
	}) {
		t.Fatalf("equal-month row = %#v", one.Months[0])
	}
	wide := mustSeries(t, ctx, fx.summary, summary.SeriesInput{
		FromMonth: "2024-01",
		ToMonth:   "2025-12",
	})
	if len(wide.Months) != 24 {
		t.Fatalf("24-month row count = %d, want 24", len(wide.Months))
	}
	assertContiguousZeroSeries(t, wide, "2024-01")
}

func TestSeriesRejects25Months(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	before := loadSnapshot(t, ctx, fx.db)

	_, fields, err := fx.summary.Series(ctx, summary.SeriesInput{
		FromMonth: "2025-01",
		ToMonth:   "2027-01",
	})
	if err != nil {
		t.Fatalf("Series() error = %v, want field issue", err)
	}
	want := []contract.FieldIssue{{Field: "to_month", Reason: "must be at most 24 months after from_month"}}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("25-month fields = %#v, want %#v", fields, want)
	}
	assertUnchanged(t, ctx, fx.db, before)
}

func TestSeriesSnapshotAndSpendingOnlyRowsAreContiguous(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	createCategory(t, ctx, fx.categories, "Health")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-01", "500.00")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-03", "0.00")
	addTransaction(t, ctx, fx.transactions, "90.00", "Metro", "Groceries", "2026-01-10")
	addTransaction(t, ctx, fx.transactions, "20.00", "Shoppers", "Health", "2026-01-11")
	addTransaction(t, ctx, fx.transactions, "20.00", "Shoppers", "Health", "2026-02-11")
	addTransaction(t, ctx, fx.transactions, "5.00", "Shoppers", "Health", "2026-04-11")
	before := loadSnapshot(t, ctx, fx.db)

	result := mustSeries(t, ctx, fx.summary, summary.SeriesInput{
		FromMonth: "2026-01",
		ToMonth:   "2026-04",
	})
	want := []summary.SeriesMonth{
		{
			Month: "2026-01", TotalBaseBudget: stringPtr("500.00"), TotalSinkingFundOpening: stringPtr("0.00"), TotalRolloverAdjustment: stringPtr("0.00"), TotalBudget: stringPtr("500.00"), TotalSpending: "110.00",
			Remaining: stringPtr("390.00"), SpentOfBudget: stringPtr("22.00"), TransactionCount: 2,
		},
		{
			Month: "2026-02", TotalBudget: nil, TotalSpending: "20.00",
			Remaining: nil, SpentOfBudget: nil, TransactionCount: 1,
		},
		{
			Month: "2026-03", TotalBaseBudget: stringPtr("0.00"), TotalSinkingFundOpening: stringPtr("0.00"), TotalRolloverAdjustment: stringPtr("0.00"), TotalBudget: stringPtr("0.00"), TotalSpending: "0.00",
			Remaining: stringPtr("0.00"), SpentOfBudget: nil, TransactionCount: 0,
		},
		{
			Month: "2026-04", TotalBudget: nil, TotalSpending: "5.00",
			Remaining: nil, SpentOfBudget: nil, TransactionCount: 1,
		},
	}
	if result.Category != nil || !reflect.DeepEqual(result.Months, want) {
		t.Fatalf("series = %#v, want months %#v", result, want)
	}
	assertUnchanged(t, ctx, fx.db, before)
}

func TestSeriesAllowsFutureMonthsAndReturnsZeroRows(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))

	result := mustSeries(t, ctx, fx.summary, summary.SeriesInput{
		FromMonth: "2027-01",
		ToMonth:   "2027-02",
	})
	if result.FromMonth != "2027-01" || result.ToMonth != "2027-02" || len(result.Months) != 2 {
		t.Fatalf("future result = %#v", result)
	}
	assertContiguousZeroSeries(t, result, "2027-01")
}

func TestSeriesCategoryFilterIncludesInactiveAndUsesStoredName(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 10, 15, 12, 0))
	dining := createCategory(t, ctx, fx.categories, "Dining")
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	insertBudget(t, ctx, fx.db, dining.ID, "2026-08", "150.00")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-10", "300.00")
	addTransaction(t, ctx, fx.transactions, "40.00", "Cafe", "Dining", "2026-08-10")
	addTransaction(t, ctx, fx.transactions, "5.00", "Cafe", "Dining", "2026-09-10")
	addTransaction(t, ctx, fx.transactions, "7.00", "Cafe", "Dining", "2026-10-10")
	if _, changed, _, err := fx.categories.Disable(ctx, "Dining"); err != nil || !changed {
		t.Fatalf("Disable(Dining) = changed %v, error %v", changed, err)
	}
	before := loadSnapshot(t, ctx, fx.db)

	result := mustSeries(t, ctx, fx.summary, summary.SeriesInput{
		FromMonth: "2026-08",
		ToMonth:   "2026-10",
		Category:  stringPtr(" dining "),
	})
	if result.Category == nil || *result.Category != "Dining" {
		t.Fatalf("category echo = %#v, want stored inactive name", result.Category)
	}
	want := []summary.SeriesMonth{
		{Month: "2026-08", TotalBaseBudget: stringPtr("150.00"), TotalSinkingFundOpening: stringPtr("0.00"), TotalRolloverAdjustment: stringPtr("0.00"), TotalBudget: stringPtr("150.00"), TotalSpending: "40.00", Remaining: stringPtr("110.00"), SpentOfBudget: stringPtr("26.66"), TransactionCount: 1},
		{Month: "2026-09", TotalBudget: nil, TotalSpending: "5.00", Remaining: nil, SpentOfBudget: nil, TransactionCount: 1},
		{Month: "2026-10", TotalBaseBudget: stringPtr("0.00"), TotalSinkingFundOpening: stringPtr("0.00"), TotalRolloverAdjustment: stringPtr("0.00"), TotalBudget: stringPtr("0.00"), TotalSpending: "7.00", Remaining: stringPtr("-7.00"), SpentOfBudget: nil, TransactionCount: 1},
	}
	if !reflect.DeepEqual(result.Months, want) {
		t.Fatalf("inactive category series = %#v, want %#v", result.Months, want)
	}
	assertUnchanged(t, ctx, fx.db, before)
}

func TestSeriesMissingCategoryReturnsCategoryNotFound(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	before := loadSnapshot(t, ctx, fx.db)

	_, fields, err := fx.summary.Series(ctx, summary.SeriesInput{
		FromMonth: "2026-08",
		ToMonth:   "2026-08",
		Category:  stringPtr(" Pharmacy "),
	})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	var missing *summary.CategoryNotFoundError
	if err == nil || !errors.As(err, &missing) {
		t.Fatalf("error = %v, want CategoryNotFoundError", err)
	}
	if missing.Requested != "Pharmacy" {
		t.Fatalf("requested = %q, want Pharmacy", missing.Requested)
	}
	if len(missing.ActiveCategories) != 1 || missing.ActiveCategories[0].ID != groceries.ID {
		t.Fatalf("active categories = %#v, want Groceries", missing.ActiveCategories)
	}
	assertUnchanged(t, ctx, fx.db, before)
}

func TestSeriesPresentSnapshotMatchesExistingSummaries(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	dining := createCategory(t, ctx, fx.categories, "Dining")
	createCategory(t, ctx, fx.categories, "Health")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-08", "500.00")
	insertBudget(t, ctx, fx.db, dining.ID, "2026-08", "150.00")
	addTransaction(t, ctx, fx.transactions, "90.00", "Metro", "Groceries", "2026-08-14")
	addTransaction(t, ctx, fx.transactions, "30.00", "Cafe", "Dining", "2026-08-15")
	addTransaction(t, ctx, fx.transactions, "20.00", "Shoppers", "Health", "2026-08-15")
	before := loadSnapshot(t, ctx, fx.db)

	monthly := mustMonthly(t, ctx, fx.summary, "2026-08")
	series := mustSeries(t, ctx, fx.summary, summary.SeriesInput{
		FromMonth: "2026-08",
		ToMonth:   "2026-08",
	})
	if len(series.Months) != 1 {
		t.Fatalf("series months = %#v", series.Months)
	}
	row := series.Months[0]
	if row.Month != monthly.Month || row.TotalBudget == nil || *row.TotalBudget != monthly.TotalBudget || row.TotalSpending != monthly.TotalSpending || row.Remaining == nil || *row.Remaining != monthly.Remaining || !reflect.DeepEqual(row.SpentOfBudget, monthly.SpentOfBudget) || row.TransactionCount != 3 {
		t.Fatalf("series row = %#v, monthly = %#v", row, monthly)
	}

	category := mustCategory(t, ctx, fx.summary, " groceries ", "2026-08")
	filtered := mustSeries(t, ctx, fx.summary, summary.SeriesInput{
		FromMonth: "2026-08",
		ToMonth:   "2026-08",
		Category:  stringPtr(" groceries "),
	})
	if filtered.Category == nil || *filtered.Category != category.Category || len(filtered.Months) != 1 {
		t.Fatalf("filtered identity = %#v, category = %#v", filtered, category)
	}
	filteredRow := filtered.Months[0]
	if filteredRow.TotalBudget == nil || *filteredRow.TotalBudget != category.Budget || filteredRow.TotalSpending != category.TotalSpending || filteredRow.Remaining == nil || *filteredRow.Remaining != category.Remaining || !reflect.DeepEqual(filteredRow.SpentOfBudget, category.SpentOfBudget) || filteredRow.TransactionCount != category.TransactionCount {
		t.Fatalf("filtered row = %#v, category = %#v", filteredRow, category)
	}
	assertUnchanged(t, ctx, fx.db, before)
}

func TestSeriesIncludeCategoriesBuildsRectangularAxisAndShares(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 2, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	dining := createCategory(t, ctx, fx.categories, "Dining")
	createCategory(t, ctx, fx.categories, "Health")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-01", "100.00")
	insertBudget(t, ctx, fx.db, dining.ID, "2026-02", "200.00")
	addTransaction(t, ctx, fx.transactions, "50.00", "Shoppers", "Health", "2026-01-10")

	result := mustSeries(t, ctx, fx.summary, summary.SeriesInput{
		FromMonth: "2026-01", ToMonth: "2026-02", IncludeCategories: true,
	})
	if !result.IncludeCategories || len(result.Months) != 2 {
		t.Fatalf("series header = %#v", result)
	}
	for _, month := range result.Months {
		if len(month.Categories) != 3 || month.Categories[0].Category != "Dining" || month.Categories[1].Category != "Groceries" || month.Categories[2].Category != "Health" {
			t.Fatalf("%s category axis = %#v", month.Month, month.Categories)
		}
	}
	january := result.Months[0].Categories
	if january[0].BaseBudget == nil || *january[0].BaseBudget != "0.00" || january[1].ShareOfBaseBudget == nil || *january[1].ShareOfBaseBudget != "100.00" || january[2].ShareOfSpending == nil || *january[2].ShareOfSpending != "100.00" {
		t.Fatalf("January categories = %#v", january)
	}
	february := result.Months[1].Categories
	if february[0].ShareOfBaseBudget == nil || *february[0].ShareOfBaseBudget != "100.00" || february[2].Spending != "0.00" || february[2].ShareOfSpending != nil {
		t.Fatalf("February categories = %#v", february)
	}
}

func TestSeriesSpendingOverflowIsReturnedAndDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	insertRawTransaction(t, ctx, fx.db, "Huge", math.MaxInt64, "2026-08-01", groceries.ID)
	insertRawTransaction(t, ctx, fx.db, "One", 1, "2026-08-02", groceries.ID)
	before := loadSnapshot(t, ctx, fx.db)

	_, fields, err := fx.summary.Series(ctx, summary.SeriesInput{
		FromMonth: "2026-08",
		ToMonth:   "2026-08",
	})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	if err == nil {
		t.Fatal("Series() spending overflow error = nil")
	}
	var missing *summary.CategoryNotFoundError
	if errors.As(err, &missing) {
		t.Fatalf("overflow mapped to category_not_found: %v", err)
	}
	assertUnchanged(t, ctx, fx.db, before)
}

func TestSeriesSpentOfBudgetOverflowIsReturnedAndDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 8, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-08", "1.00")
	insertRawTransaction(t, ctx, fx.db, "Huge", math.MaxInt64, "2026-08-01", groceries.ID)
	before := loadSnapshot(t, ctx, fx.db)

	_, fields, err := fx.summary.Series(ctx, summary.SeriesInput{
		FromMonth: "2026-08",
		ToMonth:   "2026-08",
	})
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want none", fields)
	}
	if err == nil {
		t.Fatal("Series() spent_of_budget overflow error = nil")
	}
	var missing *summary.NotFoundError
	if errors.As(err, &missing) {
		t.Fatalf("overflow mapped to monthly not found: %v", err)
	}
	assertUnchanged(t, ctx, fx.db, before)
}

func mustSeries(t *testing.T, ctx context.Context, store *summary.Store, in summary.SeriesInput) summary.SeriesResult {
	t.Helper()
	result, fields, err := store.Series(ctx, in)
	if err != nil || len(fields) != 0 {
		t.Fatalf("Series(%#v) = %#v fields %#v error %v", in, result, fields, err)
	}
	if result.Months == nil {
		t.Fatal("Series() months is nil, want non-nil slice")
	}
	return result
}

func assertContiguousZeroSeries(t *testing.T, result summary.SeriesResult, from string) {
	t.Helper()
	start, err := time.Parse("2006-01", from)
	if err != nil {
		t.Fatalf("parse start month %q: %v", from, err)
	}
	for i, row := range result.Months {
		wantMonth := start.AddDate(0, i, 0).Format("2006-01")
		if row.Month != wantMonth || row.TotalSpending != "0.00" || row.TransactionCount != 0 || row.TotalBudget != nil || row.Remaining != nil || row.SpentOfBudget != nil {
			t.Fatalf("zero row[%d] = %#v, want month %s with spending/count zero and null budget fields", i, row, wantMonth)
		}
	}
}
