package account_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jordanp2002/Local-Ledger/internal/account"
	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/database"
)

func openStore(t *testing.T) *account.Store {
	t.Helper()
	return openStoreWithClock(t, time.Date(2026, 9, 1, 14, 30, 0, 0, time.UTC))
}

func openStoreWithClock(t *testing.T, now time.Time) *account.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "finance.db")
	db, err := database.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			t.Errorf("close: %v", err)
		}
	})
	return &account.Store{DB: db, Now: func() time.Time { return now }}
}

func strPtr(s string) *string { return &s }

func mustCreate(t *testing.T, s *account.Store, name, typ, balance string) contract.Account {
	t.Helper()
	res, fields, err := s.Create(context.Background(), account.CreateInput{Name: name, Type: typ, OpeningBalance: balance})
	if err != nil {
		t.Fatalf("Create(%q): %v", name, err)
	}
	if len(fields) != 0 {
		t.Fatalf("Create(%q) fields = %v", name, fields)
	}
	if !res.Created || res.Reactivated {
		t.Fatalf("Create(%q) created=%v reactivated=%v", name, res.Created, res.Reactivated)
	}
	return res.Account
}

func mustUpdate(t *testing.T, s *account.Store, in account.UpdateInput) account.UpdateResult {
	t.Helper()
	res, fields, err := s.Update(context.Background(), in)
	if err != nil {
		t.Fatalf("Update(%+v): %v", in, err)
	}
	if len(fields) != 0 {
		t.Fatalf("Update(%+v) fields = %v", in, fields)
	}
	return res
}

func mustList(t *testing.T, s *account.Store, in account.ListInput) []contract.Account {
	t.Helper()
	got, fields, err := s.List(context.Background(), in)
	if err != nil {
		t.Fatalf("List(%+v): %v", in, err)
	}
	if len(fields) != 0 {
		t.Fatalf("List(%+v) fields = %v", in, fields)
	}
	return got
}

func TestCreateReturnsCanonicalRecord(t *testing.T) {
	s := openStore(t)
	for _, typ := range []string{"checking", "savings", "cash", "other"} {
		res, fields, err := s.Create(context.Background(), account.CreateInput{Name: "Acct " + typ, Type: typ, OpeningBalance: "2500.00"})
		if err != nil || len(fields) != 0 {
			t.Fatalf("Create(%s): %v %v", typ, err, fields)
		}
		a := res.Account
		if a.Type != typ || a.OpeningBalance != "2500.00" || a.CurrentBalance != "2500.00" || !a.Active || a.Note != nil {
			t.Fatalf("account = %+v, want canonical shape", a)
		}
		if a.CreatedAt != "2026-09-01T14:30:00.000Z" || a.UpdatedAt != "2026-09-01T14:30:00.000Z" {
			t.Fatalf("timestamps = %q %q", a.CreatedAt, a.UpdatedAt)
		}
	}
}

func TestCreateSignedBalances(t *testing.T) {
	s := openStore(t)
	cases := map[string]string{"Pos": "10.50", "Zero": "0.00", "Neg": "-10.50"}
	inputs := map[string]string{"Pos": "10.5", "Zero": "0.00", "Neg": "-10.5"}
	for name, want := range cases {
		a := mustCreate(t, s, name, "cash", inputs[name])
		if a.OpeningBalance != want || a.CurrentBalance != want {
			t.Fatalf("%s balance = %q/%q, want %q", name, a.OpeningBalance, a.CurrentBalance, want)
		}
	}
}

func TestCreateInvalid(t *testing.T) {
	s := openStore(t)
	for _, in := range []account.CreateInput{
		{Name: "  ", Type: "checking", OpeningBalance: "1.00"},
		{Name: "A", Type: "", OpeningBalance: "1.00"},
		{Name: "A", Type: "credit", OpeningBalance: "1.00"},
		{Name: "A", Type: "checking", OpeningBalance: ""},
		{Name: "A", Type: "checking", OpeningBalance: "1.000"},
		{Name: "A", Type: "checking", OpeningBalance: "abc"},
		{Name: "A", Type: "checking", OpeningBalance: "92233720368547758.08"},
		{Name: "A", Type: "checking", OpeningBalance: "-92233720368547758.08"},
	} {
		_, fields, err := s.Create(context.Background(), in)
		if err != nil {
			t.Fatalf("Create(%+v) err = %v", in, err)
		}
		if len(fields) == 0 {
			t.Fatalf("Create(%+v) fields empty, want invalid_input", in)
		}
	}
}

