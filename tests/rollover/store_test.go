package rollover_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jordanp2002/Local-Ledger/internal/budget"
	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/recurring"
	"github.com/jordanp2002/Local-Ledger/internal/rollover"
	"github.com/jordanp2002/Local-Ledger/internal/summary"
	"github.com/jordanp2002/Local-Ledger/internal/transaction"
)

func TestCreateListSummaryAndCarryForwardKeepAdjustmentsSeparate(t *testing.T) {
	ctx := context.Background()
	fx := openFixture(t)
	createCategory(t, ctx, fx.categories, "Groceries")
	createBudget(t, ctx, fx.budgets, "2026-03", "Groceries", "300.00")
	transactionResult := addTransaction(t, ctx, fx.transactions, "320.00", "Groceries", "2026-03-15", nil)

	created := createRollover(t, ctx, fx.rollovers, "2026-03", "groceries", "20.00", int64Ptr(transactionResult.Transaction.ID))
	if created.Rollover.SourceMonth != "2026-03" || created.Rollover.TargetMonth != "2026-04" || created.Rollover.Amount != "20.00" {
		t.Fatalf("created rollover = %#v", created.Rollover)
	}
	if created.Rollover.SourceTransactionID == nil || *created.Rollover.SourceTransactionID != transactionResult.Transaction.ID {
		t.Fatalf("source transaction link = %#v", created.Rollover.SourceTransactionID)
	}
	if created.Rollover.Status != rollover.StatusPending {
		t.Fatalf("created rollover status = %q, want pending", created.Rollover.Status)
	}

	listed, fields, err := fx.rollovers.List(ctx, rollover.ListInput{SourceMonth: stringPtr("2026-03")})
	if err != nil || len(fields) != 0 {
		t.Fatalf("list pending rollover = %#v fields=%#v err=%v", listed, fields, err)
	}
	if len(listed.Rollovers) != 1 || listed.Rollovers[0].Status != rollover.StatusPending {
		t.Fatalf("pending list = %#v", listed)
	}

	createBudget(t, ctx, fx.budgets, "2026-04", "Groceries", "300.00")
	listed, fields, err = fx.rollovers.List(ctx, rollover.ListInput{TargetMonth: stringPtr("2026-04")})
	if err != nil || len(fields) != 0 || len(listed.Rollovers) != 1 || listed.Rollovers[0].Status != rollover.StatusApplied {
		t.Fatalf("applied list = %#v fields=%#v err=%v", listed, fields, err)
	}

	summaries := &summary.Store{DB: fx.db}
	april, fields, err := summaries.Monthly(ctx, "2026-04")
	if err != nil || len(fields) != 0 {
		t.Fatalf("April summary = %#v fields=%#v err=%v", april, fields, err)
	}
	if april.TotalBaseBudget != "300.00" || april.TotalRolloverAdjustment != "-20.00" || april.TotalBudget != "280.00" {
		t.Fatalf("April totals = (%s, %s, %s)", april.TotalBaseBudget, april.TotalRolloverAdjustment, april.TotalBudget)
	}
	if len(april.Categories) != 1 || april.Categories[0].BaseBudget != "300.00" || april.Categories[0].RolloverAdjustment != "-20.00" || april.Categories[0].Budget != "280.00" {
		t.Fatalf("April category = %#v", april.Categories)
	}

	mayBudget := createBudget(t, ctx, fx.budgets, "2026-05", "Groceries", "300.00")
	if mayBudget.Budgets[0].Amount != "300.00" {
		t.Fatalf("explicit May budget = %#v", mayBudget)
	}
	// Recreate the carry-forward regression with a month whose source is April.
	if _, fields, err := fx.budgets.CreateCarryForward(ctx, "2026-06", nil); err != nil || len(fields) != 0 {
		t.Fatalf("carry forward June: fields=%#v err=%v", fields, err)
	}
	maySummary, fields, err := summaries.Monthly(ctx, "2026-05")
	if err != nil || len(fields) != 0 || maySummary.TotalBaseBudget != "300.00" || maySummary.TotalRolloverAdjustment != "0.00" || maySummary.TotalBudget != "300.00" {
		t.Fatalf("May summary = %#v fields=%#v err=%v", maySummary, fields, err)
	}

	removed, fields, err := fx.rollovers.Remove(ctx, created.Rollover.ID)
	if err != nil || len(fields) != 0 || removed.ID != created.Rollover.ID {
		t.Fatalf("remove rollover = %#v fields=%#v err=%v", removed, fields, err)
	}
	april, fields, err = summaries.Monthly(ctx, "2026-04")
	if err != nil || len(fields) != 0 || april.TotalBaseBudget != "300.00" || april.TotalRolloverAdjustment != "0.00" || april.TotalBudget != "300.00" {
		t.Fatalf("April after removal = %#v fields=%#v err=%v", april, fields, err)
	}
	if got := countRollovers(t, ctx, fx.db); got != 0 {
		t.Fatalf("rollover rows after removal = %d, want 0", got)
	}
	_, fields, err = fx.rollovers.Remove(ctx, created.Rollover.ID)
	if err == nil || len(fields) != 0 || !errors.Is(err, rollover.ErrNotFound) {
		t.Fatalf("repeated remove = fields=%#v err=%v, want not found", fields, err)
	}
}

