# Journey 2 — Theo Brooks's high-volume freelance ledger

Theo imports tiny business expenses from another system. This attacks batch
limits, all-or-nothing writes, split-allocation replacement, idempotency
fingerprints, money precision, pagination, ranking ties, explicit rollover
auditing, and report reconciliation.

Database: `/tmp/local-finance-mcp-journeys/theo-freelancer/finance.db`

Follow the suite contract. Prefix names and keys with `Theo J2`. Resolve dates
dynamically and calculate expected totals from final request payloads first.

## Setup and import fixture

Create Software, Travel, Meals, Office, and Fees. Previous budget: 500.00,
1,200.00, 300.00, 800.00, 200.00; total 3,000.00. Carry forward with Travel
1,500.00 and Office 1,000.00; total 3,500.00. Set Fees 150.00 and Software
500.01; final total 3,450.01. Rename Office to Equipment mid-run.

Build exactly 100 current-month rows. Rows 1–95 are 0.01; rows 96–100 are
10.00, 20.00, 30.00, 40.00, 50.00. Total is 150.95. Cycle categories and ten
merchants `Theo J2 Vendor 00`–`09`. Use deterministic dates no later than
`RUN_DATE`, including same-date rows. Alternate omitted/string notes. Map all
vendors first; mostly omit categories, but include explicit same/different
categories to exercise `matched`, `preserved`, and replacement. Record expected
per-category/merchant totals from the actual payload.

## Runbook

1. Discover 36 tools and prove empty shapes. Create the five categories; test
   trimming, case-insensitive duplicate rejection, and deterministic list order.
   Category listing has no query or pagination inputs; verify its schema rejects
   or ignores no invented fields through the client's normal schema validation.
2. Create previous and carried current budgets, then set the two changes above.
   Atomically reject duplicate allocations, missing/inactive category, malformed
   amount, and total overflow; budget reads remain identical.
3. Add one historical transaction per category totaling exactly 100.00. Confirm
   previous monthly/category reports.
4. Create all ten mappings. Replace one twice, rename another, and query a
   mixed-case substring. Verify IDs, order, filters, pages, previous category.
5. Reject empty and 101-row batches with no writes. Submit a 100-row batch whose
   last row is invalid; prove the first 99 are absent via public reads/reports.
6. Submit the valid batch with key `Theo J2 import 100`. Require 100 results,
   total 150.95, request order, unique IDs, and correct mapping actions. Replay
   normalized-equivalent input for identical identities/no spending change.
   Change row 100's note under the key for `idempotency_conflict` and no write.
7. Enable Travel as a sinking fund for the current month. Repeat enable for
   `changed=false`, list it, and reconcile base contribution, zero opening
   balance, batch spending, and closing balance. A rollover involving Travel in
   either current or next month must fail with
   `sinking_fund_rollover_conflict`.
8. Add a 300.00 `Theo J2 Laptop Bundle` split between Software 100.00 and Office
   200.00. Require a single parent transaction, two allocations, no merchant
   mapping mutation, and exact idempotent replay. Patch allocations to Software
   120.00 and Office 180.00, then verify category reports move only the 20.00
   delta. Reject duplicate categories, one allocation, zero values, overflow,
   and mismatched replay atomically.
9. Add 0.01 with key `Theo J2 single penny`; replay, then conflict at 0.02. Add
   the contract's canonical maximum amount temporarily, verify formatting and no
   overflow, then remove it. Reject one cent above maximum, negative, zero,
   exponent, excessive precision, comma formatting, invalid/NUL text/key,
   future date, and malformed dates. Only the penny remains.
10. Add one current Fees expense large enough to exceed the final 150.00 Fees
    base after all earlier allocations. Capture its `rollover_offers`; prove the
    offer itself made no record, present the exact eligible amount, and confirm
    it. Create that amount from `RUN_MONTH` into the immediately following month,
    list it through source/target/category filters and pagination. Attempting to
    carry the current base budget into that future target month must return
    `invalid_input` without mutating the snapshot or rollover. Remove the
    rollover, require repeated removal to fail, then recreate it for final audit
    evidence.
11. Page all current rows with a small limit and derive the expected count from
   accepted writes: every ID is unique, order is newest date/ID, and final
   `has_more=false`. Test out-of-range offset, category, exact merchant,
   inclusive day, combined filters, and no-match `[]`. Category filters select
   the full split parent through the matching allocation; category report totals
   use only the allocation amount. Reject invalid limits, offsets, ranges, and
   category names.
12. Update one ordinary import row across amount, merchant, category, date, and
    note string, then clear its note. ID remains stable and mappings do not
    change. Reject ID 0, missing ID, empty patch, invalid category/amount, and
    future date. Remove another row, then require `transaction_not_found`.
    Recompute accepted totals.
13. Exercise every report across the previous and current months. Verify the
    future target month returns the documented missing-snapshot behavior where
    applicable. Exercise monthly/category and spending with each
    and combined filter, rankings at 1 and 50, comparison, overall/category
    series, and `include_categories: true`. Verify deterministic ties, a stable
    category axis, exact category shares, separate base/rollover/fund fields,
    and compact responses when category expansion is false. Reject category plus
    expansion, ranking limits 0/51, reversed dates, invalid categories,
    reversed/equal comparison, and >24-month series.
14. Rename Office to Equipment and prove budgets, split allocations, old rows,
    mappings, filters, and reports follow its ID. Disable Fees: spending remains,
    current allocation disappears, and mappings become inactive. Re-enable the
    same ID and restore 150.00.
15. Disable the Travel sinking fund and require its exact released closing
    balance and next effective month. Repeat for `changed=false`; list completed
    history. The already-created target-month budget keeps its copied Travel base
    contribution but loses the fund opening balance. Same-month re-enable returns
    `sinking_fund_active`, because the period remains effective through the
    current month; later-month re-enable is left to clock-controlled tests.
16. Remove one mapping; old rows remain and omitted add fails. Recreate it, add
    successfully with omitted category, then remove that transaction.
17. Create Cloud 99.99/Software/day 1, Coworking 250.00/Equipment/day
    `min(15, day(RUN_DATE))`, Late Invoice Tool 0.01/Fees/day 31. Cover null,
    empty, nonempty notes. Reject invalid IDs, amounts, days, categories, notes.
    Patch Cloud's amount/note/day and require stable identity. Disable and
    re-enable Coworking, repeating each for `changed=false`. Verify list ordering.
18. Disable Fees so Late Invoice is blocked if otherwise due. Preview exact
    upcoming, due, and blocked sets based on the date with both preview tools;
    prove no writes. Re-enable Fees/restore budget and require its blocker to
    become due only if reached.
19. Confirm and materialize atomically; verify exact list/report deltas. Retry
    with zero result. Decline any additional rollover offers. Disable all
    templates, repeat one for `changed=false`, and prove generated rows survive.
    Complete all 36 successful tools.

Fail for a partial batch, non-identical replay, split double counting, implicit
rollover creation, sinking-fund/rollover overlap, hidden cent, overflow,
duplicate page row, unstable tie, update-driven mapping change, preview write,
partial materialization, or report disagreement.
