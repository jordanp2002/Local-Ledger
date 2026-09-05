package recurring_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/jordanp2002/Local-Ledger/internal/recurring"
)

func TestPreviewDueEmptyDatabase(t *testing.T) {
	ctx := context.Background()
	store, _, _ := openRecurringStore(t)

	res, err := store.PreviewDue(ctx)
	if err != nil {
		t.Fatalf("PreviewDue error = %v", err)
	}

	if res.AsOfDate != "2026-08-30" {
		t.Errorf("as_of_date = %q, want 2026-08-30", res.AsOfDate)
	}
	if res.Month != "2026-08" {
		t.Errorf("month = %q, want 2026-08", res.Month)
	}
	if res.TotalAmount != "0.00" {
		t.Errorf("total_amount = %q, want 0.00", res.TotalAmount)
	}
	if len(res.DueTransactions) != 0 {
		t.Errorf("due_transactions length = %d, want 0", len(res.DueTransactions))
	}
	if len(res.Blocked) != 0 {
		t.Errorf("blocked length = %d, want 0", len(res.Blocked))
	}
}

func TestPreviewDueNothingDueFutureOnly(t *testing.T) {
	ctx := context.Background()
	store, catStore, _ := openRecurringStore(t)
	toronto := time.FixedZone("EDT", -4*60*60)
	store.Now = func() time.Time { return time.Date(2026, 8, 10, 10, 0, 0, 0, toronto) }

	mustCreateCategory(t, ctx, catStore, "Entertainment")
	_, _, err := store.Create(ctx, recurring.CreateInput{
		Merchant:   "Netflix",
		Amount:     "22.99",
		Category:   "Entertainment",
		DayOfMonth: 15,
	})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}

	res, err := store.PreviewDue(ctx)
	if err != nil {
		t.Fatalf("PreviewDue error = %v", err)
	}

	if res.AsOfDate != "2026-08-10" || res.Month != "2026-08" {
		t.Fatalf("preview dates = (%q, %q), want (2026-08-10, 2026-08)", res.AsOfDate, res.Month)
	}
	if res.TotalAmount != "0.00" {
		t.Errorf("total_amount = %q, want 0.00", res.TotalAmount)
	}
	if len(res.DueTransactions) != 0 || len(res.Blocked) != 0 {
		t.Fatalf("due = %d, blocked = %d, want both 0", len(res.DueTransactions), len(res.Blocked))
	}
}

func TestPreviewDueExactDueDateAndLatePreview(t *testing.T) {
	ctx := context.Background()
	store, catStore, _ := openRecurringStore(t)
	toronto := time.FixedZone("EDT", -4*60*60)
	store.Now = func() time.Time { return time.Date(2026, 8, 20, 12, 0, 0, 0, toronto) }

	mustCreateCategory(t, ctx, catStore, "Housing")
	mustCreateCategory(t, ctx, catStore, "Utilities")

	_, _, err := store.Create(ctx, recurring.CreateInput{
		Merchant:   "Rent",
		Amount:     "1500.00",
		Category:   "Housing",
		DayOfMonth: 1,
	})
	if err != nil {
		t.Fatalf("Create Rent error = %v", err)
	}

	_, _, err = store.Create(ctx, recurring.CreateInput{
		Merchant:   "Electric",
		Amount:     "85.50",
		Category:   "Utilities",
		DayOfMonth: 20,
		Note:       stringPointer("Monthly electric bill"),
	})
	if err != nil {
		t.Fatalf("Create Electric error = %v", err)
	}

	res, err := store.PreviewDue(ctx)
	if err != nil {
		t.Fatalf("PreviewDue error = %v", err)
	}

	if res.AsOfDate != "2026-08-20" || res.Month != "2026-08" {
		t.Fatalf("dates = (%q, %q)", res.AsOfDate, res.Month)
	}
	if res.TotalAmount != "1585.50" {
		t.Errorf("total_amount = %q, want 1585.50", res.TotalAmount)
	}
	if len(res.DueTransactions) != 2 {
		t.Fatalf("due length = %d, want 2", len(res.DueTransactions))
	}

	rent := res.DueTransactions[0]
	if rent.Merchant != "Rent" || rent.Amount != "1500.00" || rent.DueDate != "2026-08-01" || rent.Note != nil {
		t.Errorf("rent = %+v", rent)
	}

	electric := res.DueTransactions[1]
	if electric.Merchant != "Electric" || electric.Amount != "85.50" || electric.DueDate != "2026-08-20" {
		t.Errorf("electric = %+v", electric)
	}
	if electric.Note == nil || *electric.Note != "Monthly electric bill" {
		t.Errorf("electric note = %v, want Monthly electric bill", electric.Note)
	}
}

