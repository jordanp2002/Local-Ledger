package savingsgoal_test

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/account"
	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/jordanp2002/local-finance-mcp/internal/database"
	"github.com/jordanp2002/local-finance-mcp/internal/savingsgoal"
)

var fixedNow = time.Date(2026, 9, 1, 14, 30, 0, 0, time.UTC)

func openTestStores(t *testing.T, now time.Time) (*sql.DB, *account.Store, *savingsgoal.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "finance.db")
	db, err := database.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	acctStore := &account.Store{DB: db, Now: func() time.Time { return now }}
	goalStore := &savingsgoal.Store{DB: db, Now: func() time.Time { return now }}
	return db, acctStore, goalStore
}

func strPtr(s string) *string {
	return &s
}

func mustCreateAccount(t *testing.T, s *account.Store, name, typ, balance string) contract.Account {
	t.Helper()
	res, fields, err := s.Create(context.Background(), account.CreateInput{Name: name, Type: typ, OpeningBalance: balance})
	if err != nil || len(fields) != 0 {
		t.Fatalf("create account: %v, %v", err, fields)
	}
	return res.Account
}

func TestCreateSavingsGoalSuccessEveryAccountType(t *testing.T) {
	_, acctStore, goalStore := openTestStores(t, fixedNow)

	types := []string{"checking", "savings", "cash", "other"}
	for _, typ := range types {
		acct := mustCreateAccount(t, acctStore, "Account-"+typ, typ, "1000.00")
		goalName := "Goal for " + typ
		goal, fields, err := goalStore.Create(context.Background(), savingsgoal.CreateInput{
			Name:         goalName,
			AccountID:    acct.ID,
			TargetAmount: "5000.00",
			TargetDate:   strPtr("2027-10-01"),
			Note:         strPtr("trip note"),
		})
		if err != nil || len(fields) != 0 {
			t.Fatalf("create goal for %s failed: %v, %v", typ, err, fields)
		}
		if goal.Name != goalName || goal.AccountID != acct.ID || goal.Account != acct.Name {
			t.Fatalf("unexpected goal info: %+v", goal)
		}
		if goal.TargetAmount != "5000.00" || goal.CurrentAmount != "0.00" || goal.RemainingAmount != "5000.00" || goal.ProgressPercent != "0.00" || goal.TargetReached {
			t.Fatalf("unexpected progress: %+v", goal)
		}
		if goal.Status != "active" || *goal.TargetDate != "2027-10-01" || *goal.Note != "trip note" {
			t.Fatalf("unexpected metadata: %+v", goal)
		}
		if goal.CreatedAt != "2026-09-01T14:30:00.000Z" || goal.UpdatedAt != "2026-09-01T14:30:00.000Z" {
			t.Fatalf("unexpected timestamps: %+v", goal)
		}
		if goal.CompletedAt != nil || goal.CancelledAt != nil {
			t.Fatalf("completed/cancelled should be nil: %+v", goal)
		}
	}
}

