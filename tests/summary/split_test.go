package summary_test

import (
	"context"
	"testing"

	"github.com/jordanp2002/Local-Ledger/internal/summary"
	"github.com/jordanp2002/Local-Ledger/internal/transaction"
)

func TestSummariesCountSplitParentsAndSumAllocations(t *testing.T) {
	ctx := context.Background()
	fx := openSummaryStore(t, torontoTime(t, 2026, 10, 15, 12, 0))
	groceries := createCategory(t, ctx, fx.categories, "Groceries")
	household := createCategory(t, ctx, fx.categories, "Household")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-08", "100.00")
	insertBudget(t, ctx, fx.db, household.ID, "2026-08", "100.00")
	insertBudget(t, ctx, fx.db, groceries.ID, "2026-09", "100.00")
	insertBudget(t, ctx, fx.db, household.ID, "2026-09", "100.00")
	addSplit := func(date, groceriesAmount, householdAmount string) {
		t.Helper()
		result, fields, err := fx.transactions.AddSplit(ctx, transaction.AddSplitInput{
			Merchant: "Costco",
			Date:     stringPtr(date),
			Allocations: []transaction.AllocationInput{
				{Category: "Household", Amount: householdAmount},
				{Category: "Groceries", Amount: groceriesAmount},
			},
		})
		if err != nil || len(fields) != 0 {
			t.Fatalf("AddSplit(%s) = %#v fields %#v error %v", date, result, fields, err)
		}
	}
	addSplit("2026-08-30", "65.00", "20.00")
	addSplit("2026-09-30", "60.00", "25.00")

	monthly := mustMonthly(t, ctx, fx.summary, "2026-08")
	if monthly.TotalSpending != "85.00" || len(monthly.Categories) != 2 {
		t.Fatalf("monthly = %#v, want one parent total 85.00", monthly)
	}
	if monthly.Categories[0].Category != "Groceries" || monthly.Categories[0].Spending != "65.00" || monthly.Categories[1].Category != "Household" || monthly.Categories[1].Spending != "20.00" {
		t.Fatalf("monthly categories = %#v", monthly.Categories)
	}

	category := mustCategory(t, ctx, fx.summary, "Groceries", "2026-08")
	if category.TotalSpending != "65.00" || category.TransactionCount != 1 {
		t.Fatalf("category summary = %#v, want 65.00 and one parent", category)
	}

	spending := mustSpending(t, ctx, fx.summary, summary.SpendingInput{
		StartDate: stringPtr("2026-08-01"),
		EndDate:   stringPtr("2026-08-31"),
	})
	if spending.TotalSpending != "85.00" || spending.TransactionCount != 1 || len(spending.Categories) != 2 {
		t.Fatalf("spending = %#v, want one parent total 85.00", spending)
	}
	filtered := mustSpending(t, ctx, fx.summary, summary.SpendingInput{
		StartDate: stringPtr("2026-08-01"),
		EndDate:   stringPtr("2026-08-31"),
		Category:  stringPtr("Household"),
	})
	if filtered.TotalSpending != "20.00" || filtered.TransactionCount != 1 || filtered.Categories[0].TransactionCount != 1 {
		t.Fatalf("filtered spending = %#v, want one parent at 20.00", filtered)
	}

	top := mustTopMerchants(t, ctx, fx.summary, summary.TopMerchantsInput{
		StartDate: stringPtr("2026-08-01"),
		EndDate:   stringPtr("2026-08-31"),
	})
	if top.TotalSpending != "85.00" || top.TransactionCount != 1 || len(top.Merchants) != 1 || top.Merchants[0].Spending != "85.00" || top.Merchants[0].TransactionCount != 1 {
		t.Fatalf("top merchants = %#v, want one parent total 85.00", top)
	}

	series := mustSeries(t, ctx, fx.summary, summary.SeriesInput{FromMonth: "2026-08", ToMonth: "2026-09"})
	if len(series.Months) != 2 || series.Months[0].TotalSpending != "85.00" || series.Months[0].TransactionCount != 1 || series.Months[1].TotalSpending != "85.00" || series.Months[1].TransactionCount != 1 {
		t.Fatalf("series = %#v, want one parent per month at 85.00", series)
	}

	comparison, fields, err := fx.summary.Compare(ctx, "2026-08", "2026-09")
	if err != nil || len(fields) != 0 {
		t.Fatalf("Compare() = %#v fields %#v error %v", comparison, fields, err)
	}
	if comparison.From.TotalSpending != "85.00" || comparison.To.TotalSpending != "85.00" || len(comparison.Categories) != 2 {
		t.Fatalf("comparison = %#v, want allocation sums", comparison)
	}
}