func TestPreviewDueMultipleRowsAndStableOrdering(t *testing.T) {
	ctx := context.Background()
	store, catStore, _ := openRecurringStore(t)
	toronto := time.FixedZone("EDT", -4*60*60)
	store.Now = func() time.Time { return time.Date(2026, 8, 30, 12, 0, 0, 0, toronto) }

	mustCreateCategory(t, ctx, catStore, "Entertainment")
	mustCreateCategory(t, ctx, catStore, "General")

	mustCreateCategory(t, ctx, catStore, "Health")

	mustCreateRecurring(t, ctx, store, recurring.CreateInput{Merchant: "Spotify", Amount: "10.99", Category: "Entertainment", DayOfMonth: 15})
	firstApple := mustCreateRecurring(t, ctx, store, recurring.CreateInput{Merchant: "Apple Music", Amount: "10.99", Category: "Entertainment", DayOfMonth: 15})
	mustCreateRecurring(t, ctx, store, recurring.CreateInput{Merchant: "Gym", Amount: "50.00", Category: "Health", DayOfMonth: 5})
	mustCreateRecurring(t, ctx, store, recurring.CreateInput{Merchant: "apple one", Amount: "19.95", Category: "Entertainment", DayOfMonth: 15})
	secondApple := mustCreateRecurring(t, ctx, store, recurring.CreateInput{Merchant: "apple music", Amount: "10.99", Category: "Entertainment", DayOfMonth: 15})

	res, err := store.PreviewDue(ctx)
	if err != nil {
		t.Fatalf("PreviewDue error = %v", err)
	}

	if len(res.DueTransactions) != 5 {
		t.Fatalf("due transactions = %d, want 5", len(res.DueTransactions))
	}

	if res.DueTransactions[0].Merchant != "Gym" {
		t.Errorf("item 0 merchant = %q, want Gym", res.DueTransactions[0].Merchant)
	}
	if res.DueTransactions[1].Merchant != "Apple Music" {
		t.Errorf("item 1 merchant = %q, want Apple Music", res.DueTransactions[1].Merchant)
	}
	if res.DueTransactions[1].RecurringTransactionID != firstApple.RecurringTransaction.ID ||
		res.DueTransactions[2].RecurringTransactionID != secondApple.RecurringTransaction.ID {
		t.Errorf("case-insensitive merchant tie ordered by IDs %d, %d; want %d, %d",
			res.DueTransactions[1].RecurringTransactionID,
			res.DueTransactions[2].RecurringTransactionID,
			firstApple.RecurringTransaction.ID,
			secondApple.RecurringTransaction.ID,
		)
	}
	if res.DueTransactions[3].Merchant != "apple one" {
		t.Errorf("item 3 merchant = %q, want apple one", res.DueTransactions[3].Merchant)
	}
	if res.DueTransactions[4].Merchant != "Spotify" {
		t.Errorf("item 4 merchant = %q, want Spotify", res.DueTransactions[4].Merchant)
	}
	if res.TotalAmount != "102.92" {
		t.Errorf("total_amount = %q, want 102.92", res.TotalAmount)
	}
}

func TestPreviewDueDisabledTemplatesExcluded(t *testing.T) {
	ctx := context.Background()
	store, catStore, _ := openRecurringStore(t)
	mustCreateCategory(t, ctx, catStore, "Entertainment")

	created, _, err := store.Create(ctx, recurring.CreateInput{
		Merchant:   "Netflix",
		Amount:     "22.99",
		Category:   "Entertainment",
		DayOfMonth: 15,
	})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}

	_, _, err = store.Disable(ctx, created.RecurringTransaction.ID)
	if err != nil {
		t.Fatalf("Disable error = %v", err)
	}

	res, err := store.PreviewDue(ctx)
	if err != nil {
		t.Fatalf("PreviewDue error = %v", err)
	}
	if len(res.DueTransactions) != 0 || len(res.Blocked) != 0 {
		t.Fatalf("due = %d, blocked = %d, want 0 for disabled template", len(res.DueTransactions), len(res.Blocked))
	}
}