func TestRolloverEligibilityAndAuditLinkValidation(t *testing.T) {
	ctx := context.Background()
	fx := openFixture(t)
	createCategory(t, ctx, fx.categories, "Groceries")
	createCategory(t, ctx, fx.categories, "Dining")

	_, fields, err := fx.rollovers.Create(ctx, rollover.CreateInput{SourceMonth: "2026-03", Category: "Groceries", Amount: "1.00"})
	if err == nil || len(fields) != 0 || !errors.Is(err, rollover.ErrNotEligible) {
		t.Fatalf("missing budget rollover = fields=%#v err=%v", fields, err)
	}

	createBudget(t, ctx, fx.budgets, "2026-03", "Groceries", "100.00")
	under := addTransaction(t, ctx, fx.transactions, "99.99", "Groceries", "2026-03-01", nil)
	_, fields, err = fx.rollovers.Create(ctx, rollover.CreateInput{SourceMonth: "2026-03", Category: "Groceries", Amount: "0.01"})
	if err == nil || len(fields) != 0 || !errors.Is(err, rollover.ErrNotEligible) {
		t.Fatalf("exact-budget eligibility = fields=%#v err=%v", fields, err)
	}

	_, fields, err = fx.rollovers.Create(ctx, rollover.CreateInput{SourceMonth: "2026-03", Category: "Groceries", Amount: "1.00", SourceTransactionID: int64Ptr(9999)})
	if err == nil || len(fields) != 0 || !errors.Is(err, rollover.ErrTransactionNotFound) {
		t.Fatalf("missing source transaction = fields=%#v err=%v", fields, err)
	}

	wrongMonth := addTransaction(t, ctx, fx.transactions, "10.00", "Groceries", "2026-02-01", nil)
	_, fields, err = fx.rollovers.Create(ctx, rollover.CreateInput{SourceMonth: "2026-03", Category: "Groceries", Amount: "0.01", SourceTransactionID: int64Ptr(wrongMonth.Transaction.ID)})
	if err == nil || len(fields) != 0 || !errors.Is(err, rollover.ErrNotEligible) {
		t.Fatalf("wrong source month = fields=%#v err=%v", fields, err)
	}

	wrongCategory := addTransaction(t, ctx, fx.transactions, "10.00", "Dining", "2026-03-02", nil)
	_, fields, err = fx.rollovers.Create(ctx, rollover.CreateInput{SourceMonth: "2026-03", Category: "Groceries", Amount: "0.01", SourceTransactionID: int64Ptr(wrongCategory.Transaction.ID)})
	if err == nil || len(fields) != 0 || !errors.Is(err, rollover.ErrNotEligible) {
		t.Fatalf("wrong source category = fields=%#v err=%v", fields, err)
	}

	eligible := addTransaction(t, ctx, fx.transactions, "1.01", "Groceries", "2026-03-03", nil)
	first := createRollover(t, ctx, fx.rollovers, "2026-03", "Groceries", "0.50", int64Ptr(eligible.Transaction.ID))
	second := createRollover(t, ctx, fx.rollovers, "2026-03", "Groceries", "0.50", nil)
	if first.Rollover.ID == second.Rollover.ID {
		t.Fatal("partial rollovers reused ID")
	}
	_, fields, err = fx.rollovers.Create(ctx, rollover.CreateInput{SourceMonth: "2026-03", Category: "Groceries", Amount: "0.01"})
	if err == nil || len(fields) != 0 || !errors.Is(err, rollover.ErrNotEligible) {
		t.Fatalf("amount above eligibility = fields=%#v err=%v", fields, err)
	}

	if _, err := fx.db.ExecContext(ctx, `DELETE FROM transactions WHERE id = ?`, eligible.Transaction.ID); err != nil {
		t.Fatalf("delete linked transaction: %v", err)
	}
	listed, fields, err := fx.rollovers.List(ctx, rollover.ListInput{})
	if err != nil || len(fields) != 0 || len(listed.Rollovers) != 2 {
		t.Fatalf("list after linked transaction removal = %#v fields=%#v err=%v", listed, fields, err)
	}
	if listed.Rollovers[0].SourceTransactionID != nil {
		t.Fatalf("deleted linked transaction ID = %#v, want null", listed.Rollovers[0].SourceTransactionID)
	}
	_ = under
}

