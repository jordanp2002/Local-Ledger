package recurring_test

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/jordanp2002/Local-Ledger/internal/recurring"
)

func TestMaterializeDueNothingDue(t *testing.T) {
	ctx := context.Background()
	store, catStore, db := openRecurringStore(t)
	toronto := time.FixedZone("EDT", -4*60*60)
	store.Now = func() time.Time { return time.Date(2026, 8, 10, 10, 0, 0, 0, toronto) }

	mustCreateCategory(t, ctx, catStore, "Entertainment")
	mustCreateRecurring(t, ctx, store, recurring.CreateInput{
		Merchant:   "Netflix",
		Amount:     "22.99",
		Category:   "Entertainment",
		DayOfMonth: 15,
	})

	res, err := store.MaterializeDue(ctx)
	if err != nil {
		t.Fatalf("MaterializeDue error = %v", err)
	}

	if res.AsOfDate != "2026-08-10" || res.Month != "2026-08" {
		t.Fatalf("dates = (%q, %q)", res.AsOfDate, res.Month)
	}
	if res.Created != 0 || res.TotalAmount != "0.00" || len(res.Transactions) != 0 {
		t.Fatalf("res = %+v, want created=0, total=0.00, empty txns", res)
	}
	if countRows(t, ctx, db, "SELECT count(*) FROM transactions") != 0 {
		t.Fatal("transactions created when nothing was due")
	}
	if countRows(t, ctx, db, "SELECT count(*) FROM recurring_transaction_runs") != 0 {
		t.Fatal("runs created when nothing was due")
	}
}

