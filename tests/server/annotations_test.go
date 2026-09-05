package server_test

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolAnnotationsDiscovery(t *testing.T) {
	tests := []struct {
		name        string
		readOnly    bool
		destructive *bool
		idempotent  bool
		openWorld   bool
	}{
		{name: "list_categories", readOnly: true, openWorld: false},
		{name: "list_known_merchants", readOnly: true, openWorld: false},
		{name: "list_top_merchants", readOnly: true, openWorld: false},
		{name: "list_transactions", readOnly: true, openWorld: false},
		{name: "get_monthly_summary", readOnly: true, openWorld: false},
		{name: "get_monthly_series", readOnly: true, openWorld: false},
		{name: "get_category_summary", readOnly: true, openWorld: false},
		{name: "get_spending_summary", readOnly: true, openWorld: false},
		{name: "compare_months", readOnly: true, openWorld: false},
		{name: "enable_sinking_fund", destructive: annotationBool(true), idempotent: true, openWorld: false},
		{name: "disable_sinking_fund", destructive: annotationBool(true), idempotent: true, openWorld: false},
		{name: "list_sinking_funds", readOnly: true, openWorld: false},
		{name: "list_budget_rollovers", readOnly: true, openWorld: false},
		{name: "create_budget_rollover", destructive: annotationBool(true), openWorld: false},
		{name: "remove_budget_rollover", destructive: annotationBool(true), openWorld: false},
		{name: "create_monthly_budget", destructive: annotationBool(false), idempotent: true, openWorld: false},
		{name: "create_account", destructive: annotationBool(true), idempotent: true, openWorld: false},
		{name: "update_account", destructive: annotationBool(true), idempotent: true, openWorld: false},
		{name: "disable_account", destructive: annotationBool(true), idempotent: true, openWorld: false},
		{name: "list_accounts", readOnly: true, openWorld: false},
		{name: "record_account_activity", destructive: annotationBool(true), idempotent: true, openWorld: false},
		{name: "reconcile_account_balance", destructive: annotationBool(true), idempotent: true, openWorld: false},
		{name: "list_account_activity", readOnly: true, openWorld: false},
		{name: "reverse_account_activity", destructive: annotationBool(true), idempotent: true, openWorld: false},
		{name: "transfer_between_accounts", destructive: annotationBool(true), idempotent: true, openWorld: false},
		{name: "list_account_transfers", readOnly: true, openWorld: false},
		{name: "reverse_account_transfer", destructive: annotationBool(true), idempotent: true, openWorld: false},
		{name: "create_category", destructive: annotationBool(true), idempotent: true, openWorld: false},
		{name: "create_recurring_transaction", destructive: annotationBool(true), idempotent: false, openWorld: false},
		{name: "update_recurring_transaction", destructive: annotationBool(true), idempotent: true, openWorld: false},
		{name: "enable_recurring_transaction", destructive: annotationBool(true), idempotent: true, openWorld: false},
		{name: "disable_category", destructive: annotationBool(true), idempotent: true, openWorld: false},
		{name: "disable_recurring_transaction", destructive: annotationBool(true), idempotent: true, openWorld: false},
		{name: "list_recurring_transactions", readOnly: true, openWorld: false},
		{name: "preview_due_transactions", readOnly: true, openWorld: false},
		{name: "preview_upcoming_transactions", readOnly: true, openWorld: false},
		{name: "materialize_due_transactions", destructive: annotationBool(true), idempotent: true, openWorld: false},
		{name: "rename_category", destructive: annotationBool(true), idempotent: true, openWorld: false},
		{name: "set_known_merchant", destructive: annotationBool(true), idempotent: true, openWorld: false},
		{name: "rename_known_merchant", destructive: annotationBool(true), idempotent: true, openWorld: false},
		{name: "remove_known_merchant", destructive: annotationBool(true), idempotent: true, openWorld: false},
		{name: "remove_transaction", destructive: annotationBool(true), idempotent: true, openWorld: false},
		{name: "set_budgets", destructive: annotationBool(true), openWorld: false},
		{name: "add_transaction", destructive: annotationBool(true), openWorld: false},
		{name: "add_split_transaction", destructive: annotationBool(true), openWorld: false},
		{name: "add_transactions", destructive: annotationBool(true), idempotent: true, openWorld: false},
		{name: "update_transaction", destructive: annotationBool(true), openWorld: false},
	}

	session := connectCategorySession(t, openCategoryDB(t), fixedTransactionNow, nil)
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	if len(result.Tools) != len(tests) {
		t.Fatalf("tool count = %d, want %d", len(result.Tools), len(tests))
	}
	wantNames := make([]string, 0, len(tests))
	for _, test := range tests {
		wantNames = append(wantNames, test.name)
	}
	sort.Strings(wantNames)
	if got := listedToolNames(result.Tools); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("tools = %v, want %v", got, wantNames)
	}

	byName := make(map[string]*mcp.Tool, len(result.Tools))
	for _, tool := range result.Tools {
		if _, exists := byName[tool.Name]; exists {
			t.Fatalf("duplicate tool %q", tool.Name)
		}
		byName[tool.Name] = tool
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tool := byName[test.name]
			if tool == nil {
				t.Fatalf("tool is not discoverable")
			}
			if tool.Annotations == nil {
				t.Fatal("annotations are nil")
			}

			annotations := tool.Annotations
			if annotations.ReadOnlyHint != test.readOnly {
				t.Errorf("readOnlyHint = %t, want %t", annotations.ReadOnlyHint, test.readOnly)
			}
			if annotations.IdempotentHint != test.idempotent {
				t.Errorf("idempotentHint = %t, want %t", annotations.IdempotentHint, test.idempotent)
			}
			if annotations.OpenWorldHint == nil {
				t.Error("openWorldHint is nil, want explicit false")
			} else if *annotations.OpenWorldHint != test.openWorld {
				t.Errorf("openWorldHint = %t, want %t", *annotations.OpenWorldHint, test.openWorld)
			}
			if test.destructive == nil {
				if annotations.DestructiveHint != nil {
					t.Errorf("destructiveHint = %t, want omitted for read-only tool", *annotations.DestructiveHint)
				}
				return
			}
			if annotations.DestructiveHint == nil {
				t.Fatalf("destructiveHint is nil, want %t", *test.destructive)
			}
			if *annotations.DestructiveHint != *test.destructive {
				t.Errorf("destructiveHint = %t, want %t", *annotations.DestructiveHint, *test.destructive)
			}
		})
	}
}

func annotationBool(value bool) *bool {
	return &value
}
