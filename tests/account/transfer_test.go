package account_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jordanp2002/Local-Ledger/internal/account"
)

func TestTransferRecordsBothSidesAndPreservesReplayBalances(t *testing.T) {
	s := openStore(t)
	source := mustCreate(t, s, "Checking", "checking", "100.00")
	destination := mustCreate(t, s, "Savings", "savings", "-50.00")

	first, fields, err := s.TransferBetweenAccounts(context.Background(), account.TransferInput{
		SourceAccountID: source.ID, DestinationAccountID: destination.ID,
		Amount: "25.00", Date: "2026-09-01", Note: strPtr("move"), NotePresent: true,
		IdempotencyKey: "move-1",
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("transfer = %+v %v %v", first, fields, err)
	}
	if first.Transfer.SourceAccount != "Checking" || first.Transfer.DestinationAccount != "Savings" || first.Transfer.Status != "recorded" || first.SourceBalance != "75.00" || first.DestinationBalance != "-25.00" || first.IdempotentReplay {
		t.Fatalf("first transfer = %+v", first)
	}

	mustRecord(t, s, account.RecordInput{AccountID: source.ID, Type: "deposit", Amount: "10.00", Date: "2026-08-01", IdempotencyKey: "later-id-backdated"})
	replay, fields, err := s.TransferBetweenAccounts(context.Background(), account.TransferInput{
		SourceAccountID: source.ID, DestinationAccountID: destination.ID,
		Amount: "25", Date: "2026-09-01", Note: strPtr("move"), NotePresent: true,
		IdempotencyKey: "move-1",
	})
	if err != nil || len(fields) != 0 || !replay.IdempotentReplay || replay.Transfer.ID != first.Transfer.ID {
		t.Fatalf("replay = %+v %v %v", replay, fields, err)
	}
	if replay.SourceBalance != "75.00" || replay.DestinationBalance != "-25.00" {
		t.Fatalf("replay balances = %q/%q", replay.SourceBalance, replay.DestinationBalance)
	}

	sourceActivity := mustListActivity(t, s, account.ListActivityInput{AccountID: source.ID})
	destinationActivity := mustListActivity(t, s, account.ListActivityInput{AccountID: destination.ID})
	if len(sourceActivity.Entries) != 2 || len(destinationActivity.Entries) != 1 {
		t.Fatalf("paired activity = %d/%d", len(sourceActivity.Entries), len(destinationActivity.Entries))
	}
	var sourceTransferEntry, destinationTransferEntry int64
	for _, entry := range sourceActivity.Entries {
		if entry.TransferID != nil && *entry.TransferID == first.Transfer.ID {
			sourceTransferEntry = entry.ID
			if entry.Kind != "transfer_out" || entry.Delta != "-25.00" {
				t.Fatalf("source transfer entry = %+v", entry)
			}
		}
	}
	for _, entry := range destinationActivity.Entries {
		if entry.TransferID != nil && *entry.TransferID == first.Transfer.ID {
			destinationTransferEntry = entry.ID
			if entry.Kind != "transfer_in" || entry.Delta != "25.00" {
				t.Fatalf("destination transfer entry = %+v", entry)
			}
		}
	}
	if sourceTransferEntry == 0 || destinationTransferEntry == 0 {
		t.Fatalf("paired transfer entry IDs = %d/%d", sourceTransferEntry, destinationTransferEntry)
	}
	if _, _, err := s.ReverseActivity(context.Background(), account.ReverseInput{EntryID: sourceTransferEntry, IdempotencyKey: "entry-reverse"}); !errors.Is(err, account.ErrEntryNotReversible) {
		t.Fatalf("transfer entry reversal err = %v", err)
	}
}

func TestTransferReversalIsAtomicAndRetrySafe(t *testing.T) {
	s := openStore(t)
	source := mustCreate(t, s, "Checking", "checking", "10.00")
	destination := mustCreate(t, s, "Savings", "savings", "0.00")
	first, fields, err := s.TransferBetweenAccounts(context.Background(), account.TransferInput{
		SourceAccountID: source.ID, DestinationAccountID: destination.ID,
		Amount: "10.00", Date: "2026-09-01", IdempotencyKey: "move-2",
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("transfer = %+v %v %v", first, fields, err)
	}
	reversal, fields, err := s.ReverseAccountTransfer(context.Background(), account.ReverseTransferInput{
		TransferID: first.Transfer.ID, Note: strPtr("undo"), NotePresent: true, IdempotencyKey: "undo-2",
	})
	if err != nil || len(fields) != 0 {
		t.Fatalf("reversal = %+v %v %v", reversal, fields, err)
	}
	if !reversal.Changed || reversal.Transfer.ReversalOfTransferID == nil || *reversal.Transfer.ReversalOfTransferID != first.Transfer.ID || reversal.SourceBalance != "0.00" || reversal.DestinationBalance != "10.00" {
		t.Fatalf("reversal = %+v", reversal)
	}
	retry, fields, err := s.ReverseAccountTransfer(context.Background(), account.ReverseTransferInput{
		TransferID: first.Transfer.ID, Note: strPtr("undo"), NotePresent: true, IdempotencyKey: "undo-2",
	})
	if err != nil || len(fields) != 0 || retry.Changed || !retry.IdempotentReplay || retry.Transfer.ID != reversal.Transfer.ID {
		t.Fatalf("reversal retry = %+v %v %v", retry, fields, err)
	}
	repeat, fields, err := s.ReverseAccountTransfer(context.Background(), account.ReverseTransferInput{
		TransferID: first.Transfer.ID, Note: strPtr("undo"), NotePresent: true, IdempotencyKey: "undo-3",
	})
	if err != nil || len(fields) != 0 || repeat.Changed || repeat.IdempotentReplay || repeat.Transfer.ID != reversal.Transfer.ID {
		t.Fatalf("reversal repeat = %+v %v %v", repeat, fields, err)
	}
	if _, _, err := s.ReverseAccountTransfer(context.Background(), account.ReverseTransferInput{TransferID: reversal.Transfer.ID, IdempotencyKey: "undo-4"}); !errors.Is(err, account.ErrTransferAlreadyReversed) {
		t.Fatalf("reversal of reversal err = %v", err)
	}
	if _, _, err := s.ReverseAccountTransfer(context.Background(), account.ReverseTransferInput{TransferID: first.Transfer.ID, IdempotencyKey: "undo-5"}); !errors.Is(err, account.ErrTransferAlreadyReversed) {
		t.Fatalf("second reversal err = %v", err)
	}
	accounts := mustList(t, s, account.ListInput{})
	if accounts[0].CurrentBalance != "10.00" || accounts[1].CurrentBalance != "0.00" {
		t.Fatalf("restored accounts = %+v", accounts)
	}
}

func TestTransferRollsBackAfterEntryFailure(t *testing.T) {
	for _, kind := range []string{"transfer_out", "transfer_in"} {
		t.Run(kind, func(t *testing.T) {
			assertTransferRollsBack(t, kind)
		})
	}
}

func assertTransferRollsBack(t *testing.T, failingKind string) {
	s := openStore(t)
	source := mustCreate(t, s, "Checking", "checking", "10.00")
	destination := mustCreate(t, s, "Savings", "savings", "0.00")
	trigger := `
		CREATE TRIGGER fail_account_transfer_in
		BEFORE INSERT ON account_entries
		WHEN NEW.kind = '` + failingKind + `'
		BEGIN
			SELECT RAISE(ABORT, 'forced transfer entry failure');
		END
	`
	if _, err := s.DB.ExecContext(context.Background(), trigger); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	defer func() { _, _ = s.DB.ExecContext(context.Background(), `DROP TRIGGER fail_account_transfer_in`) }()
	_, fields, err := s.TransferBetweenAccounts(context.Background(), account.TransferInput{
		SourceAccountID: source.ID, DestinationAccountID: destination.ID,
		Amount: "5.00", Date: "2026-09-01", IdempotencyKey: "forced-rollback",
	})
	if err == nil || len(fields) != 0 {
		t.Fatalf("forced transfer = fields=%v err=%v, want error", fields, err)
	}
	var transfers, entries int
	if err := s.DB.QueryRowContext(context.Background(), "SELECT count(*) FROM account_transfers").Scan(&transfers); err != nil {
		t.Fatalf("count transfers: %v", err)
	}
	if err := s.DB.QueryRowContext(context.Background(), "SELECT count(*) FROM account_entries").Scan(&entries); err != nil {
		t.Fatalf("count entries: %v", err)
	}
	if transfers != 0 || entries != 0 {
		t.Fatalf("rollback counts = transfers %d entries %d", transfers, entries)
	}
	if got := mustList(t, s, account.ListInput{}); got[0].CurrentBalance != "10.00" || got[1].CurrentBalance != "0.00" {
		t.Fatalf("balances after rollback = %+v", got)
	}
}

func TestTransferOverflowAndNegativeBalances(t *testing.T) {
	s := openStore(t)
	minSource := mustCreate(t, s, "Minimum", "checking", "-92233720368547758.07")
	zeroDestination := mustCreate(t, s, "Zero Destination", "savings", "0.00")
	_, fields, err := s.TransferBetweenAccounts(context.Background(), account.TransferInput{
		SourceAccountID: minSource.ID, DestinationAccountID: zeroDestination.ID,
		Amount: "0.02", Date: "2026-09-01", IdempotencyKey: "source-underflow",
	})
	if err == nil || len(fields) != 0 {
		t.Fatalf("source underflow = fields=%v err=%v, want overflow error", fields, err)
	}

	zeroSource := mustCreate(t, s, "Zero Source", "cash", "0.00")
	maxDestination := mustCreate(t, s, "Maximum", "savings", "92233720368547758.07")
	_, fields, err = s.TransferBetweenAccounts(context.Background(), account.TransferInput{
		SourceAccountID: zeroSource.ID, DestinationAccountID: maxDestination.ID,
		Amount: "0.01", Date: "2026-09-01", IdempotencyKey: "destination-overflow",
	})
	if err == nil || len(fields) != 0 {
		t.Fatalf("destination overflow = fields=%v err=%v, want overflow error", fields, err)
	}
	negativeSource := mustCreate(t, s, "Negative Source", "cash", "-10.00")
	positiveDestination := mustCreate(t, s, "Positive Destination", "cash", "5.00")
	result, fields, err := s.TransferBetweenAccounts(context.Background(), account.TransferInput{
		SourceAccountID: negativeSource.ID, DestinationAccountID: positiveDestination.ID,
		Amount: "2.00", Date: "2026-09-01", IdempotencyKey: "negative-allowed",
	})
	if err != nil || len(fields) != 0 || result.SourceBalance != "-12.00" || result.DestinationBalance != "7.00" {
		t.Fatalf("negative transfer = %+v %v %v", result, fields, err)
	}
}

func TestTransferConcurrentSameKeyCreatesOneRecord(t *testing.T) {
	s := openStore(t)
	source := mustCreate(t, s, "Checking", "checking", "100.00")
	destination := mustCreate(t, s, "Savings", "savings", "0.00")
	const attempts = 8
	results := make(chan account.TransferResult, attempts)
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, fields, err := s.TransferBetweenAccounts(context.Background(), account.TransferInput{
				SourceAccountID: source.ID, DestinationAccountID: destination.ID,
				Amount: "10.00", Date: "2026-09-01", IdempotencyKey: "concurrent-transfer",
			})
			if err != nil {
				errs <- err
				return
			}
			if len(fields) != 0 {
				errs <- errors.New("unexpected validation fields")
				return
			}
			results <- result
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent transfer error: %v", err)
	}
	var transferID int64
	var nonReplay int
	for result := range results {
		if result.Transfer.ID == 0 {
			t.Fatal("concurrent transfer returned empty transfer")
		}
		if transferID == 0 {
			transferID = result.Transfer.ID
		} else if result.Transfer.ID != transferID {
			t.Fatalf("concurrent transfer IDs = %d and %d", transferID, result.Transfer.ID)
		}
		if !result.IdempotentReplay {
			nonReplay++
		}
	}
	if got := attempts - nonReplay; got != attempts-1 {
		t.Fatalf("concurrent replay count = %d, want %d", got, attempts-1)
	}
	var count int
	if err := s.DB.QueryRowContext(context.Background(), "SELECT count(*) FROM account_transfers").Scan(&count); err != nil {
		t.Fatalf("count concurrent transfers: %v", err)
	}
	if count != 1 {
		t.Fatalf("concurrent transfer count = %d, want 1", count)
	}
	if got := mustList(t, s, account.ListInput{}); got[0].CurrentBalance != "90.00" || got[1].CurrentBalance != "10.00" {
		t.Fatalf("concurrent balances = %+v", got)
	}
}

func TestTransferListFiltersPaginationAndInactiveHistory(t *testing.T) {
	s := openStore(t)
	a := mustCreate(t, s, "A", "checking", "10.00")
	b := mustCreate(t, s, "B", "savings", "0.00")
	c := mustCreate(t, s, "C", "cash", "0.00")
	for _, in := range []account.TransferInput{
		{SourceAccountID: a.ID, DestinationAccountID: b.ID, Amount: "1.00", Date: "2026-08-01", IdempotencyKey: "l1"},
		{SourceAccountID: a.ID, DestinationAccountID: c.ID, Amount: "2.00", Date: "2026-08-02", IdempotencyKey: "l2"},
		{SourceAccountID: b.ID, DestinationAccountID: c.ID, Amount: "3.00", Date: "2026-08-03", IdempotencyKey: "l3"},
	} {
		if _, fields, err := s.TransferBetweenAccounts(context.Background(), in); err != nil || len(fields) != 0 {
			t.Fatalf("list setup = %v %v", fields, err)
		}
	}
	if _, _, err := s.ReverseAccountTransfer(context.Background(), account.ReverseTransferInput{TransferID: 2, IdempotencyKey: "l2-reverse"}); err != nil {
		t.Fatalf("reverse list transfer: %v", err)
	}
	if _, _, err := s.ReverseAccountTransfer(context.Background(), account.ReverseTransferInput{TransferID: 3, IdempotencyKey: "l3-reverse"}); err != nil {
		t.Fatalf("reverse second list transfer: %v", err)
	}
	status := "reversed"
	pageSize, offset := int64(1), int64(0)
	page, fields, err := s.ListTransfers(context.Background(), account.ListTransfersInput{Status: &status, Limit: &pageSize, Offset: &offset})
	if err != nil || len(fields) != 0 || len(page.Transfers) != 1 || page.Transfers[0].ID != 3 || !page.Page.HasMore || page.Page.Total != 2 {
		t.Fatalf("reversed page = %+v %v %v", page, fields, err)
	}
	accountFilter := a.ID
	all, fields, err := s.ListTransfers(context.Background(), account.ListTransfersInput{AccountID: &accountFilter})
	if err != nil || len(fields) != 0 || len(all.Transfers) != 3 || all.Transfers[0].ID != 4 || all.Transfers[1].ID != 2 || all.Transfers[2].ID != 1 {
		t.Fatalf("account filter = %+v %v %v", all, fields, err)
	}
	if _, _, err := s.Disable(context.Background(), a.ID); err == nil {
		t.Fatal("disable non-zero historical account unexpectedly succeeded")
	}
	// Bring A to zero using factual activity; its historical transfers remain
	// listable after the account is inactive.
	mustRecord(t, s, account.RecordInput{AccountID: a.ID, Type: "withdrawal", Amount: "9.00", Date: "2026-09-01", IdempotencyKey: "zero-a"})
	if _, _, err := s.Disable(context.Background(), a.ID); err != nil {
		t.Fatalf("disable historical account: %v", err)
	}
	historical, fields, err := s.ListTransfers(context.Background(), account.ListTransfersInput{AccountID: &accountFilter})
	if err != nil || len(fields) != 0 || len(historical.Transfers) != 3 {
		t.Fatalf("inactive historical transfers = %+v %v %v", historical, fields, err)
	}
}

func TestTransferListSourceDestinationDateFiltersAndTieOrder(t *testing.T) {
	s := openStore(t)
	a := mustCreate(t, s, "A", "checking", "10.00")
	b := mustCreate(t, s, "B", "savings", "0.00")
	c := mustCreate(t, s, "C", "cash", "0.00")
	for _, in := range []account.TransferInput{
		{SourceAccountID: a.ID, DestinationAccountID: b.ID, Amount: "1.00", Date: "2026-08-01", IdempotencyKey: "filter-1"},
		{SourceAccountID: c.ID, DestinationAccountID: a.ID, Amount: "2.00", Date: "2026-08-02", IdempotencyKey: "filter-2"},
		{SourceAccountID: a.ID, DestinationAccountID: c.ID, Amount: "3.00", Date: "2026-08-02", IdempotencyKey: "filter-3"},
	} {
		if _, fields, err := s.TransferBetweenAccounts(context.Background(), in); err != nil || len(fields) != 0 {
			t.Fatalf("filter setup = %v %v", fields, err)
		}
	}
	sourceID, destinationID := a.ID, a.ID
	startDate, endDate := "2026-08-02", "2026-08-02"
	source, fields, err := s.ListTransfers(context.Background(), account.ListTransfersInput{SourceAccountID: &sourceID})
	if err != nil || len(fields) != 0 || len(source.Transfers) != 2 || source.Transfers[0].ID != 3 || source.Transfers[1].ID != 1 {
		t.Fatalf("source filter = %+v %v %v", source, fields, err)
	}
	destination, fields, err := s.ListTransfers(context.Background(), account.ListTransfersInput{DestinationAccountID: &destinationID})
	if err != nil || len(fields) != 0 || len(destination.Transfers) != 1 || destination.Transfers[0].ID != 2 {
		t.Fatalf("destination filter = %+v %v %v", destination, fields, err)
	}
	byDate, fields, err := s.ListTransfers(context.Background(), account.ListTransfersInput{StartDate: &startDate, EndDate: &endDate})
	if err != nil || len(fields) != 0 || len(byDate.Transfers) != 2 || byDate.Transfers[0].ID != 3 || byDate.Transfers[1].ID != 2 {
		t.Fatalf("date filter = %+v %v %v", byDate, fields, err)
	}
}

func TestTransferInTxRollsBackWithCaller(t *testing.T) {
	s := openStore(t)
	source := mustCreate(t, s, "Checking", "checking", "10.00")
	destination := mustCreate(t, s, "Savings", "savings", "0.00")
	tx, err := s.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	result, err := account.TransferInTx(context.Background(), tx, account.TransferInTxInput{
		SourceAccountID: source.ID, DestinationAccountID: destination.ID,
		AmountHundredths: 500, Date: "2026-09-01", IdempotencyKey: "caller-owned",
		Timestamp: "2026-09-01T14:30:00.000Z",
	})
	if err != nil || result.Transfer.ID == 0 {
		_ = tx.Rollback()
		t.Fatalf("TransferInTx = %+v %v", result, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var count int
	if err := s.DB.QueryRowContext(context.Background(), "SELECT count(*) FROM account_transfers").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("caller rollback transfer count = %d", count)
	}
}
