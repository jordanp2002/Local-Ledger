package contract_test

import (
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

func TestNormalizeNameTrimsASCIIWhitespaceOnly(t *testing.T) {
	got := contract.TrimASCIIWhitespace(" \t\n\r\v\fGroceries \t\n\r\v\f")
	if got != "Groceries" {
		t.Fatalf("TrimASCIIWhitespace() = %q, want Groceries", got)
	}
	if got := contract.TrimASCIIWhitespace(" \t\n\r\v\f "); got != "" {
		t.Fatalf("TrimASCIIWhitespace(whitespace-only) = %q, want empty", got)
	}
	if got := contract.TrimASCIIWhitespace("Foo Bar"); got != "Foo Bar" {
		t.Fatalf("TrimASCIIWhitespace(internal space) = %q, want Foo Bar", got)
	}
	if got := contract.TrimASCIIWhitespace("\u00a0"); got != "\u00a0" {
		t.Fatalf("TrimASCIIWhitespace(NBSP) = %q, want NBSP preserved", got)
	}
}
