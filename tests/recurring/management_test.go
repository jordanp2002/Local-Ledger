package recurring_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
	"github.com/jordanp2002/local-finance-mcp/internal/recurring"
)

func TestUpdateRecurringTransactionPatchesFieldsAndPreservesOmissions(t *testing.T) {
	ctx := context.Background()
	store, catStore, db := openRecurringStore(t)
	utilities := mustCreateCategory(t, ctx, catStore, "Utilities")
	other := mustCreateCategory(t, ctx, catStore, "Other")
	created := mustCreateRecurring(t, ctx, store, recurring.CreateInput{
		Merchant: "  Internet  ", Amount: "60", Category: "Utilities", DayOfMonth: 31,
		Note: stringPointer("  Monthly bill  "),
	})
	original := created.RecurringTransaction

	nowCalls := 0
	store.Now = func() time.Time {
		nowCalls++
		return time.Date(2026, 8, 30, 10, 0, 0, 0, time.FixedZone("EDT", -4*60*60))
	}
	updated, issues, err := store.Update(ctx, recurring.UpdateInput{
		ID:       original.ID,
		Merchant: stringPointer("  Broadband  "),
		Amount:   stringPointer("65.5"),
		Category: stringPointer("Other"),
		DayOfMonth: func() *int64 {
			day := int64(15)
			return &day
		}(),
		Note: recurring.NotePatch{Present: true, Value: stringPointer("  Updated  ")},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("Update() issues = %v", issues)
	}
	if !updated.Changed {
		t.Fatal("Changed = false, want true")
	}
	got := updated.RecurringTransaction
	if got.Merchant != "Broadband" || got.Amount != "65.50" || got.CategoryID != other.ID || got.Category != "Other" || got.DayOfMonth != 15 || got.Note == nil || *got.Note != "Updated" {
		t.Fatalf("updated template = %+v", got)
	}
	if got.CreatedAt != original.CreatedAt || got.UpdatedAt != "2026-08-30T14:00:00.000Z" {
		t.Fatalf("timestamps = (%q, %q)", got.CreatedAt, got.UpdatedAt)
	}
	if nowCalls != 1 {
		t.Fatalf("clock calls = %d, want one update snapshot", nowCalls)
	}
	if utilities.ID == got.CategoryID {
		t.Fatal("category did not change")
	}

	noOp, issues, err := store.Update(ctx, recurring.UpdateInput{ID: got.ID, Amount: stringPointer("65.50"), Note: recurring.NotePatch{Present: true, Value: stringPointer("Updated")}})
	if err != nil || len(issues) != 0 {
		t.Fatalf("normalized no-op = (%+v, %v, %v)", noOp, issues, err)
	}
	if noOp.Changed || noOp.RecurringTransaction.UpdatedAt != got.UpdatedAt {
		t.Fatalf("no-op result = %+v, want unchanged timestamp", noOp)
	}
	if nowCalls != 2 {
		t.Fatalf("clock calls after no-op = %d, want one per update", nowCalls)
	}

	cleared, issues, err := store.Update(ctx, recurring.UpdateInput{ID: got.ID, Note: recurring.NotePatch{Present: true}})
	if err != nil || len(issues) != 0 || cleared.RecurringTransaction.Note != nil {
		t.Fatalf("clear note = (%+v, %v, %v)", cleared, issues, err)
	}
	var note sql.NullString
	if err := db.QueryRowContext(ctx, "SELECT note FROM recurring_transactions WHERE id = ?", got.ID).Scan(&note); err != nil {
		t.Fatalf("read note: %v", err)
	}
	if note.Valid {
		t.Fatalf("stored note = %q, want NULL", note.String)
	}
	if _, issues, err := store.Disable(ctx, got.ID); err != nil || len(issues) != 0 {
		t.Fatalf("Disable() = (%v, %v)", issues, err)
	}
	disabledUpdate, issues, err := store.Update(ctx, recurring.UpdateInput{ID: got.ID, Merchant: stringPointer("Offline")})
	if err != nil || len(issues) != 0 || !disabledUpdate.Changed || disabledUpdate.RecurringTransaction.Active || disabledUpdate.RecurringTransaction.Merchant != "Offline" {
		t.Fatalf("update disabled template = (%+v, %v, %v)", disabledUpdate, issues, err)
	}
}

func TestUpdateRecurringTransactionValidationAndInactiveExistingCategory(t *testing.T) {
	ctx := context.Background()
	store, catStore, _ := openRecurringStore(t)
	mustCreateCategory(t, ctx, catStore, "Utilities")
	mustCreateCategory(t, ctx, catStore, "Other")
	created := mustCreateRecurring(t, ctx, store, recurring.CreateInput{Merchant: "Internet", Amount: "60.00", Category: "Utilities", DayOfMonth: 15})

	tests := []struct {
		name  string
		input recurring.UpdateInput
		field string
	}{
		{"missing id", recurring.UpdateInput{Amount: stringPointer("20.00")}, "id"},
		{"empty patch", recurring.UpdateInput{ID: created.RecurringTransaction.ID}, "id"},
		{"null merchant", recurring.UpdateInput{ID: created.RecurringTransaction.ID, MerchantNull: true}, "merchant"},
		{"null amount", recurring.UpdateInput{ID: created.RecurringTransaction.ID, AmountNull: true}, "amount"},
		{"null category", recurring.UpdateInput{ID: created.RecurringTransaction.ID, CategoryNull: true}, "category"},
		{"null day", recurring.UpdateInput{ID: created.RecurringTransaction.ID, DayOfMonthNull: true}, "day_of_month"},
		{"invalid amount", recurring.UpdateInput{ID: created.RecurringTransaction.ID, Amount: stringPointer("0.001")}, "amount"},
		{"invalid merchant", recurring.UpdateInput{ID: created.RecurringTransaction.ID, Merchant: stringPointer("  ")}, "merchant"},
		{"invalid day", recurring.UpdateInput{ID: created.RecurringTransaction.ID, DayOfMonth: int64Pointer(32)}, "day_of_month"},
		{"invalid note", recurring.UpdateInput{ID: created.RecurringTransaction.ID, Note: recurring.NotePatch{Present: true, Value: stringPointer("bad\x00note")}}, "note"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, issues, err := store.Update(ctx, tt.input)
			if err != nil {
				t.Fatalf("Update() error = %v", err)
			}
			if !hasIssue(issues, tt.field) {
				t.Fatalf("issues = %v, want field %q", issues, tt.field)
			}
		})
	}
	_, issues, err := store.Update(ctx, recurring.UpdateInput{ID: created.RecurringTransaction.ID, Category: stringPointer("Missing")})
	if err == nil || len(issues) != 0 || !errors.Is(err, recurring.ErrCategoryNotFound) {
		t.Fatalf("missing supplied category = (issues=%v, err=%v), want category not found", issues, err)
	}

	mustDisableCategory(t, ctx, catStore, "Utilities")
	updated, issues, err := store.Update(ctx, recurring.UpdateInput{ID: created.RecurringTransaction.ID, Amount: stringPointer("61.00")})
	if err != nil || len(issues) != 0 {
		t.Fatalf("update with inactive existing category = (%+v, %v, %v)", updated, issues, err)
	}
	if updated.RecurringTransaction.Amount != "61.00" || updated.RecurringTransaction.CategoryActive {
		t.Fatalf("updated inactive-category template = %+v", updated.RecurringTransaction)
	}

	_, issues, err = store.Update(ctx, recurring.UpdateInput{ID: created.RecurringTransaction.ID, Category: stringPointer("Utilities")})
	if err == nil || len(issues) != 0 || !errors.Is(err, recurring.ErrCategoryInactive) {
		t.Fatalf("inactive supplied category = (issues=%v, err=%v), want category inactive", issues, err)
	}
}