func TestMaterializeDueMultipleRowsSuccessAndOrdering(t *testing.T) {
	ctx := context.Background()
	store, catStore, db := openRecurringStore(t)
	toronto := time.FixedZone("EDT", -4*60*60)
	store.Now = func() time.Time { return time.Date(2026, 8, 20, 10, 0, 0, 0, toronto) }

	catHousing := mustCreateCategory(t, ctx, catStore, "Housing")
	catEnt := mustCreateCategory(t, ctx, catStore, "Entertainment")
	if _, err := db.ExecContext(ctx, `INSERT INTO known_merchants (merchant, category_id) VALUES (?, ?)`, "Landlord", catHousing.ID); err != nil {
		t.Fatalf("insert known merchant: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO budgets (category_id, month, amount_hundredths) VALUES (?, ?, ?)`, catHousing.ID, "2026-08", 200000); err != nil {
		t.Fatalf("insert budget: %v", err)
	}

	t1 := mustCreateRecurring(t, ctx, store, recurring.CreateInput{
		Merchant:   "Rent",
		Amount:     "1500.00",
		Category:   "Housing",
		DayOfMonth: 1,
	})
	t2 := mustCreateRecurring(t, ctx, store, recurring.CreateInput{
		Merchant:   "Netflix",
		Amount:     "22.99",
		Category:   "Entertainment",
		DayOfMonth: 15,
		Note:       stringPointer("Monthly subscription"),
	})
	t3 := mustCreateRecurring(t, ctx, store, recurring.CreateInput{
		Merchant:   "Spotify",
		Amount:     "10.99",
		Category:   "Entertainment",
		DayOfMonth: 25,
	})
	res, err := store.MaterializeDue(ctx)
	if err != nil {
		t.Fatalf("MaterializeDue error = %v", err)
	}

	if res.AsOfDate != "2026-08-20" || res.Month != "2026-08" {
		t.Fatalf("dates = (%q, %q)", res.AsOfDate, res.Month)
	}
	if res.Created != 2 || res.TotalAmount != "1522.99" {
		t.Fatalf("res created=%d, total=%q, want 2, 1522.99", res.Created, res.TotalAmount)
	}
	if len(res.Transactions) != 2 {
		t.Fatalf("transactions len = %d, want 2", len(res.Transactions))
	}

	rent := res.Transactions[0]
	if rent.Merchant != "Rent" || rent.Amount != "1500.00" || rent.Date != "2026-08-01" || rent.CategoryID == nil || *rent.CategoryID != catHousing.ID || rent.Category == nil || *rent.Category != "Housing" || rent.Note != nil {
		t.Errorf("rent = %+v", rent)
	}

	netflix := res.Transactions[1]
	if netflix.Merchant != "Netflix" || netflix.Amount != "22.99" || netflix.Date != "2026-08-15" || netflix.CategoryID == nil || *netflix.CategoryID != catEnt.ID || netflix.Category == nil || *netflix.Category != "Entertainment" {
		t.Errorf("netflix = %+v", netflix)
	}
	if netflix.Note == nil || *netflix.Note != "Monthly subscription" {
		t.Errorf("netflix note = %v, want Monthly subscription", netflix.Note)
	}

	if countRows(t, ctx, db, "SELECT count(*) FROM transactions") != 2 {
		t.Fatalf("transaction rows = %d, want 2", countRows(t, ctx, db, "SELECT count(*) FROM transactions"))
	}
	if countRows(t, ctx, db, "SELECT count(*) FROM recurring_transaction_runs") != 2 {
		t.Fatalf("run rows = %d, want 2", countRows(t, ctx, db, "SELECT count(*) FROM recurring_transaction_runs"))
	}

	var runRentTxnID int64
	if err := db.QueryRowContext(ctx, "SELECT transaction_id FROM recurring_transaction_runs WHERE recurring_transaction_id = ? AND month = '2026-08'", t1.RecurringTransaction.ID).Scan(&runRentTxnID); err != nil {
		t.Fatalf("query rent run: %v", err)
	}
	if runRentTxnID != rent.ID {
		t.Errorf("rent run txnID = %d, want %d", runRentTxnID, rent.ID)
	}

	var runNetflixTxnID int64
	if err := db.QueryRowContext(ctx, "SELECT transaction_id FROM recurring_transaction_runs WHERE recurring_transaction_id = ? AND month = '2026-08'", t2.RecurringTransaction.ID).Scan(&runNetflixTxnID); err != nil {
		t.Fatalf("query netflix run: %v", err)
	}
	if runNetflixTxnID != netflix.ID {
		t.Errorf("netflix run txnID = %d, want %d", runNetflixTxnID, netflix.ID)
	}

	if countRows(t, ctx, db, "SELECT count(*) FROM recurring_transaction_runs WHERE recurring_transaction_id = ?", t3.RecurringTransaction.ID) != 0 {
		t.Errorf("future template Spotify has runs, want 0")
	}

	var knownMerchant string
	var knownCategoryID int64
	if err := db.QueryRowContext(ctx, `SELECT merchant, category_id FROM known_merchants`).Scan(&knownMerchant, &knownCategoryID); err != nil {
		t.Fatalf("query known merchant: %v", err)
	}
	if knownMerchant != "Landlord" || knownCategoryID != catHousing.ID {
		t.Errorf("known merchant = (%q, %d), want (Landlord, %d)", knownMerchant, knownCategoryID, catHousing.ID)
	}
	var budgetCategoryID, budgetAmount int64
	var budgetMonth string
	if err := db.QueryRowContext(ctx, `SELECT category_id, month, amount_hundredths FROM budgets`).Scan(&budgetCategoryID, &budgetMonth, &budgetAmount); err != nil {
		t.Fatalf("query budget: %v", err)
	}
	if budgetCategoryID != catHousing.ID || budgetMonth != "2026-08" || budgetAmount != 200000 {
		t.Errorf("budget = (%d, %q, %d), want (%d, 2026-08, 200000)", budgetCategoryID, budgetMonth, budgetAmount, catHousing.ID)
	}
}

func TestMaterializeDueRepeatedCallIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store, catStore, db := openRecurringStore(t)
	toronto := time.FixedZone("EDT", -4*60*60)
	store.Now = func() time.Time { return time.Date(2026, 8, 20, 10, 0, 0, 0, toronto) }

	mustCreateCategory(t, ctx, catStore, "Entertainment")
	mustCreateRecurring(t, ctx, store, recurring.CreateInput{
		Merchant:   "Netflix",
		Amount:     "22.99",
		Category:   "Entertainment",
		DayOfMonth: 15,
	})

	res1, err := store.MaterializeDue(ctx)
	if err != nil {
		t.Fatalf("first MaterializeDue error = %v", err)
	}
	if res1.Created != 1 {
		t.Fatalf("first created = %d, want 1", res1.Created)
	}

	res2, err := store.MaterializeDue(ctx)
	if err != nil {
		t.Fatalf("second MaterializeDue error = %v", err)
	}
	if res2.Created != 0 || res2.TotalAmount != "0.00" || len(res2.Transactions) != 0 {
		t.Fatalf("second res = %+v, want created=0, total=0.00, empty txns", res2)
	}

	if countRows(t, ctx, db, "SELECT count(*) FROM transactions") != 1 {
		t.Fatalf("transaction count = %d, want 1", countRows(t, ctx, db, "SELECT count(*) FROM transactions"))
	}
	if countRows(t, ctx, db, "SELECT count(*) FROM recurring_transaction_runs") != 1 {
		t.Fatalf("run count = %d, want 1", countRows(t, ctx, db, "SELECT count(*) FROM recurring_transaction_runs"))
	}
}

