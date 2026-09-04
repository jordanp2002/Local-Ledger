package account_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jordanp2002/local-finance-mcp/internal/account"
	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

func openMutableStore(t *testing.T, now *time.Time) *account.Store {
	t.Helper()
	base := *now
	s := openStoreWithClock(t, base)
	s.Now = func() time.Time { return *now }
	return s
}

func mustRecord(t *testing.T, s *account.Store, in account.RecordInput) account.RecordResult {
	t.Helper()
	res, fields, err := s.RecordActivity(context.Background(), in)
	if err != nil {
		t.Fatalf("Record(%+v): %v", in, err)
	}
	if len(fields) != 0 {
		t.Fatalf("Record(%+v) fields = %v", in, fields)
	}
	return res
}

func mustReconcile(t *testing.T, s *account.Store, in account.ReconcileInput) account.ReconcileResult {
	t.Helper()
	res, fields, err := s.ReconcileBalance(context.Background(), in)
	if err != nil {
		t.Fatalf("Reconcile(%+v): %v", in, err)
	}
	if len(fields) != 0 {
		t.Fatalf("Reconcile(%+v) fields = %v", in, fields)
	}
	return res
}

func mustReverse(t *testing.T, s *account.Store, in account.ReverseInput) account.ReverseResult {
	t.Helper()
	res, fields, err := s.ReverseActivity(context.Background(), in)
	if err != nil {
		t.Fatalf("Reverse(%+v): %v", in, err)
	}
	if len(fields) != 0 {
		t.Fatalf("Reverse(%+v) fields = %v", in, fields)
	}
	return res
}

func mustListActivity(t *testing.T, s *account.Store, in account.ListActivityInput) account.ListActivityResult {
	t.Helper()
	res, fields, err := s.ListActivity(context.Background(), in)
	if err != nil {
		t.Fatalf("ListActivity(%+v): %v", in, err)
	}
	if len(fields) != 0 {
		t.Fatalf("ListActivity(%+v) fields = %v", in, fields)
	}
	return res
}

func TestActivityDepositWithdrawalBalances(t *testing.T) {
	for _, opening := range []struct{ name, balance string }{
		{"Pos", "100.00"}, {"Zero", "0.00"}, {"Neg", "-50.00"},
	} {
		s := openStore(t)
		a := mustCreate(t, s, opening.name, "checking", opening.balance)
		dep := mustRecord(t, s, account.RecordInput{AccountID: a.ID, Type: "deposit", Amount: "25.50", Date: "2026-09-01", IdempotencyKey: "dep-" + opening.name})
		if dep.Entry.Kind != "deposit" || dep.Entry.Amount != "25.50" || dep.Entry.Delta != "25.50" {
			t.Fatalf("%s deposit entry = %+v", opening.name, dep.Entry)
		}
		if dep.Entry.TransferID != nil || dep.Entry.ReversalOfEntryID != nil {
			t.Fatalf("%s transfer/reversal = %+v", opening.name, dep.Entry)
		}
		if dep.Entry.BalanceAfter != dep.Balance {
			t.Fatalf("%s balance mismatch %q vs %q", opening.name, dep.Entry.BalanceAfter, dep.Balance)
		}
		wd := mustRecord(t, s, account.RecordInput{AccountID: a.ID, Type: "withdrawal", Amount: "10.00", Date: "2026-09-01", IdempotencyKey: "wd-" + opening.name})
		if wd.Entry.Kind != "withdrawal" || wd.Entry.Amount != "10.00" || wd.Entry.Delta != "-10.00" {
			t.Fatalf("%s withdrawal entry = %+v", opening.name, wd.Entry)
		}
		got := mustList(t, s, account.ListInput{})
		wantAfter := map[string]string{"Pos": "115.50", "Zero": "15.50", "Neg": "-34.50"}[opening.name]
		if got[0].CurrentBalance != wantAfter {
			t.Fatalf("%s balance = %q want %q", opening.name, got[0].CurrentBalance, wantAfter)
		}
	}
}