func TestTransactionOffersAreInformationalAndReplayDoesNotWriteRollover(t *testing.T) {
	ctx := context.Background()
	fx := openFixture(t)
	createCategory(t, ctx, fx.categories, "Groceries")
	createBudget(t, ctx, fx.budgets, "2026-03", "Groceries", "100.00")

	key := stringPtr("purchase-1")
	first := addTransaction(t, ctx, fx.transactions, "120.00", "Groceries", "2026-03-15", key)
	if len(first.RolloverOffers) != 1 {
		t.Fatalf("first offers = %#v, want one", first.RolloverOffers)
	}
	offer := first.RolloverOffers[0]
	if offer.SourceMonth != "2026-03" || offer.TargetMonth != "2026-04" || offer.BaseBudget != "100.00" || offer.AvailableBudget != "100.00" || offer.SpendingAfter != "120.00" || offer.EligibleRollover != "20.00" {
		t.Fatalf("offer = %#v", offer)
	}
	if got := countRollovers(t, ctx, fx.db); got != 0 {
		t.Fatalf("transaction write created %d rollovers", got)
	}

	replay := addTransaction(t, ctx, fx.transactions, "120.00", "Groceries", "2026-03-15", key)
	if !replay.IdempotentReplay || replay.Transaction.ID != first.Transaction.ID {
		t.Fatalf("replay = %#v", replay)
	}
	if replay.RolloverOffers == nil || len(replay.RolloverOffers) != 0 {
		t.Fatalf("replay offers = %#v, want non-nil empty", replay.RolloverOffers)
	}
	if got := countRollovers(t, ctx, fx.db); got != 0 {
		t.Fatalf("replay created %d rollovers", got)
	}
}

func TestMutationGuardsRejectUnsafeCorrectionsAtomically(t *testing.T) {
	ctx := context.Background()
	fx := openFixture(t)
	createCategory(t, ctx, fx.categories, "Groceries")
	createBudget(t, ctx, fx.budgets, "2026-03", "Groceries", "100.00")
	transactionResult := addTransaction(t, ctx, fx.transactions, "120.00", "Groceries", "2026-03-15", nil)
	created := createRollover(t, ctx, fx.rollovers, "2026-03", "Groceries", "20.00", nil)

	_, fields, err := fx.budgets.Set(ctx, "2026-03", []budget.Allocation{{Category: "Groceries", Amount: "110.00"}})
	if err == nil || len(fields) != 0 || !errors.Is(err, rollover.ErrDependencyConflict) {
		t.Fatalf("unsafe budget increase = fields=%#v err=%v", fields, err)
	}
	assertBudgetAmount(t, ctx, fx.db, "2026-03", "100.00")

	_, fields, err = fx.transactions.Update(ctx, transaction.UpdateInput{ID: transactionResult.Transaction.ID, Amount: stringPtr("105.00")})
	if err == nil || len(fields) != 0 || !errors.Is(err, rollover.ErrDependencyConflict) {
		t.Fatalf("unsafe transaction reduction = fields=%#v err=%v", fields, err)
	}
	assertTransactionAmount(t, ctx, fx.db, transactionResult.Transaction.ID, "120.00")

	_, fields, err = fx.transactions.Remove(ctx, transactionResult.Transaction.ID)
	if err == nil || len(fields) != 0 || !errors.Is(err, rollover.ErrDependencyConflict) {
		t.Fatalf("unsafe transaction removal = fields=%#v err=%v", fields, err)
	}
	assertTransactionAmount(t, ctx, fx.db, transactionResult.Transaction.ID, "120.00")

	if _, fields, err := fx.rollovers.Remove(ctx, created.Rollover.ID); err != nil || len(fields) != 0 {
		t.Fatalf("safe rollover removal: fields=%#v err=%v", fields, err)
	}
	if _, fields, err := fx.budgets.Set(ctx, "2026-03", []budget.Allocation{{Category: "Groceries", Amount: "110.00"}}); err != nil || len(fields) != 0 {
		t.Fatalf("safe budget increase: fields=%#v err=%v", fields, err)
	}
}