func TestCreateDuplicateAndReactivation(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	mustCreate(t, s, "Checking", "checking", "100.00")

	if _, _, err := s.Create(ctx, account.CreateInput{Name: " checking ", Type: "checking", OpeningBalance: "100.00"}); !errors.Is(err, account.ErrAlreadyExists) {
		t.Fatalf("duplicate err = %v, want ErrAlreadyExists", err)
	}

	zero := mustCreate(t, s, "Empty", "savings", "0.00")
	originalCreated := zero.CreatedAt
	if _, _, err := s.Disable(ctx, zero.ID); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	later := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return later }
	note := "restored"
	res, fields, err := s.Create(ctx, account.CreateInput{Name: "empty", Type: "savings", OpeningBalance: "0.00", Note: &note, NotePresent: true})
	if err != nil || len(fields) != 0 || res.Created || !res.Reactivated {
		t.Fatalf("reactivate = %+v %v %v", res, fields, err)
	}
	if res.Account.ID != zero.ID {
		t.Fatalf("reactivated ID = %d, want %d", res.Account.ID, zero.ID)
	}
	if res.Account.CreatedAt != originalCreated {
		t.Fatalf("reactivated created_at = %q, want preserved %q", res.Account.CreatedAt, originalCreated)
	}
	if res.Account.Type != "savings" || res.Account.OpeningBalance != "0.00" || res.Account.CurrentBalance != "0.00" {
		t.Fatalf("reactivated financial identity changed: %+v", res.Account)
	}
	if res.Account.Note == nil || *res.Account.Note != "restored" {
		t.Fatalf("reactivated note = %+v", res.Account.Note)
	}
	if res.Account.UpdatedAt != "2026-09-02T10:00:00.000Z" {
		t.Fatalf("reactivated updated_at = %q", res.Account.UpdatedAt)
	}

	mismatch := mustCreate(t, s, "Mismatch", "cash", "0.00")
	if _, _, err := s.Disable(ctx, mismatch.ID); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if _, fields, err := s.Create(ctx, account.CreateInput{Name: "mismatch", Type: "checking", OpeningBalance: "0.00"}); err != nil || len(fields) == 0 {
		t.Fatalf("type mismatch = %v %v, want field issue", fields, err)
	}
	other := mustCreate(t, s, "Other", "cash", "0.00")
	if _, _, err := s.Disable(ctx, other.ID); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if _, fields, err := s.Create(ctx, account.CreateInput{Name: "other", Type: "cash", OpeningBalance: "5.00"}); err != nil || len(fields) == 0 {
		t.Fatalf("opening mismatch = %v %v, want field issue", fields, err)
	}
}

func TestUpdate(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	a := mustCreate(t, s, "Checking", "checking", "10.00")
	b := mustCreate(t, s, "Savings", "savings", "0.00")

	res := mustUpdate(t, s, account.UpdateInput{ID: a.ID, Name: strPtr("Primary")})
	if !res.Changed || res.Account.Name != "Primary" {
		t.Fatalf("rename = %+v", res)
	}
	res = mustUpdate(t, s, account.UpdateInput{ID: a.ID, Note: strPtr("my note"), NotePresent: true})
	if res.Account.Note == nil || *res.Account.Note != "my note" {
		t.Fatalf("note set = %+v", res.Account)
	}
	res = mustUpdate(t, s, account.UpdateInput{ID: a.ID, Note: nil, NotePresent: true})
	if res.Account.Note != nil {
		t.Fatalf("note clear = %+v", res.Account)
	}
	if _, fields, err := s.Update(ctx, account.UpdateInput{ID: a.ID}); err != nil || len(fields) == 0 {
		t.Fatalf("empty patch = %v %v, want field issue", fields, err)
	}
	before := res.Account.UpdatedAt
	res = mustUpdate(t, s, account.UpdateInput{ID: a.ID, Name: strPtr(res.Account.Name)})
	if res.Changed || res.Account.UpdatedAt != before {
		t.Fatalf("no-op changed=%v ts=%q want %q", res.Changed, res.Account.UpdatedAt, before)
	}
	if _, _, err := s.Update(ctx, account.UpdateInput{ID: a.ID, Name: strPtr("savings")}); !errors.Is(err, account.ErrAlreadyExists) {
		t.Fatalf("collision err = %v", err)
	}
	if _, _, err := s.Disable(ctx, b.ID); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	res = mustUpdate(t, s, account.UpdateInput{ID: b.ID, Name: strPtr("Rainy")})
	if res.Account.Name != "Rainy" {
		t.Fatalf("inactive update = %+v", res)
	}
	if _, _, err := s.Update(ctx, account.UpdateInput{ID: 9999, Name: strPtr("X")}); !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("missing err = %v", err)
	}
}