func TestActivityValidation(t *testing.T) {
	s := openStore(t)
	a := mustCreate(t, s, "Checking", "checking", "10.00")
	for _, in := range []account.RecordInput{
		{AccountID: 0, Type: "deposit", Amount: "1.00", Date: "2026-09-01", IdempotencyKey: "k1"},
		{AccountID: a.ID, Type: "", Amount: "1.00", Date: "2026-09-01", IdempotencyKey: "k2"},
		{AccountID: a.ID, Type: "transfer", Amount: "1.00", Date: "2026-09-01", IdempotencyKey: "k3"},
		{AccountID: a.ID, Type: "deposit", Amount: "0.00", Date: "2026-09-01", IdempotencyKey: "k4"},
		{AccountID: a.ID, Type: "deposit", Amount: "-1.00", Date: "2026-09-01", IdempotencyKey: "k5"},
		{AccountID: a.ID, Type: "deposit", Amount: "1.000", Date: "2026-09-01", IdempotencyKey: "k6"},
		{AccountID: a.ID, Type: "deposit", Amount: "1.00", Date: "", IdempotencyKey: "k7"},
		{AccountID: a.ID, Type: "deposit", Amount: "1.00", Date: "2026-09-02", IdempotencyKey: "k8"},
		{AccountID: a.ID, Type: "deposit", Amount: "1.00", Date: "09/01/2026", IdempotencyKey: "k9"},
		{AccountID: a.ID, Type: "deposit", Amount: "1.00", Date: "2026-09-01", IdempotencyKey: ""},
		{AccountID: a.ID, Type: "deposit", Amount: "1.00", Date: "2026-09-01", IdempotencyKey: "k\x0010"},
	} {
		_, fields, err := s.RecordActivity(context.Background(), in)
		if err != nil || len(fields) == 0 {
			t.Fatalf("Record(%+v) = %v %v, want fields", in, err, fields)
		}
	}
	if _, fields, _ := s.RecordActivity(context.Background(), account.RecordInput{AccountID: a.ID, Type: "deposit", Amount: "1.00", Date: "2026-09-01", Note: strPtr("a\x00b"), NotePresent: true, IdempotencyKey: "knote"}); len(fields) == 0 {
		t.Fatal("NUL note accepted")
	}
	res := mustRecord(t, s, account.RecordInput{AccountID: a.ID, Type: "deposit", Amount: "2.00", Date: "2026-09-01", Note: strPtr("  padded  "), NotePresent: true, IdempotencyKey: "knorm"})
	if res.Entry.Note == nil || *res.Entry.Note != "padded" {
		t.Fatalf("note = %+v", res.Entry.Note)
	}
	cleared := mustRecord(t, s, account.RecordInput{AccountID: a.ID, Type: "deposit", Amount: "2.00", Date: "2026-09-01", Note: strPtr("   "), NotePresent: true, IdempotencyKey: "knorm2"})
	if cleared.Entry.Note != nil {
		t.Fatalf("blank note = %+v", cleared.Entry.Note)
	}
}

func TestActivityExactAndConflictingReplay(t *testing.T) {
	s := openStore(t)
	a := mustCreate(t, s, "Checking", "checking", "0.00")
	first := mustRecord(t, s, account.RecordInput{AccountID: a.ID, Type: "deposit", Amount: "5.00", Date: "2026-09-01", Note: strPtr("n"), NotePresent: true, IdempotencyKey: "k"})
	replay, fields, err := s.RecordActivity(context.Background(), account.RecordInput{AccountID: a.ID, Type: "deposit", Amount: "5.00", Date: "2026-09-01", Note: strPtr("n"), NotePresent: true, IdempotencyKey: "k"})
	if err != nil || len(fields) != 0 || !replay.IdempotentReplay || replay.Entry.ID != first.Entry.ID || replay.Balance != first.Balance {
		t.Fatalf("replay = %+v %v %v", replay, fields, err)
	}
	_, _, err = s.RecordActivity(context.Background(), account.RecordInput{AccountID: a.ID, Type: "deposit", Amount: "6.00", Date: "2026-09-01", Note: strPtr("n"), NotePresent: true, IdempotencyKey: "k"})
	if !errors.Is(err, account.ErrIdempotencyConflict) {
		t.Fatalf("conflict err = %v", err)
	}
}

