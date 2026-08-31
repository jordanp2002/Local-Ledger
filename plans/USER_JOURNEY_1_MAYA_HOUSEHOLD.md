# Journey 1 — Maya Chen's household ledger

Maya sets up Local Ledger and rolls a household budget into a second month. The
journey keeps the original correction and merchant-mapping story, then adds a
split warehouse purchase, an explicitly confirmed overspending rollover, a
short-lived Health sinking fund, category-inclusive monthly series, recurring
template edits, and due/upcoming previews.

Database: `/tmp/local-finance-mcp-journeys/maya-household/finance.db`

Follow `USER_JOURNEY_TOOL_TEST_PLAN.md`. Prefix every name and key with
`Maya J1`. Resolve `RUN_DATE`, `RUN_MONTH`, and `PREVIOUS_MONTH` at run time.
Historical rows belong to `PREVIOUS_MONTH`; current rows must not be later than
`RUN_DATE`.

## Worked checkpoints

Categories: Groceries, Dining, Transit, and Health, all prefixed `Maya J1`.

Previous budget: 400.00, 150.00, 80.00, 50.00 respectively; total 680.00.
Carry forward with Groceries 350.00 and Transit 90.00; total 640.00. Then set
Dining 160.00 and Health 60.00; final current budget 660.00.

Historical purchases are Metro Market 62.40/Groceries, Cafe 24.10/Dining,
Transit Pass 80.00/Transit, and Family Dinner 175.00/Dining. Spending is 341.50
and remaining is 338.50. Dining spent 199.10 against 150.00, so its eligible
explicit rollover is 49.10. After confirmation, the current Dining base remains
160.00 while its rollover adjustment is -49.10 and its available budget is
110.90. The current monthly base total remains 660.00; available budget is
610.90.

Current ordinary purchases:

| Merchant | Amount | Category | Behavior |
| --- | ---: | --- | --- |
| Maya J1 Metro Market | 42.50, corrected to 45.00 | Groceries | matched |
| Maya J1 Cafe | 18.75 | Dining | matched |
| Maya J1 Unknown Shop | 12.00 | Groceries | mapping created |
| Maya J1 Cafe | 9.25 | Health | one-off preserves Dining mapping |
| Maya J1 Cafe | 5.00 | Dining | proves preservation |
| Maya J1 Corner Store | 20.50 | Groceries | batch; later removed |
| Maya J1 Transit Pass | 25.00 | Transit | batch |
| Maya J1 Warehouse Club | 60.00 | Groceries 40.00 + Health 20.00 | split; no mapping mutation |

Before correction spending is 133.00. After correction it is 135.50. After
removing Corner Store and adding the split purchase it is 175.00: Groceries
97.00, Dining 23.75, Health 29.25, Transit 25.00. With the Dining rollover,
remaining available budget is 435.90. While Health is a sinking fund, its base
contribution is 60.00, opening balance is 0.00, available balance is 60.00, and
closing balance is 30.75.

## Runbook

1. Discover exactly 36 tools. On the fresh DB, every collection endpoint returns
   an empty array with its canonical page/null shape. Current monthly summary
   returns `monthly_budget_not_found`.
2. Create four categories. Repeat Groceries with different ASCII casing and
   surrounding whitespace; expect `category_already_exists`. Verify sorted list
   and stable IDs.
3. Create the previous budget. Reject duplicate category allocations atomically;
   reject a second snapshot with `monthly_budget_already_exists`. Confirm 680.00.
4. Add four historical transactions. Give the first an idempotency key; replay
   it for the same ID and no spending change, then change its amount under that
   key for `idempotency_conflict`. Family Dinner must return a non-mutating
   Dining rollover offer for 49.10. Show it but do not create anything yet;
   confirm spending 341.50 and no rollover records.
5. Carry into the current month with overrides. Verify source month, stable
   category IDs, and 640.00. A second create fails without mutation. Set Dining
   and Health and confirm the 660.00 base total. After explicit confirmation,
   create the 49.10 Dining rollover linked to Family Dinner. Verify its target is
   exactly `RUN_MONTH`, list/filter/page it, and reconcile base 660.00,
   adjustment -49.10, and available 610.90. Remove it, prove 660.00 is restored,
   require repeated removal to return `budget_rollover_not_found`, then recreate
   it from the still-eligible overspending for the remaining journey.
6. Remove the Metro Market mapping created by the historical transaction. Map
   Metro to Groceries and Cafe to Dining. Replace Metro with Dining, inspect
   `previous_category`, restore it, then rename it Metro Market. Verify mapping
   ID stability, mixed-case query `metro`, filters, order, and pagination.
