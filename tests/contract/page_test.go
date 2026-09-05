package contract_test

import (
	"encoding/json"
	"testing"

	"github.com/jordanp2002/Local-Ledger/internal/contract"
)

func TestPageJSONShape(t *testing.T) {
	got, err := json.Marshal(contract.Page{
		Limit:    50,
		Offset:   10,
		Returned: 5,
		Total:    25,
		HasMore:  true,
	})
	if err != nil {
		t.Fatalf("marshal page: %v", err)
	}

	want := `{"limit":50,"offset":10,"returned":5,"total":25,"has_more":true}`
	if string(got) != want {
		t.Fatalf("page JSON = %s, want %s", got, want)
	}
}