func TestActivityReplayAfterBackdatedWrites(t *testing.T) {
	s := openStore(t)
	a := mustCreate(t, s, "Checking", "checking", "100.00")
	deposit := account.RecordInput{AccountID: a.ID, Type: "deposit", Amount: "10.00", Date: "2026-09-01", IdempotencyKey: "deposit"}
	first := mustRecord(t, s, deposit)
	reconcile := account.ReconcileInput{AccountID: a.ID, Balance: "120.00", IdempotencyKey: "reconcile"}
	adjusted := mustReconcile(t, s, reconcile)
	reverse := account.ReverseInput{EntryID: first.Entry.ID, IdempotencyKey: "reverse"}
	reversed := mustReverse(t, s, reverse)
	backdated := mustRecord(t, s, account.RecordInput{AccountID: a.ID, Type: "deposit", Amount: "5.00", Date: "2026-08-01", IdempotencyKey: "backdated"})
	if backdated.Balance != "115.00" {
		t.Fatalf("backdated resulting balance = %s, want 115.00", backdated.Balance)
	}
	if replay := mustRecord(t, s, deposit); !replay.IdempotentReplay || replay.Balance != first.Balance || replay.Entry.BalanceAfter != first.Entry.BalanceAfter {
		t.Fatalf("deposit replay = %+v, original = %+v", replay, first)
	}
	if replay := mustReconcile(t, s, reconcile); !replay.IdempotentReplay || replay.Balance != adjusted.Balance || replay.PreviousBalance != adjusted.PreviousBalance || replay.Adjustment != adjusted.Adjustment {
		t.Fatalf("reconciliation replay = %+v, original = %+v", replay, adjusted)
	}
	if replay := mustReverse(t, s, reverse); !replay.IdempotentReplay || replay.Changed || replay.Balance != reversed.Balance {
		t.Fatalf("reversal replay = %+v, original = %+v", replay, reversed)
	}
	history := mustListActivity(t, s, account.ListActivityInput{AccountID: a.ID})
	for i, want := range []string{"105.00", "115.00", "125.00", "115.00"} {
		if history.Entries[i].BalanceAfter != want {
			t.Fatalf("history entry %d balance = %s, want %s", i, history.Entries[i].BalanceAfter, want)
		}
	}
}

func TestActivityConcurrentSameKey(t *testing.T) {
	s := openStore(t)
	a := mustCreate(t, s, "Checking", "checking", "0.00")
	const n = 8
	var wg sync.WaitGroup
	ids := make([]int64, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, fields, err := s.RecordActivity(context.Background(), account.RecordInput{AccountID: a.ID, Type: "deposit", Amount: "3.00", Date: "2026-09-01", IdempotencyKey: "shared"})
			if err != nil || len(fields) != 0 {
				errs[i] = err
				if err == nil {
					errs[i] = errors.New("fields")
				}
				return
			}
			ids[i] = res.Entry.ID
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent err = %v", err)
		}
	}
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("ids = %v", ids)
		}
	}
}

func TestReconcileIncreaseDecreaseNoop(t *testing.T) {
	s := openStore(t)
	a := mustCreate(t, s, "Checking", "checking", "100.00")
	up := mustReconcile(t, s, account.ReconcileInput{AccountID: a.ID, Balance: "150.00", IdempotencyKey: "up"})
	if !up.Changed || up.Adjustment != "50.00" || up.PreviousBalance != "100.00" || up.Balance != "150.00" || up.Entry.Kind != "reconciliation" {
		t.Fatalf("up = %+v", up)
	}
	down := mustReconcile(t, s, account.ReconcileInput{AccountID: a.ID, Balance: "120.00", IdempotencyKey: "down"})
	if down.Adjustment != "-30.00" || down.Balance != "120.00" {
		t.Fatalf("down = %+v", down)
	}
	noop := mustReconcile(t, s, account.ReconcileInput{AccountID: a.ID, Balance: "120.00", IdempotencyKey: "noop"})
	if noop.Changed || noop.Adjustment != "0.00" || noop.Entry != nil || noop.Balance != "120.00" {
		t.Fatalf("noop = %+v", noop)
	}
	replay := mustReconcile(t, s, account.ReconcileInput{AccountID: a.ID, Balance: "120.00", IdempotencyKey: "noop"})
	if !replay.IdempotentReplay || replay.Changed || replay.Balance != "120.00" {
		t.Fatalf("noop replay = %+v", replay)
	}
	mustRecord(t, s, account.RecordInput{AccountID: a.ID, Type: "deposit", Amount: "10.00", Date: "2026-09-01", IdempotencyKey: "later"})
	still := mustReconcile(t, s, account.ReconcileInput{AccountID: a.ID, Balance: "120.00", IdempotencyKey: "noop"})
	if !still.IdempotentReplay || still.Balance != "120.00" || still.PreviousBalance != "120.00" {
		t.Fatalf("persisted noop = %+v", still)
	}
	neg := mustReconcile(t, s, account.ReconcileInput{AccountID: a.ID, Balance: "-5.00", IdempotencyKey: "neg"})
	if neg.Balance != "-5.00" {
		t.Fatalf("neg = %+v", neg)
	}
}