func TestUpdateCombinedPatch(t *testing.T) {
	s := openStore(t)
	a := mustCreate(t, s, "Checking", "checking", "10.00")
	res := mustUpdate(t, s, account.UpdateInput{ID: a.ID, Name: strPtr("Primary"), Note: strPtr("note"), NotePresent: true})
	if !res.Changed || res.Account.Name != "Primary" || res.Account.Note == nil || *res.Account.Note != "note" {
		t.Fatalf("combined patch = %+v", res.Account)
	}
}

func TestUpdateClockOnlyOnWrite(t *testing.T) {
	fixed := time.Date(2026, 9, 1, 14, 30, 0, 0, time.UTC)
	later := time.Date(2026, 9, 2, 10, 15, 0, 0, time.UTC)
	s := openStoreWithClock(t, fixed)
	calls := 0
	s.Now = func() time.Time {
		calls++
		if calls > 1 {
			return later
		}
		return fixed
	}
	a := mustCreate(t, s, "Checking", "checking", "1.00")
	if calls != 1 {
		t.Fatalf("create clock calls = %d, want 1", calls)
	}
	updated := mustUpdate(t, s, account.UpdateInput{ID: a.ID, Name: strPtr("Primary")})
	if calls != 2 {
		t.Fatalf("update clock calls = %d, want 2", calls)
	}
	if updated.Account.UpdatedAt != "2026-09-02T10:15:00.000Z" {
		t.Fatalf("update updated_at = %q", updated.Account.UpdatedAt)
	}
	noOp := mustUpdate(t, s, account.UpdateInput{ID: a.ID, Name: strPtr("Primary")})
	if calls != 2 {
		t.Fatalf("no-op clock calls = %d, want 2", calls)
	}
	if noOp.Account.UpdatedAt != updated.Account.UpdatedAt {
		t.Fatalf("no-op updated_at = %q, want %q", noOp.Account.UpdatedAt, updated.Account.UpdatedAt)
	}
}

func TestCreateConvertsLocalClockToUTC(t *testing.T) {
	loc := time.FixedZone("EDT", -4*60*60)
	local := time.Date(2026, 9, 1, 0, 30, 0, 0, loc)
	s := openStoreWithClock(t, local)
	res, fields, err := s.Create(context.Background(), account.CreateInput{Name: "Checking", Type: "checking", OpeningBalance: "1.00"})
	if err != nil || len(fields) != 0 {
		t.Fatalf("Create: %v %v", err, fields)
	}
	if res.Account.CreatedAt != "2026-09-01T04:30:00.000Z" || res.Account.UpdatedAt != "2026-09-01T04:30:00.000Z" {
		t.Fatalf("timestamps = %q %q, want UTC conversion", res.Account.CreatedAt, res.Account.UpdatedAt)
	}
}

func TestList(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	mustCreate(t, s, "b Checking", "checking", "1.00")
	mustCreate(t, s, "a Savings", "savings", "2.00")
	z := mustCreate(t, s, "0 Zero", "cash", "0.00")
	if _, _, err := s.Disable(ctx, z.ID); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	got := mustList(t, s, account.ListInput{})
	if len(got) != 2 || got[0].Name != "a Savings" {
		t.Fatalf("list = %+v", got)
	}
	got = mustList(t, s, account.ListInput{IncludeInactive: true})
	if len(got) != 3 || got[0].Name != "a Savings" || got[1].Name != "b Checking" || got[2].Name != "0 Zero" {
		t.Fatalf("include inactive = %+v", got)
	}
	got = mustList(t, s, account.ListInput{Name: strPtr("A SAVINGS")})
	if len(got) != 1 {
		t.Fatalf("name filter = %+v", got)
	}
	got = mustList(t, s, account.ListInput{Type: strPtr("checking")})
	if len(got) != 1 || got[0].Type != "checking" {
		t.Fatalf("type filter = %+v", got)
	}
	empty := mustList(t, s, account.ListInput{Type: strPtr("other")})
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty list nil or non-empty: %#v", empty)
	}
}