func TestPreviewDueMonthEndClampingFebruaryNonLeapYear(t *testing.T) {
	ctx := context.Background()
	store, catStore, _ := openRecurringStore(t)
	toronto := time.FixedZone("EST", -5*60*60)

	mustCreateCategory(t, ctx, catStore, "Housing")
	_, _, err := store.Create(ctx, recurring.CreateInput{
		Merchant:   "Rent",
		Amount:     "1500.00",
		Category:   "Housing",
		DayOfMonth: 31,
	})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}

	store.Now = func() time.Time { return time.Date(2026, 2, 27, 10, 0, 0, 0, toronto) }
	res, err := store.PreviewDue(ctx)
	if err != nil {
		t.Fatalf("PreviewDue on Feb 27 error = %v", err)
	}
	if len(res.DueTransactions) != 0 {
		t.Errorf("due on Feb 27 = %d, want 0", len(res.DueTransactions))
	}

	store.Now = func() time.Time { return time.Date(2026, 2, 28, 10, 0, 0, 0, toronto) }
	res, err = store.PreviewDue(ctx)
	if err != nil {
		t.Fatalf("PreviewDue on Feb 28 error = %v", err)
	}
	if len(res.DueTransactions) != 1 {
		t.Fatalf("due on Feb 28 = %d, want 1", len(res.DueTransactions))
	}
	if res.DueTransactions[0].DueDate != "2026-02-28" {
		t.Errorf("due date = %q, want 2026-02-28", res.DueTransactions[0].DueDate)
	}
}

func TestPreviewDueMonthEndClamping30DayMonth(t *testing.T) {
	ctx := context.Background()
	store, catStore, _ := openRecurringStore(t)
	toronto := time.FixedZone("EDT", -4*60*60)

	mustCreateCategory(t, ctx, catStore, "Utilities")
	mustCreateRecurring(t, ctx, store, recurring.CreateInput{
		Merchant:   "Internet",
		Amount:     "60.00",
		Category:   "Utilities",
		DayOfMonth: 31,
	})

	store.Now = func() time.Time { return time.Date(2026, 4, 30, 10, 0, 0, 0, toronto) }
	res, err := store.PreviewDue(ctx)
	if err != nil {
		t.Fatalf("PreviewDue error = %v", err)
	}
	if len(res.DueTransactions) != 1 {
		t.Fatalf("due on April 30 = %d, want 1", len(res.DueTransactions))
	}
	if res.DueTransactions[0].DueDate != "2026-04-30" {
		t.Errorf("due date = %q, want 2026-04-30", res.DueTransactions[0].DueDate)
	}
}

func TestPreviewDueLeapYear(t *testing.T) {
	ctx := context.Background()
	store, catStore, _ := openRecurringStore(t)
	toronto := time.FixedZone("EST", -5*60*60)

	mustCreateCategory(t, ctx, catStore, "Housing")
	mustCreateRecurring(t, ctx, store, recurring.CreateInput{
		Merchant:   "Rent",
		Amount:     "1500.00",
		Category:   "Housing",
		DayOfMonth: 31,
	})

	store.Now = func() time.Time { return time.Date(2024, 2, 28, 10, 0, 0, 0, toronto) }
	res, err := store.PreviewDue(ctx)
	if err != nil {
		t.Fatalf("PreviewDue on Feb 28 leap year error = %v", err)
	}
	if len(res.DueTransactions) != 0 {
		t.Errorf("due on Feb 28 2024 = %d, want 0", len(res.DueTransactions))
	}

	store.Now = func() time.Time { return time.Date(2024, 2, 29, 10, 0, 0, 0, toronto) }
	res, err = store.PreviewDue(ctx)
	if err != nil {
		t.Fatalf("PreviewDue on Feb 29 leap year error = %v", err)
	}
	if len(res.DueTransactions) != 1 {
		t.Fatalf("due on Feb 29 2024 = %d, want 1", len(res.DueTransactions))
	}
	if res.DueTransactions[0].DueDate != "2024-02-29" {
		t.Errorf("due date = %q, want 2024-02-29", res.DueTransactions[0].DueDate)
	}
}