func TestReconcileReplayAcrossDays(t *testing.T) {
	day1 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	s := openMutableStore(t, &day1)
	a := mustCreate(t, s, "Checking", "checking", "10.00")
	first := mustReconcile(t, s, account.ReconcileInput{AccountID: a.ID, Balance: "20.00", IdempotencyKey: "k"})
	day1 = time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	replay := mustReconcile(t, s, account.ReconcileInput{AccountID: a.ID, Balance: "20.00", IdempotencyKey: "k"})
	if !replay.IdempotentReplay || replay.Entry.ID != first.Entry.ID {
		t.Fatalf("cross-day replay = %+v first=%+v", replay, first)
	}
	rev := mustReverse(t, s, account.ReverseInput{EntryID: first.Entry.ID, IdempotencyKey: "rv"})
	day1 = time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	revReplay := mustReverse(t, s, account.ReverseInput{EntryID: first.Entry.ID, IdempotencyKey: "rv"})
	if !revReplay.IdempotentReplay || revReplay.Entry.ID != rev.Entry.ID {
		t.Fatalf("reverse cross-day = %+v", revReplay)
	}
}

func TestReverseKinds(t *testing.T) {
	s := openStore(t)
	a := mustCreate(t, s, "Checking", "checking", "0.00")
	dep := mustRecord(t, s, account.RecordInput{AccountID: a.ID, Type: "deposit", Amount: "10.00", Date: "2026-09-01", IdempotencyKey: "d"})
	wd := mustRecord(t, s, account.RecordInput{AccountID: a.ID, Type: "withdrawal", Amount: "3.00", Date: "2026-09-01", IdempotencyKey: "w"})
	rec := mustReconcile(t, s, account.ReconcileInput{AccountID: a.ID, Balance: "20.00", IdempotencyKey: "r"})
	for _, orig := range []struct {
		id    int64
		delta string
	}{
		{dep.Entry.ID, "-10.00"}, {wd.Entry.ID, "3.00"}, {rec.Entry.ID, "-13.00"},
	} {
		rv := mustReverse(t, s, account.ReverseInput{EntryID: orig.id, IdempotencyKey: "rv-" + orig.delta})
		if rv.Entry.Kind != "reversal" || rv.Entry.Delta != orig.delta || rv.Entry.ReversalOfEntryID == nil || *rv.Entry.ReversalOfEntryID != orig.id || !rv.Changed {
			t.Fatalf("reverse %d = %+v", orig.id, rv.Entry)
		}
		if _, _, err := s.ReverseActivity(context.Background(), account.ReverseInput{EntryID: orig.id, Note: strPtr("x"), NotePresent: true, IdempotencyKey: "other"}); !errors.Is(err, account.ErrEntryNotReversible) {
			t.Fatalf("differing reversal err = %v", err)
		}
		dup, fields, err := s.ReverseActivity(context.Background(), account.ReverseInput{EntryID: orig.id, IdempotencyKey: "other2"})
		if err != nil || len(fields) != 0 || dup.Changed || dup.IdempotentReplay || dup.Entry.ID != rv.Entry.ID {
			t.Fatalf("duplicate reversal = %+v %v %v", dup, fields, err)
		}
	}
	sameKey := mustReverse(t, s, account.ReverseInput{EntryID: dep.Entry.ID, IdempotencyKey: "rv--10.00"})
	if sameKey.Changed || !sameKey.IdempotentReplay {
		t.Fatalf("same-key replay = %+v", sameKey)
	}
	if _, _, err := s.ReverseActivity(context.Background(), account.ReverseInput{EntryID: 9999, IdempotencyKey: "missing"}); !errors.Is(err, account.ErrEntryNotFound) {
		t.Fatalf("missing err = %v", err)
	}
	rv := mustReverse(t, s, account.ReverseInput{EntryID: wd.Entry.ID, IdempotencyKey: "rv-3.00"})
	if _, _, err := s.ReverseActivity(context.Background(), account.ReverseInput{EntryID: rv.Entry.ID, IdempotencyKey: "r-of-r"}); !errors.Is(err, account.ErrEntryNotReversible) {
		t.Fatalf("reversal-of-reversal err = %v", err)
	}
}