func TestCreateSavingsGoalValidationErrors(t *testing.T) {
	db, acctStore, goalStore := openTestStores(t, fixedNow)
	activeAcct := mustCreateAccount(t, acctStore, "Savings", "savings", "100.00")

	inactiveAcct := mustCreateAccount(t, acctStore, "ToDisable", "savings", "0.00")
	_, _, err := acctStore.Disable(context.Background(), inactiveAcct.ID)
	if err != nil {
		t.Fatalf("disable account: %v", err)
	}

	_, fields, _ := goalStore.Create(context.Background(), savingsgoal.CreateInput{
		Name:         "   ",
		AccountID:    activeAcct.ID,
		TargetAmount: "100.00",
	})
	if len(fields) == 0 || fields[0].Field != "name" {
		t.Fatalf("expected empty name error, got %v", fields)
	}

	_, fields, _ = goalStore.Create(context.Background(), savingsgoal.CreateInput{
		Name:         "Valid",
		AccountID:    -1,
		TargetAmount: "100.00",
	})
	if len(fields) == 0 || fields[0].Field != "account_id" {
		t.Fatalf("expected account_id error, got %v", fields)
	}

	_, _, err = goalStore.Create(context.Background(), savingsgoal.CreateInput{
		Name:         "Valid",
		AccountID:    9999,
		TargetAmount: "100.00",
	})
	var notFound *savingsgoal.AccountNotFoundError
	if !errors.As(err, &notFound) || notFound.ID != 9999 {
		t.Fatalf("expected AccountNotFoundError, got %v", err)
	}

	_, _, err = goalStore.Create(context.Background(), savingsgoal.CreateInput{
		Name:         "Valid",
		AccountID:    inactiveAcct.ID,
		TargetAmount: "100.00",
	})
	if !errors.Is(err, savingsgoal.ErrAccountInactive) {
		t.Fatalf("expected ErrAccountInactive, got %v", err)
	}

	for _, invalidAmount := range []string{"", "0", "0.00", "-5.00", "abc", "12.345"} {
		_, fields, _ := goalStore.Create(context.Background(), savingsgoal.CreateInput{
			Name:         "Valid",
			AccountID:    activeAcct.ID,
			TargetAmount: invalidAmount,
		})
		if len(fields) == 0 || fields[0].Field != "target_amount" {
			t.Fatalf("expected target_amount error for %q, got %v", invalidAmount, fields)
		}
	}

	for _, invalidDate := range []string{"", "   ", "invalid-date", "2026-08-31", "2026/10/01", "2026-02-30"} {
		_, fields, _ := goalStore.Create(context.Background(), savingsgoal.CreateInput{
			Name:         "Valid",
			AccountID:    activeAcct.ID,
			TargetAmount: "100.00",
			TargetDate:   strPtr(invalidDate),
		})
		if len(fields) == 0 || fields[0].Field != "target_date" {
			t.Fatalf("expected target_date error for %q, got %v", invalidDate, fields)
		}
	}

	_, fields, err = goalStore.Create(context.Background(), savingsgoal.CreateInput{
		Name:         "Japan Trip",
		AccountID:    activeAcct.ID,
		TargetAmount: "5000.00",
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("first create failed: %v, %v", err, fields)
	}

	_, _, err = goalStore.Create(context.Background(), savingsgoal.CreateInput{
		Name:         "japan trip",
		AccountID:    activeAcct.ID,
		TargetAmount: "3000.00",
	})
	var alreadyExists *savingsgoal.AlreadyExistsError
	if !errors.As(err, &alreadyExists) {
		t.Fatalf("expected AlreadyExistsError, got %v", err)
	}

	var budgetCount, txCount int
	_ = db.QueryRow("SELECT count(*) FROM budgets").Scan(&budgetCount)
	_ = db.QueryRow("SELECT count(*) FROM transactions").Scan(&txCount)
	if budgetCount != 0 || txCount != 0 {
		t.Fatalf("budgets or transactions were written: %d, %d", budgetCount, txCount)
	}
}

func TestUpdateSavingsGoalFields(t *testing.T) {
	_, acctStore, goalStore := openTestStores(t, fixedNow)
	acct1 := mustCreateAccount(t, acctStore, "Savings", "savings", "1000.00")
	acct2 := mustCreateAccount(t, acctStore, "Checking", "checking", "2000.00")

	created, _, err := goalStore.Create(context.Background(), savingsgoal.CreateInput{
		Name:         "Japan",
		AccountID:    acct1.ID,
		TargetAmount: "5000.00",
		TargetDate:   strPtr("2027-10-01"),
		Note:         strPtr("initial note"),
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}

	noOp, fields, err := goalStore.Update(context.Background(), savingsgoal.UpdateInput{
		ID:                created.ID,
		Name:              strPtr("Japan"),
		TargetAmount:      strPtr("5000.00"),
		TargetDate:        strPtr("2027-10-01"),
		TargetDatePresent: true,
		Note:              strPtr("initial note"),
		NotePresent:       true,
		AccountID:         &acct1.ID,
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("no-op update: %v, %v", err, fields)
	}
	if noOp.Changed {
		t.Fatal("expected Changed=false for no-op")
	}
	if noOp.Goal.UpdatedAt != created.UpdatedAt {
		t.Fatalf("updated_at changed on no-op: %s vs %s", noOp.Goal.UpdatedAt, created.UpdatedAt)
	}

	later := fixedNow.Add(time.Hour)
	goalStore.Now = func() time.Time { return later }

	updated, fields, err := goalStore.Update(context.Background(), savingsgoal.UpdateInput{
		ID:                created.ID,
		Name:              strPtr("Japan 2027"),
		TargetAmount:      strPtr("6000.00"),
		TargetDatePresent: true,
		NotePresent:       true,
		AccountID:         &acct2.ID,
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("update goal: %v, %v", err, fields)
	}
	if !updated.Changed {
		t.Fatal("expected Changed=true")
	}
	if updated.Goal.Name != "Japan 2027" || updated.Goal.AccountID != acct2.ID || updated.Goal.Account != acct2.Name {
		t.Fatalf("unexpected updated fields: %+v", updated.Goal)
	}
	if updated.Goal.TargetAmount != "6000.00" || updated.Goal.RemainingAmount != "6000.00" {
		t.Fatalf("unexpected updated amounts: %+v", updated.Goal)
	}
	if updated.Goal.TargetDate != nil || updated.Goal.Note != nil {
		t.Fatalf("cleared deadline and note should be nil: %+v", updated.Goal)
	}
	if updated.Goal.UpdatedAt != "2026-09-01T15:30:00.000Z" {
		t.Fatalf("unexpected updated_at: %s", updated.Goal.UpdatedAt)
	}
}

func TestUpdateSavingsGoalRestrictions(t *testing.T) {
	db, acctStore, goalStore := openTestStores(t, fixedNow)
	acct1 := mustCreateAccount(t, acctStore, "Savings", "savings", "1000.00")
	acct2 := mustCreateAccount(t, acctStore, "Checking", "checking", "2000.00")

	goal1, _, _ := goalStore.Create(context.Background(), savingsgoal.CreateInput{
		Name:         "Goal 1",
		AccountID:    acct1.ID,
		TargetAmount: "1000.00",
	})
	_, _, _ = goalStore.Create(context.Background(), savingsgoal.CreateInput{
		Name:         "Goal 2",
		AccountID:    acct1.ID,
		TargetAmount: "2000.00",
	})

	_, fields, _ := goalStore.Update(context.Background(), savingsgoal.UpdateInput{
		ID: goal1.ID,
	})
	if len(fields) == 0 {
		t.Fatal("expected error when no fields supplied")
	}

	_, _, err := goalStore.Update(context.Background(), savingsgoal.UpdateInput{
		ID:   goal1.ID,
		Name: strPtr("goal 2"),
	})
	var alreadyExists *savingsgoal.AlreadyExistsError
	if !errors.As(err, &alreadyExists) {
		t.Fatalf("expected collision error, got %v", err)
	}

	for _, invalidDate := range []string{"", "   "} {
		_, fields, err := goalStore.Update(context.Background(), savingsgoal.UpdateInput{
			ID: goal1.ID, TargetDate: strPtr(invalidDate), TargetDatePresent: true,
		})
		if err != nil || len(fields) == 0 || fields[0].Field != "target_date" {
			t.Fatalf("expected target_date error for %q, got %v, %v", invalidDate, fields, err)
		}
	}

	_, err = db.Exec(`
		INSERT INTO savings_goal_entries (goal_id, account_id, delta_hundredths, kind, date, idempotency_key, fingerprint)
		VALUES (?, ?, 50000, 'allocation', '2026-09-01', 'alloc-1', 'fp-1')
	`, goal1.ID, acct1.ID)
	if err != nil {
		t.Fatalf("seed goal entry: %v", err)
	}

	_, _, err = goalStore.Update(context.Background(), savingsgoal.UpdateInput{
		ID:        goal1.ID,
		AccountID: &acct2.ID,
	})
	var hasAllocations *savingsgoal.HasAllocationsError
	if !errors.As(err, &hasAllocations) {
		t.Fatalf("expected HasAllocationsError when changing account with allocations, got %v", err)
	}

	res, _, err := goalStore.Update(context.Background(), savingsgoal.UpdateInput{
		ID:           goal1.ID,
		TargetAmount: strPtr("400.00"),
	})
	if err != nil {
		t.Fatalf("update target below current amount: %v", err)
	}
	if res.Goal.TargetAmount != "400.00" || res.Goal.CurrentAmount != "500.00" || res.Goal.RemainingAmount != "0.00" {
		t.Fatalf("unexpected progress after target below current: %+v", res.Goal)
	}
	if !res.Goal.TargetReached || res.Goal.ProgressPercent != "100.00" || res.Goal.AmountAboveTarget == nil || *res.Goal.AmountAboveTarget != "100.00" {
		t.Fatalf("unexpected overfunded status: %+v", res.Goal)
	}

	_, err = db.Exec("UPDATE savings_goals SET status = 'completed', completed_at = '2026-09-02T10:00:00.000Z' WHERE id = ?", goal1.ID)
	if err != nil {
		t.Fatalf("mark completed: %v", err)
	}
	_, _, err = goalStore.Update(context.Background(), savingsgoal.UpdateInput{
		ID:   goal1.ID,
		Name: strPtr("New Name"),
	})
	var closed *savingsgoal.ClosedError
	if !errors.As(err, &closed) || closed.Status != "completed" {
		t.Fatalf("expected ClosedError for completed goal, got %v", err)
	}

	_, err = db.Exec("UPDATE savings_goals SET status = 'cancelled', cancelled_at = '2026-09-02T10:00:00.000Z' WHERE id = ?", goal1.ID)
	if err != nil {
		t.Fatalf("mark cancelled: %v", err)
	}
	_, _, err = goalStore.Update(context.Background(), savingsgoal.UpdateInput{
		ID:   goal1.ID,
		Name: strPtr("New Name"),
	})
	if !errors.As(err, &closed) || closed.Status != "cancelled" {
		t.Fatalf("expected ClosedError for cancelled goal, got %v", err)
	}
}

func TestProgressCalculations(t *testing.T) {
	db, acctStore, goalStore := openTestStores(t, fixedNow)
	acct := mustCreateAccount(t, acctStore, "Savings", "savings", "10000.00")

	goal, _, err := goalStore.Create(context.Background(), savingsgoal.CreateInput{
		Name:         "Progress Goal",
		AccountID:    acct.ID,
		TargetAmount: "300.00",
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}

	list, _, _ := goalStore.List(context.Background(), savingsgoal.ListInput{Name: strPtr("Progress Goal")})
	if list[0].ProgressPercent != "0.00" || list[0].RemainingAmount != "300.00" || list[0].TargetReached {
		t.Fatalf("zero progress incorrect: %+v", list[0])
	}

	_, err = db.Exec(`
		INSERT INTO savings_goal_entries (goal_id, account_id, delta_hundredths, kind, date, idempotency_key, fingerprint)
		VALUES (?, ?, 10000, 'allocation', '2026-09-01', 'alloc-p1', 'fp-p1')
	`, goal.ID, acct.ID)
	if err != nil {
		t.Fatalf("seed partial allocation: %v", err)
	}
	list, _, _ = goalStore.List(context.Background(), savingsgoal.ListInput{Name: strPtr("Progress Goal")})
	if list[0].CurrentAmount != "100.00" || list[0].RemainingAmount != "200.00" || list[0].ProgressPercent != "33.33" || list[0].TargetReached {
		t.Fatalf("partial progress incorrect: %+v", list[0])
	}

	_, err = db.Exec(`
		INSERT INTO savings_goal_entries (goal_id, account_id, delta_hundredths, kind, date, idempotency_key, fingerprint)
		VALUES (?, ?, 20000, 'allocation', '2026-09-01', 'alloc-p2', 'fp-p2')
	`, goal.ID, acct.ID)
	if err != nil {
		t.Fatalf("seed exact allocation: %v", err)
	}
	list, _, _ = goalStore.List(context.Background(), savingsgoal.ListInput{Name: strPtr("Progress Goal")})
	if list[0].CurrentAmount != "300.00" || list[0].RemainingAmount != "0.00" || list[0].ProgressPercent != "100.00" || !list[0].TargetReached || list[0].AmountAboveTarget != nil {
		t.Fatalf("exact progress incorrect: %+v", list[0])
	}

	_, err = db.Exec(`
		INSERT INTO savings_goal_entries (goal_id, account_id, delta_hundredths, kind, date, idempotency_key, fingerprint)
		VALUES (?, ?, 10000, 'allocation', '2026-09-01', 'alloc-p3', 'fp-p3')
	`, goal.ID, acct.ID)
	if err != nil {
		t.Fatalf("seed above allocation: %v", err)
	}
	list, _, _ = goalStore.List(context.Background(), savingsgoal.ListInput{Name: strPtr("Progress Goal")})
	if list[0].CurrentAmount != "400.00" || list[0].RemainingAmount != "0.00" || list[0].ProgressPercent != "100.00" || !list[0].TargetReached {
		t.Fatalf("above target incorrect: %+v", list[0])
	}
	if list[0].AmountAboveTarget == nil || *list[0].AmountAboveTarget != "100.00" {
		t.Fatalf("amount_above_target incorrect: %v", list[0].AmountAboveTarget)
	}
}

func TestListSavingsGoalsFiltersAndOrdering(t *testing.T) {
	db, acctStore, goalStore := openTestStores(t, fixedNow)
	acct1 := mustCreateAccount(t, acctStore, "Savings 1", "savings", "5000.00")
	acct2 := mustCreateAccount(t, acctStore, "Savings 2", "savings", "5000.00")

	_, _, _ = goalStore.Create(context.Background(), savingsgoal.CreateInput{Name: "Active No Date B", AccountID: acct1.ID, TargetAmount: "100.00"})
	_, _, _ = goalStore.Create(context.Background(), savingsgoal.CreateInput{Name: "Active Early Date", AccountID: acct1.ID, TargetAmount: "100.00", TargetDate: strPtr("2026-10-01")})
	_, _, _ = goalStore.Create(context.Background(), savingsgoal.CreateInput{Name: "Active Later Date", AccountID: acct2.ID, TargetAmount: "100.00", TargetDate: strPtr("2027-01-01")})
	_, _, _ = goalStore.Create(context.Background(), savingsgoal.CreateInput{Name: "Active No Date A", AccountID: acct1.ID, TargetAmount: "100.00"})

	g5, _, _ := goalStore.Create(context.Background(), savingsgoal.CreateInput{Name: "Completed Goal", AccountID: acct1.ID, TargetAmount: "100.00"})
	_, err := db.Exec("UPDATE savings_goals SET status = 'completed', completed_at = '2026-09-05T12:00:00.000Z' WHERE id = ?", g5.ID)
	if err != nil {
		t.Fatalf("update completed: %v", err)
	}

	g6, _, _ := goalStore.Create(context.Background(), savingsgoal.CreateInput{Name: "Cancelled Goal", AccountID: acct2.ID, TargetAmount: "100.00"})
	_, err = db.Exec("UPDATE savings_goals SET status = 'cancelled', cancelled_at = '2026-09-06T12:00:00.000Z' WHERE id = ?", g6.ID)
	if err != nil {
		t.Fatalf("update cancelled: %v", err)
	}

	activeOnly, _, err := goalStore.List(context.Background(), savingsgoal.ListInput{})
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(activeOnly) != 4 {
		t.Fatalf("expected 4 active goals, got %d", len(activeOnly))
	}
	expectedActiveOrder := []string{
		"Active Early Date",
		"Active Later Date",
		"Active No Date A",
		"Active No Date B",
	}
	for i, want := range expectedActiveOrder {
		if activeOnly[i].Name != want {
			t.Fatalf("active index %d = %s, want %s", i, activeOnly[i].Name, want)
		}
	}

	allGoals, _, err := goalStore.List(context.Background(), savingsgoal.ListInput{IncludeClosed: true})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(allGoals) != 6 {
		t.Fatalf("expected 6 goals, got %d", len(allGoals))
	}
	if allGoals[4].Name != "Cancelled Goal" || allGoals[5].Name != "Completed Goal" {
		t.Fatalf("closed order unexpected: 4=%s, 5=%s", allGoals[4].Name, allGoals[5].Name)
	}

	filteredAccount, _, _ := goalStore.List(context.Background(), savingsgoal.ListInput{AccountID: &acct2.ID, IncludeClosed: true})
	if len(filteredAccount) != 2 {
		t.Fatalf("expected 2 goals for acct2, got %d", len(filteredAccount))
	}

	completedOnly, _, _ := goalStore.List(context.Background(), savingsgoal.ListInput{Status: strPtr("completed")})
	if len(completedOnly) != 1 || completedOnly[0].Name != "Completed Goal" {
		t.Fatalf("expected 1 completed goal, got %v", completedOnly)
	}
}

func TestAccountDisableGoalGuard(t *testing.T) {
	db, acctStore, goalStore := openTestStores(t, fixedNow)
	acct := mustCreateAccount(t, acctStore, "Guarded", "savings", "0.00")

	goal, _, _ := goalStore.Create(context.Background(), savingsgoal.CreateInput{
		Name:         "Guard Goal",
		AccountID:    acct.ID,
		TargetAmount: "500.00",
	})

	_, _, err := acctStore.Disable(context.Background(), acct.ID)
	var goalActive *account.GoalActiveError
	if !errors.As(err, &goalActive) {
		t.Fatalf("expected GoalActiveError for active goal, got %v", err)
	}

	_, _ = db.Exec("UPDATE savings_goals SET status = 'completed', completed_at = '2026-09-02T10:00:00.000Z' WHERE id = ?", goal.ID)
	_, _, err = acctStore.Disable(context.Background(), acct.ID)
	if err != nil {
		t.Fatalf("disable account with completed goal and 0 allocations should succeed: %v", err)
	}

	acct2 := mustCreateAccount(t, acctStore, "GuardedWithAlloc", "savings", "0.00")
	goal2, _, _ := goalStore.Create(context.Background(), savingsgoal.CreateInput{
		Name:         "Allocated Completed",
		AccountID:    acct2.ID,
		TargetAmount: "500.00",
	})
	_, _ = db.Exec("UPDATE savings_goals SET status = 'completed', completed_at = '2026-09-02T10:00:00.000Z' WHERE id = ?", goal2.ID)
	_, _ = db.Exec(`
		INSERT INTO savings_goal_entries (goal_id, account_id, delta_hundredths, kind, date, idempotency_key, fingerprint)
		VALUES (?, ?, 10000, 'allocation', '2026-09-01', 'guard-alloc', 'guard-fp')
	`, goal2.ID, acct2.ID)

	_, _, err = acctStore.Disable(context.Background(), acct2.ID)
	if !errors.As(err, &goalActive) {
		t.Fatalf("expected GoalActiveError for completed goal with retained allocations, got %v", err)
	}
}

func TestProgressCheckedArithmeticOverflow(t *testing.T) {
	db, acctStore, goalStore := openTestStores(t, fixedNow)
	acct := mustCreateAccount(t, acctStore, "Savings", "savings", "1000.00")

	goal, _, err := goalStore.Create(context.Background(), savingsgoal.CreateInput{
		Name:         "Huge Target",
		AccountID:    acct.ID,
		TargetAmount: "90000000000000000.00",
	})
	if err != nil {
		t.Fatalf("create huge target goal: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO savings_goal_entries (goal_id, account_id, delta_hundredths, kind, date, idempotency_key, fingerprint)
		VALUES (?, ?, ?, 'allocation', '2026-09-01', 'huge-alloc', 'huge-fp')
	`, goal.ID, acct.ID, int64(1_000_000_000_000_000))
	if err != nil {
		t.Fatalf("seed huge entry: %v", err)
	}

	_, _, err = goalStore.List(context.Background(), savingsgoal.ListInput{Name: strPtr("Huge Target")})
	if err == nil || err.Error() != "progress calculation overflow" {
		t.Fatalf("expected multiplication overflow, got %v", err)
	}

	negativeGoal, _, err := goalStore.Create(context.Background(), savingsgoal.CreateInput{
		Name: "Negative Total", AccountID: acct.ID, TargetAmount: "1.00",
	})
	if err != nil {
		t.Fatalf("create negative-total goal: %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO savings_goal_entries (goal_id, account_id, delta_hundredths, kind, date, idempotency_key, fingerprint)
		VALUES (?, ?, ?, 'release', '2026-09-01', 'huge-release', 'huge-release-fp')
	`, negativeGoal.ID, acct.ID, int64(-math.MaxInt64))
	if err != nil {
		t.Fatalf("seed huge release: %v", err)
	}
	_, _, err = goalStore.List(context.Background(), savingsgoal.ListInput{Name: strPtr("Negative Total")})
	if err == nil || err.Error() != "progress calculation overflow" {
		t.Fatalf("expected subtraction overflow, got %v", err)
	}
}
