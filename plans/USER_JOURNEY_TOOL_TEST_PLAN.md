# Local Ledger adversarial user-journey suite

Run three independent, public-MCP-only journeys. Each represents a different
person, owns a different database, and must exercise all 36 public tools. The
journeys are intentionally redundant at the tool level but differ in data shape,
failure modes, and invariants. One passing persona cannot hide another's defect.

## Journey files

| Scenario | Persona and pressure | Database |
| --- | --- | --- |
| [1](USER_JOURNEY_1_MAYA_HOUSEHOLD.md) | Maya Chen: household corrections, split purchases, explicit rollover, a short-lived sinking fund, and recurring edits | `/tmp/local-finance-mcp-journeys/maya-household/finance.db` |
| [2](USER_JOURNEY_2_THEO_FREELANCER.md) | Theo Brooks: 100-row imports, split business purchases, pagination, rollover audit, and amount bounds | `/tmp/local-finance-mcp-journeys/theo-freelancer/finance.db` |
| [3](USER_JOURNEY_3_PRIYA_CAREGIVER.md) | Priya Nair: inactive categories, sparse 24-month category matrices, sinking funds, recurring blockers, and month-end behavior | `/tmp/local-finance-mcp-journeys/priya-caregiver/finance.db` |

## Parallel isolation contract

One agent owns one scenario and only its listed directory. Before starting, the
agent must report `LOCAL_FINANCE_DB_PATH`, confirm it equals the scenario path,
and confirm the database does not exist. If it exists, stop; do not reuse,
overwrite, or delete it without explicit approval. Create only its parent and
start a fresh server with the absolute path:

```sh
mkdir -p /tmp/local-finance-mcp-journeys/maya-household
LOCAL_FINANCE_DB_PATH=/tmp/local-finance-mcp-journeys/maya-household/finance.db go run ./cmd/local-ledger
```

Substitute the assigned journey's path. Never use `~/LocalLedger/finance.db`.
Do not share a process, database, transcript, result file, IDs, or idempotency
keys. The three journeys may run concurrently.

## Execution contract

1. Use a real MCP client and only public tools for ledger reads and writes.
   Direct SQL, SQLite inspection, repository helpers, and fixture seeding make a
   run invalid.
2. Record local `RUN_DATE`, `RUN_MONTH`, and the immediately preceding
   `PREVIOUS_MONTH` before the first call. Resolve every relative date and report
   it. Never silently reuse an example date.
3. Capture discovery and every raw request/response in order, including error
   codes, field errors, array/null shapes, IDs, pagination, totals, and mapping
   actions.
4. Treat preview as a confirmation boundary: prove it wrote no transaction,
   show exact upcoming/due/blocked rows, and only then materialize.
5. Reconcile counts and money after each mutation cluster. Include deleted rows,
   inactive-category spending, split allocations, generated rows, rollover
   adjustments, sinking-fund opening balances, and idempotent replays.
6. For atomic failures, compare public reads before and after and require no
   observable change.
7. Capture returned IDs; never assume IDs start at 1 or are consecutive.
8. Fail on any unexpected envelope, error, field path, ordering, mapping action,
   shape, identity, count, amount, or side effect.
9. Do not clean up before review. The database and transcript are evidence.
   After approval, the scenario owner may remove its entire directory.
10. Treat every `rollover_offers` array as non-mutating advice. Record the offer,
    show it to the user, and call `create_budget_rollover` only after explicit
    confirmation. Prove declining or merely inspecting an offer writes nothing.
11. A split purchase is one transaction with two or more allocations. Its
    transaction amount must equal the allocation sum, it must not change a
    merchant default, and a category filter must match the parent through its
    allocation. The returned parent keeps its full amount and allocation list;
    category report totals use only that category's allocation rather than
    charging the full purchase to every category.
12. A sinking fund is opt-in: every category begins as a normal resetting
    category. Enabling it must require a current base budget row; while active,
    reports show its base contribution, opening balance, and available budget
    separately. Disabling releases the current closing balance after the current
    month; it does not move cash or edit budget rows. Because the ended period
    remains active through the current month, same-month re-enable must fail;
    later-month re-enable belongs in clock-controlled automated tests.

## Required report

The existing `<scenario>_RESULTS.md` files are historical evidence from the
pre-Phase-5 26-tool suite; do not rewrite or cite them as evidence for this
version. Create a new file beside each plan named
`<scenario>_PHASE_5_RESULTS.md`. Include
environment and resolved dates, version and all 36 discovered names, exact DB
path, chronological transcript (or link), checkpoint reconciliations, expected
errors, coverage checklist, defects, and final pass/fail. Distinguish MCP defects
from test-protocol/reporting defects.

## Shared all-tool gate

Every scenario must successfully call all 36 tools:

- Categories: `create_category`, `list_categories`, `rename_category`,
  `disable_category`.
- Budgets: `create_monthly_budget`, `set_budgets`.
- Merchants: `set_known_merchant`, `list_known_merchants`,
  `rename_known_merchant`, `remove_known_merchant`.
- Transactions: `add_transaction`, `add_split_transaction`, `add_transactions`,
  `list_transactions`, `update_transaction`, `remove_transaction`.
- Reports: `get_monthly_summary`, `get_category_summary`,
  `get_spending_summary`, `list_top_merchants`, `compare_months`,
  `get_monthly_series`.
- Recurring: `create_recurring_transaction`,
  `list_recurring_transactions`, `update_recurring_transaction`,
  `disable_recurring_transaction`, `enable_recurring_transaction`,
  `preview_upcoming_transactions`, `preview_due_transactions`,
  `materialize_due_transactions`.
- Explicit rollovers: `create_budget_rollover`, `list_budget_rollovers`,
  `remove_budget_rollover`.
- Sinking funds: `enable_sinking_fund`, `list_sinking_funds`,
  `disable_sinking_fund`.

For `get_monthly_series`, every scenario must call both the compact form and
`include_categories: true`. The expanded form must have one stable category axis
in every month, including missing-snapshot months; budget-derived fields are
`null` when no snapshot exists, while spending facts remain present. Reject a
request that combines a single-category filter with `include_categories: true`.

Automated tests are useful release evidence but cannot replace these MCP-level
journeys.