func TestEnableRecurringTransactionRequiresActiveCategoryAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store, catStore, _ := openRecurringStore(t)
	mustCreateCategory(t, ctx, catStore, "Utilities")
	created := mustCreateRecurring(t, ctx, store, recurring.CreateInput{Merchant: "Internet", Amount: "60.00", Category: "Utilities", DayOfMonth: 15})
	mustDisableCategory(t, ctx, catStore, "Utilities")
	if _, issues, err := store.Enable(ctx, created.RecurringTransaction.ID); err == nil || len(issues) != 0 || !errors.Is(err, recurring.ErrCategoryInactive) {
		t.Fatalf("inactive enable = (issues=%v, err=%v), want category inactive", issues, err)
	}
	mustCreateCategory(t, ctx, catStore, "Utilities")
	if _, issues, err := store.Disable(ctx, created.RecurringTransaction.ID); err != nil || len(issues) != 0 {
		t.Fatalf("Disable() error = (%v, %v)", issues, err)
	}
	enabled, issues, err := store.Enable(ctx, created.RecurringTransaction.ID)
	if err != nil || len(issues) != 0 || !enabled.Changed || !enabled.RecurringTransaction.Active {
		t.Fatalf("Enable() = (%+v, %v, %v)", enabled, issues, err)
	}
	repeated, issues, err := store.Enable(ctx, created.RecurringTransaction.ID)
	if err != nil || len(issues) != 0 || repeated.Changed || !repeated.RecurringTransaction.Active {
		t.Fatalf("repeated Enable() = (%+v, %v, %v)", repeated, issues, err)
	}
}

