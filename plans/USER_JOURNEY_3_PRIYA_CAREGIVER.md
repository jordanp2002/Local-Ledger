# Journey 3 — Priya Nair's changing family budget

Priya manages irregular family and caregiving costs. Categories pause and
return, history is sparse, and bills land at month end. This stresses inactive
categories, split caregiving purchases, an explicit prior-month rollover, an
opt-in Childcare sinking fund, a rectangular 24-month category series,
recurring-template maintenance, blockers, and calendar clamping.

Database: `/tmp/local-finance-mcp-journeys/priya-caregiver/finance.db`

Follow the suite contract. Prefix names/keys with `Priya J3`. Calculate the
inclusive 24-month window ending at `RUN_MONTH` (`WINDOW_START`–`RUN_MONTH`).

Categories: Groceries, Medical, Childcare, Transport, Respite, School. Merchants
include deliberately overlapping `Priya J3 Care` and `Priya J3 Care Plus`, plus
Pharmacy, Market, Bus, and School Board. Exact mappings must never bleed between
overlapping names; substring filters may match both only where documented.

Create the earliest window month explicitly, then carry month by month to the
run month, varying one allocation and recording every expected total. Put
transactions in at least six months including first, previous, current. Preserve
one budgeted zero-spend month and one deliberately unbudgeted sparse month.

## Runbook

1. Discover 36 tools and verify empty lists. Create six categories. Rename School
   to Education and back with stable identity. Test deterministic list order and
   invalid and duplicate names. Do not invent query/page inputs: category listing
   intentionally accepts no filters.
2. Create `WINDOW_START`. For each next stored month, carry forward with a
   deterministic override. Before one carry disable Respite; new snapshot omits
   it while historical snapshots remain. Re-enable same ID and add it to the
   existing month with `set_budgets`.
3. For one next carry, disable every category present in the source and require
   `budget_source_empty` with no destination snapshot. Re-enable same IDs, retry,
   and restore scheduled allocations. Do this inside the historical chain.
4. Skip one intermediate calendar snapshot, then carry into the following month.
   Require `source_month` to be the actual latest prior snapshot. The skipped
   month appears in series with `has_budget=false`; deliberate spending remains.
5. Create all exact mappings; replace/restore/rename/query/page/remove/recreate.
   Prove Care operations never alter Care Plus. Use explicit transactions for
   `created`, `preserved`, and `replaced_inactive` mapping actions.
6. Add individual and batch transactions across six months, including first and
   last calendar days. Exercise single/batch replay and conflict. Reject a mixed
   batch atomically. In `PREVIOUS_MONTH`, add a Medical transaction that makes
   Medical overspent and capture the exact non-mutating rollover offer. Record
   exact month/category/merchant totals from responses.
7. Present and confirm the Medical offer. Create its exact eligible amount from
   `PREVIOUS_MONTH`; require target `RUN_MONTH` and link the source transaction.
   List it through every filter and pagination. Reconcile current Medical base,
   negative rollover adjustment, and available budget. Remove it, prove only the
   current adjustment is restored and repeated removal fails, then recreate it
   for the remaining journey. A correction that would invalidate the active
   rollover must fail atomically with `budget_rollover_dependency_conflict`.
8. Enable Childcare as a current-month sinking fund. Repeat enable for
   `changed=false` and list it. Add one `Priya J3 Care Plus` split purchase across
   Medical and Childcare with two positive allocations. Replay it exactly, then
   reject duplicate categories, one allocation, and changed replay without
   writes. Exact Care/Care Plus merchant matching must remain unchanged because
   split creation never creates or replaces a merchant default.
9. List with boundary dates, categories, overlapping merchant terms, combined
   filters, and full pagination. Verify inclusive bounds, deterministic order,
   uniqueness, `has_more`, and empty arrays. Category filters select the complete
   split parent through the matching allocation; category report totals use only
   that allocation. Reject malformed/reversed ranges and invalid pages.
10. Update one old ordinary row into another historical month/category. Its ID
    remains stable and both months change by inverse deltas. Clear its note and
    reject an empty or future update. Remove a row, reject the repeat, and
    reconcile affected reports.
11. Exercise all reports on budgeted, unbudgeted, zero-spend, inactive-category,
   and current months. Test every range/ranking filter and a ranking tie. Compare
   first/current and previous/current. Run the exact 24-month series in compact,
   single-category, and `include_categories: true` forms. Every expanded month
   must contain the same ordered category IDs; the deliberately missing snapshot
   has null budget-derived fields but retains spending, allocations, and counts.
   Reconcile category base-budget and spending shares, rollover adjustment, and
   Childcare fund balances. Reject category plus expansion, 25 months, and
   reversed/equal comparisons.
12. Disable the Childcare sinking fund. Require its exact released closing
    balance and next effective month; repeated disable is `changed=false`. List
    completed history. Same-month re-enable returns `sinking_fund_active`, since
    the ended period still applies through the current month. Later-month
    re-enable behavior remains covered by clock-controlled tests.
13. Disable Medical after current budget/spending. Allocation disappears,
    historical budgets persist, current spending remains at zero budget, and
    both ordinary and split spending remain visible. Mappings are inactive.
    Explicit Pharmacy/Groceries add must say
    `replaced_inactive`; remove it. Re-enable same ID and restore allocation.
14. Create Mortgage/Groceries/day 1, Therapy/Medical/day
    `min(15, day(RUN_DATE))`, Respite Care/Respite/day 31, and School Fee/School
    day 31. Cover null, empty, nonempty notes. Reject invalid day, amount,
    category, note, and ID. Patch Therapy's amount, note, and day while preserving
    its ID and generated history. Disable and re-enable Mortgage, repeating each
    for `changed=false`. Verify active order and stable IDs.
15. Disable Medical and Respite before preview. Derive upcoming/due/blocked
    sets from `RUN_DATE`. Therapy is blocked because due/inactive. Respite is
    blocked only if its effective day has arrived; otherwise it is not due. A
    day-31 effective date clamps to month end. Call both upcoming and due
    previews and prove neither wrote anything.
16. Re-enable both categories and restore allocations. Preview again; blockers
    clear without writes. Confirm and materialize all-or-nothing. Retry for zero.
    Generated rows appear exactly once in lists and every report. If
    materialization returns rollover offers, present and decline them; no second
    rollover record may appear implicitly.
17. Disable one materialized and one not-yet-due template. Repeat one for
    `changed=false`. Disabled templates are never due/blocked, generated rows
    remain. Disable the rest; inactive rows remain listed after active rows.
18. Remove/recreate a mapping after materialization; recurring rows/templates are
    unaffected. Complete all 36 tools and reconcile the 24-month ledger.

## Calendar evidence

Report every day-31 effective due date. If the run month is shorter, this is a
direct clamp test. If it has 31 days, state that clamping was not exercised; do
not claim it passed. Treat leap-day claims the same way.

Fail if carry-forward resurrects inactive categories, loses re-enabled IDs,
chooses a wrong sparse source, mutates history, creates an implicit rollover,
double-counts a split, overlaps a sinking fund and rollover, hides unbudgeted or
inactive spending, conflates exact merchants, returns a wrong 24-month category
axis, mishandles month end, writes during preview, partially materializes, or
reports disagree.