func TestPreviewDueInactiveCategoryBlockers(t *testing.T) {
	ctx := context.Background()
	store, catStore, _ := openRecurringStore(t)
	toronto := time.FixedZone("EDT", -4*60*60)
	store.Now = func() time.Time { return time.Date(2026, 8, 30, 10, 0, 0, 0, toronto) }

	mustCreateCategory(t, ctx, catStore, "Entertainment")
	mustCreateCategory(t, ctx, catStore, "Fitness")
	mustCreateCategory(t, ctx, catStore, "Hobbies")

	mustCreateRecurring(t, ctx, store, recurring.CreateInput{
		Merchant:   "Netflix",
		Amount:     "22.99",
		Category:   "Entertainment",
		DayOfMonth: 15,
	})

	createdFitness := mustCreateRecurring(t, ctx, store, recurring.CreateInput{
		Merchant:   "Gym",
		Amount:     "50.00",
		Category:   "Fitness",
		DayOfMonth: 10,
	})

	store.Now = func() time.Time { return time.Date(2026, 8, 25, 10, 0, 0, 0, toronto) }
	mustCreateRecurring(t, ctx, store, recurring.CreateInput{
		Merchant:   "Art Class",
		Amount:     "40.00",
		Category:   "Hobbies",
		DayOfMonth: 31,
	})

	mustDisableCategory(t, ctx, catStore, "Fitness")
	mustDisableCategory(t, ctx, catStore, "Hobbies")

	res, err := store.PreviewDue(ctx)
	if err != nil {
		t.Fatalf("PreviewDue error = %v", err)
	}

	if len(res.DueTransactions) != 1 {
		t.Fatalf("due length = %d, want 1", len(res.DueTransactions))
	}
	if res.DueTransactions[0].Merchant != "Netflix" {
		t.Errorf("due merchant = %q, want Netflix", res.DueTransactions[0].Merchant)
	}
	if res.TotalAmount != "22.99" {
		t.Errorf("total_amount = %q, want 22.99 (excluding blocked)", res.TotalAmount)
	}

	if len(res.Blocked) != 1 {
		t.Fatalf("blocked length = %d, want 1 (Gym only)", len(res.Blocked))
	}
	blockedGym := res.Blocked[0]
	if blockedGym.RecurringTransactionID != createdFitness.RecurringTransaction.ID {
		t.Errorf("blocked id = %d, want %d", blockedGym.RecurringTransactionID, createdFitness.RecurringTransaction.ID)
	}
	if blockedGym.Merchant != "Gym" || blockedGym.Category != "Fitness" || blockedGym.DueDate != "2026-08-10" || blockedGym.Reason != "category_inactive" {
		t.Errorf("blocked gym = %+v", blockedGym)
	}
}

func TestPreviewDueExistingRunExcluded(t *testing.T) {
	ctx := context.Background()
	store, catStore, db := openRecurringStore(t)
	cat := mustCreateCategory(t, ctx, catStore, "Entertainment")

	created, _, err := store.Create(ctx, recurring.CreateInput{
		Merchant:   "Netflix",
		Amount:     "22.99",
		Category:   "Entertainment",
		DayOfMonth: 15,
	})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}

	resInsert, err := db.ExecContext(ctx, `
		INSERT INTO transactions (merchant, date, created_at, updated_at)
		VALUES ('Netflix', '2026-08-15', '2026-08-15T12:00:00.000Z', '2026-08-15T12:00:00.000Z')
	`)
	if err != nil {
		t.Fatalf("insert transaction error = %v", err)
	}
	txnID, _ := resInsert.LastInsertId()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO transaction_allocations (transaction_id, category_id, amount_hundredths)
		VALUES (?, ?, 2299)
	`, txnID, cat.ID); err != nil {
		t.Fatalf("insert transaction allocation error = %v", err)
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO recurring_transaction_runs (recurring_transaction_id, month, transaction_id)
		VALUES (?, '2026-08', ?)
	`, created.RecurringTransaction.ID, txnID)
	if err != nil {
		t.Fatalf("insert run error = %v", err)
	}

	res, err := store.PreviewDue(ctx)
	if err != nil {
		t.Fatalf("PreviewDue error = %v", err)
	}
	if len(res.DueTransactions) != 0 {
		t.Fatalf("due length = %d, want 0 for already-run template", len(res.DueTransactions))
	}
}

