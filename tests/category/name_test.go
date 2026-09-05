package category_test

import (
	"testing"
	"time"

	"github.com/jordanp2002/Local-Ledger/internal/category"
)

func TestLocalMonthTorontoVsUTCSameInstant(t *testing.T) {
	toronto := torontoLocation(t)
	local := time.Date(2026, time.August, 31, 23, 30, 0, 0, toronto)
	utc := local.UTC()

	if got := utc.Format("2006-01-02 15:04"); got != "2026-09-01 03:30" {
		t.Fatalf("same instant in UTC = %s, want 2026-09-01 03:30", got)
	}
	if got := category.LocalMonth(local); got != "2026-08" {
		t.Fatalf("LocalMonth(Toronto 2026-08-31 23:30) = %q, want 2026-08", got)
	}
	if got := category.LocalMonth(utc); got != "2026-09" {
		t.Fatalf("LocalMonth(UTC of same instant) = %q, want 2026-09", got)
	}
}

func TestNormalizeNameTrimsASCIIWhitespaceOnly(t *testing.T) {
	got := category.NormalizeName(" \t\n\r\v\fGroceries \t\n\r\v\f")
	if got != "Groceries" {
		t.Fatalf("NormalizeName() = %q, want Groceries", got)
	}
	if got := category.NormalizeName(" \t\n\r\v\f "); got != "" {
		t.Fatalf("NormalizeName(whitespace-only) = %q, want empty", got)
	}
	if got := category.NormalizeName("Foo Bar"); got != "Foo Bar" {
		t.Fatalf("NormalizeName(internal space) = %q, want Foo Bar", got)
	}
}

func TestNormalizeNamePreservesUnicodeWhitespace(t *testing.T) {
	const nbsp = "\u00a0"
	if got := category.NormalizeName(nbsp); got != nbsp {
		t.Fatalf("NormalizeName(NBSP) = %q, want NBSP preserved", got)
	}
}