func TestDisable(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	z := mustCreate(t, s, "Zero", "cash", "0.00")
	nz := mustCreate(t, s, "NonZero", "cash", "5.00")
	s.Now = func() time.Time { return time.Date(2026, 9, 3, 9, 45, 0, 0, time.UTC) }

	res, fields, err := s.Disable(ctx, z.ID)
	if err != nil || len(fields) != 0 || !res.Changed || res.Account.Active {
		t.Fatalf("disable = %+v %v %v", res, fields, err)
	}
	if res.Account.UpdatedAt != "2026-09-03T09:45:00.000Z" {
		t.Fatalf("disable updated_at = %q", res.Account.UpdatedAt)
	}
	res, fields, err = s.Disable(ctx, z.ID)
	if err != nil || len(fields) != 0 || res.Changed {
		t.Fatalf("repeated disable = %+v %v %v", res, fields, err)
	}
	if _, _, err := s.Disable(ctx, nz.ID); !errors.Is(err, account.ErrBalanceNotZero) {
		t.Fatalf("non-zero err = %v", err)
	}
	if _, _, err := s.Disable(ctx, 9999); !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("missing err = %v", err)
	}
	if _, fields, err := s.Disable(ctx, 0); err != nil || len(fields) == 0 {
		t.Fatalf("zero id = %v %v, want field issue", fields, err)
	}
}

func TestSchemaConstraints(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	mustCreate(t, s, "Checking", "checking", "0.00")
	db := s.DB
	if _, err := db.ExecContext(ctx, `INSERT INTO accounts (name, type, opening_balance_hundredths) VALUES (?, ?, ?)`, "CHECKING", "cash", 0); err == nil {
		t.Fatal("case-insensitive duplicate accepted")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO accounts (name, type, opening_balance_hundredths) VALUES (?, ?, ?)`, "Bad", "credit", 0); err == nil {
		t.Fatal("bad type accepted")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO accounts (name, type, opening_balance_hundredths) VALUES (?, ?, ?)`, "Bad2", "cash", 1.5); err == nil {
		t.Fatal("real balance accepted")
	}
	for _, name := range []string{"", "   ", " Checking", "Checking ", " A B "} {
		if _, err := db.ExecContext(ctx, `INSERT INTO accounts (name, type, opening_balance_hundredths) VALUES (?, ?, ?)`, name, "cash", 0); err == nil {
			t.Fatalf("name %q accepted", name)
		}
	}
	for _, active := range []int{2, -1} {
		if _, err := db.ExecContext(ctx, `INSERT INTO accounts (name, type, opening_balance_hundredths, active) VALUES (?, ?, ?, ?)`, "ActiveProbe", "cash", 0, active); err == nil {
			t.Fatalf("active %d accepted", active)
		}
	}
	res, err := db.ExecContext(ctx, `INSERT INTO accounts (name, type, opening_balance_hundredths) VALUES (?, ?, ?)`, "Defaults", "cash", 100)
	if err != nil {
		t.Fatalf("insert defaults: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("defaults LastInsertId: %v", err)
	}
	var active int
	var note sql.NullString
	var createdAt, updatedAt string
	if err := db.QueryRowContext(ctx, `SELECT active, note, created_at, updated_at FROM accounts WHERE id = ?`, id).Scan(&active, &note, &createdAt, &updatedAt); err != nil {
		t.Fatalf("select defaults: %v", err)
	}
	if active != 1 {
		t.Fatalf("default active = %d, want 1", active)
	}
	if note.Valid {
		t.Fatalf("default note = %q, want NULL", note.String)
	}
	for field, value := range map[string]string{"created_at": createdAt, "updated_at": updatedAt} {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			t.Fatalf("default %s %q does not parse: %v", field, value, err)
		}
		if parsed.Location() != time.UTC {
			t.Fatalf("default %s location = %v, want UTC", field, parsed.Location())
		}
	}
}