func TestReverseInactiveAccount(t *testing.T) {
	s := openStore(t)
	a := mustCreate(t, s, "Empty", "cash", "0.00")
	dep := mustRecord(t, s, account.RecordInput{AccountID: a.ID, Type: "deposit", Amount: "10.00", Date: "2026-09-01", IdempotencyKey: "d"})
	mustRecord(t, s, account.RecordInput{AccountID: a.ID, Type: "withdrawal", Amount: "10.00", Date: "2026-09-01", IdempotencyKey: "w"})
	dis, fields, err := s.Disable(context.Background(), a.ID)
	if err != nil || len(fields) != 0 || dis.Changed != true {
		t.Fatalf("disable = %+v %v %v", dis, fields, err)
	}
	rv := mustReverse(t, s, account.ReverseInput{EntryID: dep.Entry.ID, IdempotencyKey: "rv"})
	if rv.Entry.Kind != "reversal" {
		t.Fatalf("inactive reverse = %+v", rv)
	}
}

func TestRecordReplayAfterDisable(t *testing.T) {
	s := openStore(t)
	a := mustCreate(t, s, "Empty", "cash", "0.00")
	first := mustRecord(t, s, account.RecordInput{AccountID: a.ID, Type: "deposit", Amount: "10.00", Date: "2026-09-01", IdempotencyKey: "d"})
	mustRecord(t, s, account.RecordInput{AccountID: a.ID, Type: "withdrawal", Amount: "10.00", Date: "2026-09-01", IdempotencyKey: "w"})
	if _, _, err := s.Disable(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	replay, fields, err := s.RecordActivity(context.Background(), account.RecordInput{AccountID: a.ID, Type: "deposit", Amount: "10.00", Date: "2026-09-01", IdempotencyKey: "d"})
	if err != nil || len(fields) != 0 || !replay.IdempotentReplay || replay.Entry.ID != first.Entry.ID {
		t.Fatalf("replay after disable = %+v %v %v", replay, fields, err)
	}
}

func TestListActivityFiltersPagination(t *testing.T) {
	s := openStore(t)
	a := mustCreate(t, s, "Checking", "checking", "0.00")
	mustRecord(t, s, account.RecordInput{AccountID: a.ID, Type: "deposit", Amount: "10.00", Date: "2026-08-20", IdempotencyKey: "d1"})
	mustRecord(t, s, account.RecordInput{AccountID: a.ID, Type: "withdrawal", Amount: "2.00", Date: "2026-08-21", IdempotencyKey: "w1"})
	mustRecord(t, s, account.RecordInput{AccountID: a.ID, Type: "deposit", Amount: "5.00", Date: "2026-09-01", IdempotencyKey: "d2"})
	mustReconcile(t, s, account.ReconcileInput{AccountID: a.ID, Balance: "20.00", IdempotencyKey: "r"})
	kind := "deposit"
	filtered := mustListActivity(t, s, account.ListActivityInput{AccountID: a.ID, Kind: &kind})
	if len(filtered.Entries) != 2 {
		t.Fatalf("kind filter = %d", len(filtered.Entries))
	}
	start, end := "2026-08-21", "2026-09-01"
	ranged := mustListActivity(t, s, account.ListActivityInput{AccountID: a.ID, StartDate: &start, EndDate: &end})
	if len(ranged.Entries) != 3 {
		t.Fatalf("range = %d", len(ranged.Entries))
	}
	lim, off := int64(2), int64(0)
	p1 := mustListActivity(t, s, account.ListActivityInput{AccountID: a.ID, Limit: &lim, Offset: &off})
	off = 2
	p2 := mustListActivity(t, s, account.ListActivityInput{AccountID: a.ID, Limit: &lim, Offset: &off})
	if len(p1.Entries) != 2 || len(p2.Entries) != 2 || !p1.Page.HasMore || p2.Page.HasMore {
		t.Fatalf("pages %+v %+v", p1.Page, p2.Page)
	}
	if p1.Entries[0].BalanceAfter != "10.00" || p1.Entries[1].BalanceAfter != "8.00" || p2.Entries[0].BalanceAfter != "13.00" || p2.Entries[1].BalanceAfter != "20.00" {
		t.Fatalf("running %+v %+v", p1.Entries, p2.Entries)
	}
	if p1.Page.Total != 4 || p2.Page.Total != 4 {
		t.Fatalf("total %+v", p1.Page)
	}
	empty := mustListActivity(t, s, account.ListActivityInput{AccountID: a.ID, StartDate: strPtr("2020-01-01"), EndDate: strPtr("2020-01-31")})
	if empty.Entries == nil || len(empty.Entries) != 0 {
		t.Fatalf("empty = %#v", empty.Entries)
	}
}

func TestDerivedBalancesDisableGuards(t *testing.T) {
	s := openStore(t)
	a := mustCreate(t, s, "Checking", "checking", "5.00")
	if got := mustList(t, s, account.ListInput{})[0].CurrentBalance; got != "5.00" {
		t.Fatalf("derived = %q", got)
	}
	mustRecord(t, s, account.RecordInput{AccountID: a.ID, Type: "withdrawal", Amount: "5.00", Date: "2026-09-01", IdempotencyKey: "w"})
	if got := mustList(t, s, account.ListInput{})[0].CurrentBalance; got != "0.00" {
		t.Fatalf("derived zero = %q", got)
	}
	if _, _, err := s.Disable(context.Background(), a.ID); err != nil {
		t.Fatalf("disable zero: %v", err)
	}
	b := mustCreate(t, s, "Other", "cash", "0.00")
	mustRecord(t, s, account.RecordInput{AccountID: b.ID, Type: "deposit", Amount: "1.00", Date: "2026-09-01", IdempotencyKey: "d"})
	if _, _, err := s.Disable(context.Background(), b.ID); !errors.Is(err, account.ErrBalanceNotZero) {
		t.Fatalf("disable nonzero err = %v", err)
	}
}

func TestActivityOverflowRollback(t *testing.T) {
	s := openStore(t)
	a := mustCreate(t, s, "Big", "checking", "92233720368547758.07")
	_, _, err := s.RecordActivity(context.Background(), account.RecordInput{AccountID: a.ID, Type: "deposit", Amount: "0.01", Date: "2026-09-01", IdempotencyKey: "over"})
	if err == nil {
		t.Fatal("want overflow")
	}
	if got := mustList(t, s, account.ListInput{})[0].CurrentBalance; got != "92233720368547758.07" {
		t.Fatalf("balance after overflow = %q", got)
	}
	if got := mustListActivity(t, s, account.ListActivityInput{AccountID: a.ID}); len(got.Entries) != 0 {
		t.Fatalf("entries after overflow = %d", len(got.Entries))
	}
}

func TestEntriesImmutableTimestamps(t *testing.T) {
	s := openStore(t)
	a := mustCreate(t, s, "Checking", "checking", "0.00")
	first := mustRecord(t, s, account.RecordInput{AccountID: a.ID, Type: "deposit", Amount: "1.00", Date: "2026-09-01", IdempotencyKey: "d"})
	second := mustRecord(t, s, account.RecordInput{AccountID: a.ID, Type: "deposit", Amount: "1.00", Date: "2026-09-01", IdempotencyKey: "d2"})
	if first.Entry.CreatedAt != "2026-09-01T14:30:00.000Z" || second.Entry.CreatedAt != "2026-09-01T14:30:00.000Z" {
		t.Fatalf("timestamps %q %q", first.Entry.CreatedAt, second.Entry.CreatedAt)
	}
	if second.Entry.ID != first.Entry.ID+1 {
		t.Fatalf("ids %d %d", first.Entry.ID, second.Entry.ID)
	}
	mustReverse(t, s, account.ReverseInput{EntryID: first.Entry.ID, IdempotencyKey: "rv"})
	listed := mustListActivity(t, s, account.ListActivityInput{AccountID: a.ID})
	var orig *contract.AccountEntry
	for i, e := range listed.Entries {
		if e.ID == first.Entry.ID {
			orig = &listed.Entries[i]
		}
	}
	if orig == nil || orig.Delta != "1.00" || orig.Kind != "deposit" {
		t.Fatalf("original mutated %+v", orig)
	}
	var db *sql.DB = s.DB
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM account_entries WHERE id = ? AND delta_hundredths = 100`, first.Entry.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("row changed %d %v", count, err)
	}
}

func TestReconcileConcurrentSameKey(t *testing.T) {
	s := openStore(t)
	a := mustCreate(t, s, "Checking", "checking", "10.00")
	const n = 8
	var wg sync.WaitGroup
	balances := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, fields, err := s.ReconcileBalance(context.Background(), account.ReconcileInput{AccountID: a.ID, Balance: "30.00", IdempotencyKey: "shared-rec"})
			if err != nil || len(fields) != 0 {
				errs[i] = err
				if err == nil {
					errs[i] = errors.New("fields")
				}
				return
			}
			balances[i] = res.Balance
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent reconcile err = %v", err)
		}
	}
	for _, b := range balances {
		if b != "30.00" {
			t.Fatalf("balances = %v", balances)
		}
	}
	if got := mustListActivity(t, s, account.ListActivityInput{AccountID: a.ID}); len(got.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(got.Entries))
	}
}

func TestConcurrentDistinctBalanceActivity(t *testing.T) {
	s := openStore(t)
	a := mustCreate(t, s, "Checking", "checking", "100.00")
	const deposits = 8
	var wg sync.WaitGroup
	errs := make([]error, deposits+1)
	for i := 0; i < deposits; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, fields, err := s.RecordActivity(context.Background(), account.RecordInput{AccountID: a.ID, Type: "deposit", Amount: "5.00", Date: "2026-09-01", IdempotencyKey: "conc-dep-" + string(rune('a'+i))})
			if err != nil || len(fields) != 0 {
				errs[i] = err
				if err == nil {
					errs[i] = errors.New("fields")
				}
			}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, fields, err := s.ReconcileBalance(context.Background(), account.ReconcileInput{AccountID: a.ID, Balance: "200.00", IdempotencyKey: "conc-rec"})
		if err != nil || len(fields) != 0 {
			errs[deposits] = err
			if err == nil {
				errs[deposits] = errors.New("fields")
			}
		}
	}()
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent err = %v", err)
		}
	}
	listed := mustListActivity(t, s, account.ListActivityInput{AccountID: a.ID})
	if len(listed.Entries) != deposits+1 {
		t.Fatalf("entries = %d, want %d", len(listed.Entries), deposits+1)
	}
	accounts := mustList(t, s, account.ListInput{})
	var total int64
	for _, e := range listed.Entries {
		delta, err := contract.ParseSignedAmount(e.Delta)
		if err != nil {
			t.Fatalf("delta %q: %v", e.Delta, err)
		}
		total += delta
	}
	opening, err := contract.ParseSignedAmount(accounts[0].OpeningBalance)
	if err != nil {
		t.Fatal(err)
	}
	current, err := contract.ParseSignedAmount(accounts[0].CurrentBalance)
	if err != nil {
		t.Fatal(err)
	}
	if current != opening+total {
		t.Fatalf("balance %q != opening + deltas", accounts[0].CurrentBalance)
	}
	if listed.Entries[len(listed.Entries)-1].BalanceAfter != accounts[0].CurrentBalance {
		t.Fatalf("last balance_after %q != current %q", listed.Entries[len(listed.Entries)-1].BalanceAfter, accounts[0].CurrentBalance)
	}
}