func TestPreviewUpcomingTransactionsSchedulesDueFutureBlockedAndRunExcluded(t *testing.T) {
	ctx := context.Background()
	store, catStore, db := openRecurringStore(t)
	mustCreateCategory(t, ctx, catStore, "Active")
	mustCreateCategory(t, ctx, catStore, "Blocked")
	mustCreateCategory(t, ctx, catStore, "Future Blocked")
	due := mustCreateRecurring(t, ctx, store, recurring.CreateInput{Merchant: "Rent", Amount: "100.00", Category: "Active", DayOfMonth: 1})
	future := mustCreateRecurring(t, ctx, store, recurring.CreateInput{Merchant: "Future", Amount: "22.99", Category: "Future Blocked", DayOfMonth: 31, Note: stringPointer("later")})
	blocked := mustCreateRecurring(t, ctx, store, recurring.CreateInput{Merchant: "Blocked", Amount: "50.00", Category: "Blocked", DayOfMonth: 20})
	run := mustCreateRecurring(t, ctx, store, recurring.CreateInput{Merchant: "Already", Amount: "5.00", Category: "Active", DayOfMonth: 5})
	disabled := mustCreateRecurring(t, ctx, store, recurring.CreateInput{Merchant: "Disabled", Amount: "8.00", Category: "Active", DayOfMonth: 8})
	if _, issues, err := store.Disable(ctx, disabled.RecurringTransaction.ID); err != nil || len(issues) != 0 {
		t.Fatalf("Disable() = (%v, %v)", issues, err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO recurring_transaction_runs (recurring_transaction_id, month, transaction_id) VALUES (?, '2026-08', NULL)", run.RecurringTransaction.ID); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	mustDisableCategory(t, ctx, catStore, "Blocked")
	mustDisableCategory(t, ctx, catStore, "Future Blocked")

	res, err := store.PreviewUpcoming(ctx)
	if err != nil {
		t.Fatalf("PreviewUpcoming() error = %v", err)
	}
	if res.AsOfDate != "2026-08-30" || res.Month != "2026-08" || res.TotalAmount != "122.99" {
		t.Fatalf("summary = (%q, %q, %q)", res.AsOfDate, res.Month, res.TotalAmount)
	}
	if len(res.UpcomingTransactions) != 2 {
		t.Fatalf("upcoming = %d, want 2", len(res.UpcomingTransactions))
	}
	if res.UpcomingTransactions[0].RecurringTransactionID != due.RecurringTransaction.ID || res.UpcomingTransactions[0].Status != "due" || res.UpcomingTransactions[0].ScheduledDate != "2026-08-01" {
		t.Errorf("due row = %+v", res.UpcomingTransactions[0])
	}
	if res.UpcomingTransactions[1].RecurringTransactionID != future.RecurringTransaction.ID || res.UpcomingTransactions[1].Status != "scheduled" || res.UpcomingTransactions[1].ScheduledDate != "2026-08-31" || res.UpcomingTransactions[1].Note == nil || *res.UpcomingTransactions[1].Note != "later" {
		t.Errorf("future row = %+v", res.UpcomingTransactions[1])
	}
	if len(res.Blocked) != 1 || res.Blocked[0].RecurringTransactionID != blocked.RecurringTransaction.ID || res.Blocked[0].DueDate != "2026-08-20" || res.Blocked[0].Category != "Blocked" {
		t.Fatalf("blocked = %+v", res.Blocked)
	}
}

func TestRecurringScheduleClampsMonthEndForUpcomingAndDue(t *testing.T) {
	ctx := context.Background()
	store, catStore, _ := openRecurringStore(t)
	mustCreateCategory(t, ctx, catStore, "Housing")
	mustCreateRecurring(t, ctx, store, recurring.CreateInput{Merchant: "Rent", Amount: "1.00", Category: "Housing", DayOfMonth: 31})
	store.Now = func() time.Time { return time.Date(2024, 2, 28, 10, 0, 0, 0, time.FixedZone("EST", -5*60*60)) }
	upcoming, err := store.PreviewUpcoming(ctx)
	if err != nil {
		t.Fatalf("PreviewUpcoming() error = %v", err)
	}
	if len(upcoming.UpcomingTransactions) != 1 || upcoming.UpcomingTransactions[0].ScheduledDate != "2024-02-29" || upcoming.UpcomingTransactions[0].Status != "scheduled" {
		t.Fatalf("leap-year upcoming = %+v", upcoming.UpcomingTransactions)
	}
	due, err := store.PreviewDue(ctx)
	if err != nil {
		t.Fatalf("PreviewDue() error = %v", err)
	}
	if len(due.DueTransactions) != 0 {
		t.Fatalf("due on Feb 28 = %+v, want empty", due.DueTransactions)
	}
}

func TestUpdateRecurringTransactionLeavesMaterializedHistoryUntouched(t *testing.T) {
	ctx := context.Background()
	store, catStore, db := openRecurringStore(t)
	cat := mustCreateCategory(t, ctx, catStore, "Housing")
	created := mustCreateRecurring(t, ctx, store, recurring.CreateInput{Merchant: "Rent", Amount: "1500.00", Category: "Housing", DayOfMonth: 1})

	materialized, err := store.MaterializeDue(ctx)
	if err != nil {
		t.Fatalf("MaterializeDue() error = %v", err)
	}
	if len(materialized.Transactions) != 1 {
		t.Fatalf("materialized transactions = %d, want 1", len(materialized.Transactions))
	}
	var beforeMerchant string
	var beforeDate string
	if err := db.QueryRowContext(ctx, "SELECT merchant, date FROM transactions WHERE id = ?", materialized.Transactions[0].ID).Scan(&beforeMerchant, &beforeDate); err != nil {
		t.Fatalf("read materialized transaction: %v", err)
	}
	var beforeAmount int64
	if err := db.QueryRowContext(ctx, "SELECT amount_hundredths FROM transaction_allocations WHERE transaction_id = ?", materialized.Transactions[0].ID).Scan(&beforeAmount); err != nil {
		t.Fatalf("read materialized allocation: %v", err)
	}
	updated, issues, err := store.Update(ctx, recurring.UpdateInput{ID: created.RecurringTransaction.ID, Amount: stringPointer("1600.00")})
	if err != nil || len(issues) != 0 || updated.RecurringTransaction.Amount != "1600.00" {
		t.Fatalf("Update() = (%+v, %v, %v)", updated, issues, err)
	}
	var afterMerchant string
	var afterDate string
	if err := db.QueryRowContext(ctx, "SELECT merchant, date FROM transactions WHERE id = ?", materialized.Transactions[0].ID).Scan(&afterMerchant, &afterDate); err != nil {
		t.Fatalf("read transaction after update: %v", err)
	}
	var afterAmount int64
	if err := db.QueryRowContext(ctx, "SELECT amount_hundredths FROM transaction_allocations WHERE transaction_id = ?", materialized.Transactions[0].ID).Scan(&afterAmount); err != nil {
		t.Fatalf("read allocation after update: %v", err)
	}
	if afterMerchant != beforeMerchant || afterAmount != beforeAmount || afterDate != beforeDate {
		t.Fatalf("materialized transaction changed from (%s, %d, %s) to (%s, %d, %s)", beforeMerchant, beforeAmount, beforeDate, afterMerchant, afterAmount, afterDate)
	}
	if countRows(t, ctx, db, "SELECT count(*) FROM recurring_transaction_runs WHERE recurring_transaction_id = ?", created.RecurringTransaction.ID) != 1 {
		t.Fatal("recurring run history changed")
	}
	if countRows(t, ctx, db, "SELECT count(*) FROM transaction_allocations WHERE category_id = ?", cat.ID) != 1 {
		t.Fatal("materialized transaction count changed")
	}
}

func int64Pointer(value int64) *int64 { return &value }

func hasIssue(issues []contract.FieldIssue, field string) bool {
	for _, issue := range issues {
		if issue.Field == field {
			return true
		}
	}
	return false
}
