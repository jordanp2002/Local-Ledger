package contract_test

import (
	"encoding/json"
	"testing"

	"github.com/jordanp2002/local-finance-mcp/internal/contract"
)

func TestFieldIssueJSONShape(t *testing.T) {
	got, err := json.Marshal(contract.FieldIssue{
		Field:  "limit",
		Reason: "must be between 1 and 200",
	})
	if err != nil {
		t.Fatalf("marshal field issue: %v", err)
	}

	want := `{"field":"limit","reason":"must be between 1 and 200"}`
	if string(got) != want {
		t.Fatalf("field issue JSON = %s, want %s", got, want)
	}
}