func TestMaterializeDueConcurrentCallsCreateOnce(t *testing.T) {
	ctx := context.Background()
	store, catStore, db := openRecurringStore(t)
	mustCreateCategory(t, ctx, catStore, "Entertainment")
	mustCreateRecurring(t, ctx, store, recurring.CreateInput{
		Merchant:   "Netflix",
		Amount:     "22.99",
		Category:   "Entertainment",
		DayOfMonth: 15,
	})

	const calls = 8
	results := make(chan recurring.MaterializeDueResult, calls)
	errs := make(chan error, calls)
	var ready sync.WaitGroup
	ready.Add(calls)
	start := make(chan struct{})
	for range calls {
		go func() {
			ready.Done()
			<-start
			result, err := store.MaterializeDue(ctx)
			results <- result
			errs <- err
		}()
	}
	ready.Wait()
	close(start)

	var created int64
	for range calls {
		if err := <-errs; err != nil {
			t.Fatalf("MaterializeDue concurrent error = %v", err)
		}
		created += (<-results).Created
	}
	if created != 1 {
		t.Fatalf("created across concurrent calls = %d, want 1", created)
	}
	if countRows(t, ctx, db, "SELECT count(*) FROM transactions") != 1 {
		t.Fatal("concurrent calls created duplicate transactions")
	}
	if countRows(t, ctx, db, "SELECT count(*) FROM recurring_transaction_runs") != 1 {
		t.Fatal("concurrent calls created duplicate run rows")
	}
}

func TestMaterializeDueLaterInsertFailureRollsBackAll(t *testing.T) {
	ctx := context.Background()
	store, catStore, db := openRecurringStore(t)
	mustCreateCategory(t, ctx, catStore, "Housing")
	mustCreateRecurring(t, ctx, store, recurring.CreateInput{
		Merchant:   "Rent",
		Amount:     "1500.00",
		Category:   "Housing",
		DayOfMonth: 1,
	})
	mustCreateRecurring(t, ctx, store, recurring.CreateInput{
		Merchant:   "Utilities",
		Amount:     "125.00",
		Category:   "Housing",
		DayOfMonth: 15,
	})
	if _, err := db.ExecContext(ctx, `
		CREATE TRIGGER fail_utilities_insert
		BEFORE INSERT ON transactions
		WHEN NEW.merchant = 'Utilities'
		BEGIN
			SELECT RAISE(FAIL, 'forced later-row failure');
		END
	`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	if _, err := store.MaterializeDue(ctx); err == nil {
		t.Fatal("MaterializeDue error = nil, want later-row failure")
	}
	if countRows(t, ctx, db, "SELECT count(*) FROM transactions") != 0 {
		t.Fatal("transaction persisted after later-row failure")
	}
	if countRows(t, ctx, db, "SELECT count(*) FROM recurring_transaction_runs") != 0 {
		t.Fatal("run row persisted after later-row failure")
	}
}

func TestMaterializeDueRemovedGeneratedTransactionNotRecreated(t *testing.T) {
	ctx := context.Background()
	store, catStore, db := openRecurringStore(t)
	toronto := time.FixedZone("EDT", -4*60*60)
	store.Now = func() time.Time { return time.Date(2026, 8, 20, 10, 0, 0, 0, toronto) }

	mustCreateCategory(t, ctx, catStore, "Entertainment")
	mustCreateRecurring(t, ctx, store, recurring.CreateInput{
		Merchant:   "Netflix",
		Amount:     "22.99",
		Category:   "Entertainment",
		DayOfMonth: 15,
	})

	res1, err := store.MaterializeDue(ctx)
	if err != nil {
		t.Fatalf("first MaterializeDue error = %v", err)
	}
	if res1.Created != 1 {
		t.Fatalf("first created = %d, want 1", res1.Created)
	}

	txnID := res1.Transactions[0].ID
	if _, err := db.ExecContext(ctx, "DELETE FROM transactions WHERE id = ?", txnID); err != nil {
		t.Fatalf("delete transaction error: %v", err)
	}

	var runTxnID sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT transaction_id FROM recurring_transaction_runs WHERE month = '2026-08'").Scan(&runTxnID); err != nil {
		t.Fatalf("query run after delete: %v", err)
	}
	if runTxnID.Valid {
		t.Errorf("run transaction_id = %v, want NULL", runTxnID)
	}

	res2, err := store.MaterializeDue(ctx)
	if err != nil {
		t.Fatalf("second MaterializeDue error = %v", err)
	}
	if res2.Created != 0 || len(res2.Transactions) != 0 {
		t.Fatalf("second created = %d, want 0 after generated transaction deletion", res2.Created)
	}
	if countRows(t, ctx, db, "SELECT count(*) FROM transactions") != 0 {
		t.Errorf("transaction count = %d, want 0", countRows(t, ctx, db, "SELECT count(*) FROM transactions"))
	}
}