func TestDependencyChainRequiresNewestRolloverRemovalFirst(t *testing.T) {
	ctx := context.Background()
	fx := openFixture(t)
	createCategory(t, ctx, fx.categories, "Groceries")
	createBudget(t, ctx, fx.budgets, "2026-03", "Groceries", "100.00")
	createBudget(t, ctx, fx.budgets, "2026-04", "Groceries", "100.00")
	addTransaction(t, ctx, fx.transactions, "120.00", "Groceries", "2026-03-15", nil)
	addTransaction(t, ctx, fx.transactions, "120.00", "Groceries", "2026-04-15", nil)
	first := createRollover(t, ctx, fx.rollovers, "2026-03", "Groceries", "20.00", nil)
	second := createRollover(t, ctx, fx.rollovers, "2026-04", "Groceries", "40.00", nil)

	if _, fields, err := fx.rollovers.Remove(ctx, first.Rollover.ID); err == nil || len(fields) != 0 || !errors.Is(err, rollover.ErrDependencyConflict) {
		t.Fatalf("oldest removal = fields=%#v err=%v, want conflict", fields, err)
	}
	if got := countRollovers(t, ctx, fx.db); got != 2 {
		t.Fatalf("rows after rejected dependency removal = %d, want 2", got)
	}
	if _, fields, err := fx.rollovers.Remove(ctx, second.Rollover.ID); err != nil || len(fields) != 0 {
		t.Fatalf("newest removal: fields=%#v err=%v", fields, err)
	}
	if _, fields, err := fx.rollovers.Remove(ctx, first.Rollover.ID); err != nil || len(fields) != 0 {
		t.Fatalf("oldest removal after newest: fields=%#v err=%v", fields, err)
	}
}

func TestRolloverListPaginationAndStableOrder(t *testing.T) {
	ctx := context.Background()
	fx := openFixture(t)
	createCategory(t, ctx, fx.categories, "Zeta")
	createCategory(t, ctx, fx.categories, "Alpha")
	if result, fields, err := fx.budgets.CreateExplicit(ctx, "2026-03", []budget.Allocation{
		{Category: "Zeta", Amount: "100.00"},
		{Category: "Alpha", Amount: "100.00"},
	}); err != nil || len(fields) != 0 || result.TotalBudget != "200.00" {
		t.Fatalf("create multi-category budget = %#v fields=%#v err=%v", result, fields, err)
	}
	addTransaction(t, ctx, fx.transactions, "110.00", "Zeta", "2026-03-01", nil)
	addTransaction(t, ctx, fx.transactions, "110.00", "Alpha", "2026-03-01", nil)
	createRollover(t, ctx, fx.rollovers, "2026-03", "Zeta", "10.00", nil)
	createRollover(t, ctx, fx.rollovers, "2026-03", "Alpha", "10.00", nil)

	limit := int64(1)
	first, fields, err := fx.rollovers.List(ctx, rollover.ListInput{Limit: &limit})
	if err != nil || len(fields) != 0 || len(first.Rollovers) != 1 || first.Page.Total != 2 || !first.Page.HasMore {
		t.Fatalf("first page = %#v fields=%#v err=%v", first, fields, err)
	}
	if first.Rollovers[0].Category != "Alpha" {
		t.Fatalf("stable first category = %q, want Alpha", first.Rollovers[0].Category)
	}
	offset := int64(1)
	second, fields, err := fx.rollovers.List(ctx, rollover.ListInput{Limit: &limit, Offset: &offset})
	if err != nil || len(fields) != 0 || len(second.Rollovers) != 1 || second.Rollovers[0].Category != "Zeta" || second.Page.HasMore {
		t.Fatalf("second page = %#v fields=%#v err=%v", second, fields, err)
	}
}

func assertBudgetAmount(t *testing.T, ctx context.Context, db *sql.DB, month, want string) {
	t.Helper()
	var amount int64
	if err := db.QueryRowContext(ctx, `SELECT amount_hundredths FROM budgets WHERE month = ?`, month).Scan(&amount); err != nil {
		t.Fatalf("query budget: %v", err)
	}
	formatted, err := contract.FormatAmount(amount)
	if err != nil || formatted != want {
		t.Fatalf("budget amount = %q err=%v, want %q", formatted, err, want)
	}
}

