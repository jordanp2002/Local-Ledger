package recurring_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/jordanp2002/Local-Ledger/internal/category"
	"github.com/jordanp2002/Local-Ledger/internal/contract"
	"github.com/jordanp2002/Local-Ledger/internal/recurring"
	"github.com/jordanp2002/Local-Ledger/tests/testutil"
)

func openRecurringStore(t *testing.T) (*recurring.Store, *category.Store, *sql.DB) {
	t.Helper()
	db := testutil.OpenDB(t)
	toronto := time.FixedZone("EDT", -4*60*60)
	now := func() time.Time { return time.Date(2026, 8, 30, 8, 0, 0, 0, toronto) }
	return &recurring.Store{DB: db, Now: now}, &category.Store{DB: db, Now: now}, db
}

func mustCreateCategory(t *testing.T, ctx context.Context, store *category.Store, name string) contract.Category {
	t.Helper()
	cat, created, reactivated, err := store.Create(ctx, name)
	if err != nil {
		t.Fatalf("Create(%q) error = %v", name, err)
	}
	if !created && !reactivated {
		t.Fatalf("Create(%q) failed to create or reactivate", name)
	}
	return cat
}

func mustDisableCategory(t *testing.T, ctx context.Context, store *category.Store, name string) contract.Category {
	t.Helper()
	cat, changed, _, err := store.Disable(ctx, name)
	if err != nil {
		t.Fatalf("Disable(%q) error = %v", name, err)
	}
	if !changed {
		t.Fatalf("Disable(%q) changed = false, want true", name)
	}
	return cat
}

func mustCreateRecurring(t *testing.T, ctx context.Context, store *recurring.Store, in recurring.CreateInput) recurring.CreateResult {
	t.Helper()
	result, issues, err := store.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create(%q) error = %v", in.Merchant, err)
	}
	if len(issues) != 0 {
		t.Fatalf("Create(%q) unexpected field issues = %v", in.Merchant, issues)
	}
	return result
}

func stringPointer(s string) *string {
	return &s
}

func countRows(t *testing.T, ctx context.Context, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return n
}