func TestPreviewDueRemovedGeneratedTransactionRunExcluded(t *testing.T) {
	ctx := context.Background()
	store, catStore, db := openRecurringStore(t)
	mustCreateCategory(t, ctx, catStore, "Entertainment")

	created, _, err := store.Create(ctx, recurring.CreateInput{
		Merchant:   "Netflix",
		Amount:     "22.99",
		Category:   "Entertainment",
		DayOfMonth: 15,
	})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO recurring_transaction_runs (recurring_transaction_id, month, transaction_id)
		VALUES (?, '2026-08', NULL)
	`, created.RecurringTransaction.ID)
	if err != nil {
		t.Fatalf("insert run with NULL transaction_id error = %v", err)
	}

	res, err := store.PreviewDue(ctx)
	if err != nil {
		t.Fatalf("PreviewDue error = %v", err)
	}
	if len(res.DueTransactions) != 0 {
		t.Fatalf("due length = %d, want 0 for removed generated transaction run", len(res.DueTransactions))
	}
}

func TestPreviewDueAmountOverflowHandled(t *testing.T) {
	ctx := context.Background()
	store, catStore, db := openRecurringStore(t)
	cat := mustCreateCategory(t, ctx, catStore, "Housing")

	halfMax := int64(math.MaxInt64 / 2)
	timestamp := "2026-08-30T12:00:00.000Z"
	for i := 0; i < 3; i++ {
		_, err := db.ExecContext(ctx, `
			INSERT INTO recurring_transactions (merchant, amount_hundredths, category_id, day_of_month, note, active, created_at, updated_at)
			VALUES (?, ?, ?, 1, NULL, 1, ?, ?)
		`, "Big Rent", halfMax, cat.ID, timestamp, timestamp)
		if err != nil {
			t.Fatalf("insert big template: %v", err)
		}
	}

	_, err := store.PreviewDue(ctx)
	if err == nil {
		t.Fatal("PreviewDue error = nil, want amount overflow error")
	}
}

func TestPreviewDuePerformsNoWrites(t *testing.T) {
	ctx := context.Background()
	store, catStore, db := openRecurringStore(t)
	mustCreateCategory(t, ctx, catStore, "Entertainment")
	mustCreateRecurring(t, ctx, store, recurring.CreateInput{
		Merchant:   "Netflix",
		Amount:     "22.99",
		Category:   "Entertainment",
		DayOfMonth: 15,
	})

	catCountBefore := countRows(t, ctx, db, "SELECT count(*) FROM categories")
	recCountBefore := countRows(t, ctx, db, "SELECT count(*) FROM recurring_transactions")
	runCountBefore := countRows(t, ctx, db, "SELECT count(*) FROM recurring_transaction_runs")
	txnCountBefore := countRows(t, ctx, db, "SELECT count(*) FROM transactions")
	kmCountBefore := countRows(t, ctx, db, "SELECT count(*) FROM known_merchants")
	budCountBefore := countRows(t, ctx, db, "SELECT count(*) FROM budgets")

	res, err := store.PreviewDue(ctx)
	if err != nil {
		t.Fatalf("PreviewDue error = %v", err)
	}
	if len(res.DueTransactions) != 1 {
		t.Fatalf("due count = %d, want 1", len(res.DueTransactions))
	}

	if countRows(t, ctx, db, "SELECT count(*) FROM categories") != catCountBefore ||
		countRows(t, ctx, db, "SELECT count(*) FROM recurring_transactions") != recCountBefore ||
		countRows(t, ctx, db, "SELECT count(*) FROM recurring_transaction_runs") != runCountBefore ||
		countRows(t, ctx, db, "SELECT count(*) FROM transactions") != txnCountBefore ||
		countRows(t, ctx, db, "SELECT count(*) FROM known_merchants") != kmCountBefore ||
		countRows(t, ctx, db, "SELECT count(*) FROM budgets") != budCountBefore {
		t.Fatal("PreviewDue modified the database tables")
	}
}

func TestPreviewDueNilDependencies(t *testing.T) {
	ctx := context.Background()
	var nilStore *recurring.Store
	if _, err := nilStore.PreviewDue(ctx); err == nil {
		t.Error("PreviewDue with nil store error = nil, want error")
	}

	storeWithoutClock := &recurring.Store{DB: nil, Now: nil}
	if _, err := storeWithoutClock.PreviewDue(ctx); err == nil {
		t.Error("PreviewDue with nil db error = nil, want error")
	}
}