func assertTransactionAmount(t *testing.T, ctx context.Context, db *sql.DB, id int64, want string) {
	t.Helper()
	var amount int64
	if err := db.QueryRowContext(ctx, `SELECT a.amount_hundredths FROM transaction_allocations AS a WHERE a.transaction_id = ?`, id).Scan(&amount); err != nil {
		t.Fatalf("query transaction allocation: %v", err)
	}
	formatted, err := contract.FormatAmount(amount)
	if err != nil || formatted != want {
		t.Fatalf("transaction amount = %q err=%v, want %q", formatted, err, want)
	}
}

func TestRolloverOfferSlicesAreNonNilForNoChangeOperations(t *testing.T) {
	ctx := context.Background()
	fx := openFixture(t)
	createCategory(t, ctx, fx.categories, "Groceries")
	createBudget(t, ctx, fx.budgets, "2026-03", "Groceries", "100.00")
	result := addTransaction(t, ctx, fx.transactions, "10.00", "Groceries", "2026-03-15", nil)
	if result.RolloverOffers == nil || !reflect.DeepEqual(result.RolloverOffers, []contract.RolloverOffer{}) {
		t.Fatalf("no-change offers = %#v, want nonnil empty", result.RolloverOffers)
	}
}

func TestSplitBatchAndRecurringWritesReturnOffersWithoutCreatingRollovers(t *testing.T) {
	ctx := context.Background()
	fx := openFixture(t)
	createCategory(t, ctx, fx.categories, "Groceries")
	createCategory(t, ctx, fx.categories, "Dining")
	if result, fields, err := fx.budgets.CreateExplicit(ctx, "2026-03", []budget.Allocation{
		{Category: "Groceries", Amount: "100.00"},
		{Category: "Dining", Amount: "100.00"},
	}); err != nil || len(fields) != 0 || result.TotalBudget != "200.00" {
		t.Fatalf("create budget = %#v fields=%#v err=%v", result, fields, err)
	}

	split, fields, err := fx.transactions.AddSplit(ctx, transaction.AddSplitInput{
		Merchant: "Costco",
		Date:     stringPtr("2026-03-15"),
		Allocations: []transaction.AllocationInput{
			{Category: "Groceries", Amount: "110.00"},
			{Category: "Dining", Amount: "110.00"},
		},
	})
	if err != nil || len(fields) != 0 || len(split.RolloverOffers) != 2 {
		t.Fatalf("split = %#v fields=%#v err=%v", split, fields, err)
	}

	batch, fields, err := fx.transactions.AddBatch(ctx, transaction.AddBatchInput{
		IdempotencyKey: "batch-1",
		Transactions: []transaction.BatchRow{
			{Amount: "1.00", Merchant: "Merchant Groceries", Category: stringPtr("Groceries"), Date: "2026-03-16"},
			{Amount: "1.00", Merchant: "Merchant Dining", Category: stringPtr("Dining"), Date: "2026-03-17"},
		},
	})
	if err != nil || len(fields) != 0 || len(batch.RolloverOffers) != 2 {
		t.Fatalf("batch = %#v fields=%#v err=%v", batch, fields, err)
	}
	if got := countRollovers(t, ctx, fx.db); got != 0 {
		t.Fatalf("split/batch writes created %d rollovers", got)
	}

	// A recurring materialization uses the same ordinary transaction writer and
	// therefore participates in offer calculation without an implicit rollover.
	recurringStore := &recurring.Store{DB: fx.db, Now: func() time.Time {
		return time.Date(2026, time.March, 20, 10, 0, 0, 0, time.UTC)
	}}
	if _, fields, err := recurringStore.Create(ctx, recurring.CreateInput{
		Merchant: "Subscription", Amount: "1.00", Category: "Groceries", DayOfMonth: 1,
	}); err != nil || len(fields) != 0 {
		t.Fatalf("create recurring = fields=%#v err=%v", fields, err)
	}
	materialized, err := recurringStore.MaterializeDue(ctx)
	if err != nil || len(materialized.RolloverOffers) != 1 {
		t.Fatalf("materialized = %#v err=%v", materialized, err)
	}
	if got := countRollovers(t, ctx, fx.db); got != 0 {
		t.Fatalf("recurring write created %d rollovers", got)
	}
}