func TestMaterializeDueBlockedTemplateRollsBackAll(t *testing.T) {
	ctx := context.Background()
	store, catStore, db := openRecurringStore(t)
	toronto := time.FixedZone("EDT", -4*60*60)
	store.Now = func() time.Time { return time.Date(2026, 8, 20, 10, 0, 0, 0, toronto) }

	mustCreateCategory(t, ctx, catStore, "Housing")
	mustCreateCategory(t, ctx, catStore, "Fitness")

	mustCreateRecurring(t, ctx, store, recurring.CreateInput{
		Merchant:   "Rent",
		Amount:     "1500.00",
		Category:   "Housing",
		DayOfMonth: 1,
	})

	gymTmpl := mustCreateRecurring(t, ctx, store, recurring.CreateInput{
		Merchant:   "Gym",
		Amount:     "50.00",
		Category:   "Fitness",
		DayOfMonth: 10,
	})

	mustDisableCategory(t, ctx, catStore, "Fitness")

	res, err := store.MaterializeDue(ctx)
	if err == nil {
		t.Fatal("MaterializeDue error = nil, want category inactive error")
	}

	var inactiveErr *recurring.RecurringCategoryInactiveError
	if !errors.As(err, &inactiveErr) {
		t.Fatalf("err = %v, want *RecurringCategoryInactiveError", err)
	}
	if inactiveErr.RecurringTransactionID != gymTmpl.RecurringTransaction.ID {
		t.Errorf("inactive id = %d, want %d", inactiveErr.RecurringTransactionID, gymTmpl.RecurringTransaction.ID)
	}
	if inactiveErr.Merchant != "Gym" || inactiveErr.Category != "Fitness" || inactiveErr.DueDate != "2026-08-10" {
		t.Errorf("inactive error details = %+v", inactiveErr)
	}

	if res.Created != 0 || len(res.Transactions) != 0 {
		t.Errorf("res = %+v, want empty on error", res)
	}

	if countRows(t, ctx, db, "SELECT count(*) FROM transactions") != 0 {
		t.Errorf("transactions created on rollback: %d", countRows(t, ctx, db, "SELECT count(*) FROM transactions"))
	}
	if countRows(t, ctx, db, "SELECT count(*) FROM recurring_transaction_runs") != 0 {
		t.Errorf("runs created on rollback: %d", countRows(t, ctx, db, "SELECT count(*) FROM recurring_transaction_runs"))
	}
}

func TestMaterializeDueMonthEndClamping(t *testing.T) {
	ctx := context.Background()
	store, catStore, db := openRecurringStore(t)
	toronto := time.FixedZone("EST", -5*60*60)
	store.Now = func() time.Time { return time.Date(2026, 2, 28, 10, 0, 0, 0, toronto) }

	mustCreateCategory(t, ctx, catStore, "Housing")
	mustCreateRecurring(t, ctx, store, recurring.CreateInput{
		Merchant:   "Rent",
		Amount:     "1500.00",
		Category:   "Housing",
		DayOfMonth: 31,
	})

	res, err := store.MaterializeDue(ctx)
	if err != nil {
		t.Fatalf("MaterializeDue error = %v", err)
	}
	if res.Created != 1 {
		t.Fatalf("created = %d, want 1", res.Created)
	}
	if res.Transactions[0].Date != "2026-02-28" {
		t.Errorf("transaction date = %q, want 2026-02-28", res.Transactions[0].Date)
	}

	var storedDate string
	if err := db.QueryRowContext(ctx, "SELECT date FROM transactions WHERE id = ?", res.Transactions[0].ID).Scan(&storedDate); err != nil {
		t.Fatalf("query transaction date: %v", err)
	}
	if storedDate != "2026-02-28" {
		t.Errorf("stored date = %q, want 2026-02-28", storedDate)
	}
}

func TestMaterializeDueAmountOverflow(t *testing.T) {
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

	_, err := store.MaterializeDue(ctx)
	if err == nil {
		t.Fatal("MaterializeDue error = nil, want amount overflow error")
	}
}

func TestMaterializeDueNilDependencies(t *testing.T) {
	ctx := context.Background()
	var nilStore *recurring.Store
	if _, err := nilStore.MaterializeDue(ctx); err == nil {
		t.Error("MaterializeDue with nil store error = nil, want error")
	}

	storeWithoutClock := &recurring.Store{DB: nil, Now: nil}
	if _, err := storeWithoutClock.MaterializeDue(ctx); err == nil {
		t.Error("MaterializeDue with nil db error = nil, want error")
	}
}