7. Add the first five current rows singly. Require the exact matched/created/
   preserved actions above. Reject unmapped omitted category, nonexistent
   category, zero and malformed amounts, invalid date, and future date. Each
   rejection leaves public counts/totals unchanged.
8. Add Corner Store and Transit Pass with `add_transactions` key
   `Maya J1 batch ordinary`. Replay identically with the same IDs/no duplicates;
   mutate one amount under the key for `idempotency_conflict`. Submit a two-row
   batch with one invalid row and prove neither writes.
9. Enable Health as a sinking fund. Repeat enable for `changed=false`; list it
   and verify base 60.00, opening 0.00, and current spending 9.25. Add Warehouse
   Club with `add_split_transaction`, allocations Groceries 40.00 and Health
   20.00, and a unique idempotency key. Require one 60.00 transaction with two
   stable allocations, exact replay, no merchant mapping change, and fund closing
   balance 30.75. Reject one allocation, duplicate categories, zero allocation,
   and mismatched replay without writes.
10. List current rows newest date then newest ID. Test category, merchant, date,
   and combined filters; `limit=2` pages have no gaps/duplicates; no-match is
   `transactions: []`. Health and Groceries filters both select the 60.00 split
   parent and preserve its complete allocation list; category summaries count
   only 20.00 for Health and 40.00 for Groceries. Confirm 193.00 before
   correction/removal.
11. Exercise all six report tools across both months, every category, filtered
    range and ranking variants, and comparison. Call monthly series in compact,
    single-category, and `include_categories: true` forms. The expanded months
    use the same category IDs/order, reconcile base/rollover/fund totals, and
    expose category budget and spending shares. Reject category plus
    `include_categories`, reversed dates, ranking limit 0, reversed/equal
    comparison, and reversed series.
12. Update Metro 42.50 to 45.00 and explicitly clear its note. ID stays stable
    and total including the split becomes 195.50. An empty patch fails. Remove
    Corner Store; ordinary spending becomes 115.00 and total including the split
    becomes 175.00. Repeat removal for `transaction_not_found`.
13. Rename Transit to Transport. Its ID, old/current rows, budgets, mapping, and
    reports remain linked and display the new name.
14. Map Pharmacy to Health. Attempt to disable Health while its sinking fund is
    active and require `sinking_fund_active` with no change. Disable the fund;
    require released balance 30.75 and next-month `effective_month`. Repeat for
    `changed=false`, then list with `include_history: true`. Now disable Health.
    Its current allocation is removed: base budget becomes 600.00; the existing
    9.25 ordinary spending and 20.00 split allocation remain reported at budget
    0.00. Omitted Pharmacy add returns `merchant_category_inactive`. Add 1.00
    explicitly to Groceries with `replaced_inactive`, then remove it. Re-enable
    Health through create; require the same ID and Pharmacy still mapped to
    Groceries. Restore 60.00. Attempting to re-enable the sinking fund in the
    same month returns `sinking_fund_active`, because the released period remains
    effective through this month.
15. Remove Unknown Shop mapping. Second removal returns
    `known_merchant_not_found`; omitted add returns
    `merchant_category_required`; its old transaction remains.
16. Create Rent 900.00/Groceries/day 1/null note; Streaming 14.99/Dining/day
    `min(15, day(RUN_DATE))`/note `Maya J1 monthly`; Month End
    30.00/Transport/day 31. Reject days 0/32, zero amount, missing/inactive
    category, and invalid note. Patch Streaming to 15.49 and explicitly clear its
    note; a no-op patch returns `changed=false`. Disable and re-enable Rent,
    repeating each operation for `changed=false`. Verify stable IDs, active/day/
    merchant order, and that generated history remains untouched.
17. Snapshot reads, then call `preview_upcoming_transactions` and
    `preview_due_transactions`. Rent and Streaming form a due subtotal of 915.49.
    Month End is due only if its effective day 31 has arrived, making the due
    total 945.49; otherwise it stays upcoming. Declare the branch before calls.
    `blocked=[]`. Re-read and prove both previews wrote nothing. Present due rows.
18. Confirm and materialize. Verify exact rows, notes, due dates, IDs, total, and
    count. Retry: `created=0`, total 0.00, `transactions=[]`. Every list/report
    includes generated rows exactly once and comparison/series deltas reconcile.
    If overspending produces rollover offers, record and decline them so the
    existing Dining audit record is the only created rollover.
19. Disable Month End, repeat for `changed=false`, and verify absence from due
    state. Disable Rent and Streaming. Generated spending remains. Rerun every
    read tool and complete the all-tool gate.

Fail for any unexplained amount/count, partial atomic write, duplicate replay,
identity change, preview side effect, implicit rollover creation, split double
counting, sinking-fund/rollover overlap, hidden inactive spending, missing
generated report row, or inactive recurring template omitted from its list.
