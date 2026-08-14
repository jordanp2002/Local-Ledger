# Phase 0 Final Contract Review

This document is the approval checklist for the Phase 1 MCP contract. `PLAN.md` remains the detailed source, while this file summarizes every operation, the edge cases already covered, and the questions that still need a final decision.

## Shared contract

- Amounts are unitless decimal strings. Inputs accept zero, one, or two decimal places; outputs always use two places.
- SQLite stores amounts as integer hundredths. Currency is not modeled.
- Transaction amounts must be greater than zero. Budget amounts may be zero.
- Months use `YYYY-MM`; dates use `YYYY-MM-DD`; timestamps use RFC 3339 UTC.
- Defaults and current-date checks use the operating system's local timezone; UTC is used only for stored timestamps.
- Transactions may be dated today or earlier. Future-dated transactions are deferred until after Phase 1.
- Monthly budgets may be created or changed only for the current local month. Past snapshots are immutable and future months cannot be prepared in advance.
- User-provided text has no product-level length limit. Amounts are limited only by the signed 64-bit integer hundredths representation required for safe storage.
- Phase 1 accepts the risk that retrying `add_transaction` after a lost successful response can create a duplicate. There is no idempotency key; duplicates can be corrected with `remove_transaction`.
- Dates in ranges are inclusive.
- Names are trimmed and compared case-insensitively for equality. Merchant similarity remains an LLM decision.
- Category-facing inputs use names. Transaction updates and removals use positive integer IDs.
- Writes validate completely and commit atomically. A failed multi-row operation writes nothing.
- Success returns `{ "ok": true, ... }`. Failure returns `{ "ok": false, "error": ... }` and sets the MCP error status.
- Transaction tools never create or change a monthly budget.
- Historical transactions and budgets are not changed when later current-month snapshots are created or edited.

## Write operations

### 1. `create_category`

Purpose: create one active spending category.

Input:

```json
{ "name": "Groceries" }
```

Output: the category record, plus `created` and `reactivated` booleans.

Covered edge cases:

- Empty or whitespace-only name: `invalid_input`.
- Same name as an active category with different casing: `category_already_exists`; no duplicate is created.
- Same name as an inactive category: the existing category record is made active and returned with `reactivated: true`.
- Categories are never created implicitly from a transaction, budget, or merchant mapping.

Example: `groceries` cannot duplicate active `Groceries`, but it re-enables the same inactive record rather than creating a new category identity.

### 2. `disable_category`

Purpose: hide a category from normal selection and remove its current-month allocation without changing historical data.

Input:

```json
{ "name": "Dining" }
```

Output:

- The inactive category.
- Whether the category state changed.
- The removed current-month budget row, or `null`.

Covered edge cases:

- Earlier monthly budgets remain unchanged.
- Transactions are never deleted or recategorized.
- Merchant mappings remain unchanged.
- Normal category lists hide the inactive category.
- A mapping that points to the inactive category cannot categorize an ambiguous new transaction.
- Supplying an active category for that merchant records the transaction and replaces the unusable mapping.
- Calling it again for an already inactive category succeeds without repeating changes.
- Re-enabling is done through `create_category` with the same name.

Example: disabling `Health` in August removes only its August allocation. July reports and all transactions remain intact. If Shoppers is later mapped to Groceries, re-enabling Health cannot move Shoppers back because only the mapping's current value is retained.

### 3. `create_monthly_budget`

Purpose: create one complete monthly budget snapshot without changing any other month.

Explicit input:

```json
{
  "month": "2026-08",
  "budgets": [
    { "category": "Groceries", "amount": "500.00" },
    { "category": "Dining", "amount": "150.00" }
  ]
}
```

Carry-forward input:

```json
{
  "month": "2026-10",
  "carry_forward": true,
  "overrides": [
    { "category": "Dining", "amount": "100.00" }
  ]
}
```

Output:

- Month.
- Creation mode: `explicit` or `carry_forward`.
- Source month for carry-forward, otherwise `null`.
- Calculated total budget.
- Category budgets ordered alphabetically, case-insensitively.

Covered edge cases:

- Exactly one creation mode is required.
- Explicit mode requires at least one budget.
- Repeated category names in one request are rejected case-insensitively.
- Every referenced category must exist and be active.
- Carry-forward uses the latest earlier monthly snapshot.
- The target must be the current local month; past and future target months are rejected.
- Inactive categories are omitted from copies and cannot be supplied as overrides.
- Overrides are applied atomically with the copy.
- Existing target month: `monthly_budget_already_exists`; the user must use `set_budgets`.
- No earlier source month: `budget_source_not_found`.
- Earlier source exists but every category is inactive: `budget_source_empty`.
- The total is calculated from category rows and is never stored separately.

Example: October can copy September, lower Dining, add a newly created category, and commit the complete October snapshot in one call. August and September remain unchanged.

### 4. `set_budgets`

Purpose: change one or several category allocations in an existing monthly snapshot.

Input:

```json
{
  "month": "2026-08",
  "budgets": [
    { "category": "Groceries", "amount": "300.00" },
    { "category": "Dining", "amount": "100.00" },
    { "category": "Health", "amount": "75.00" }
  ]
}
```

Output: all changed budget records, with a per-row `created` flag, ordered by category name.

Covered edge cases:

- The month must already have a snapshot; otherwise `monthly_budget_not_found`.
- The target must be the current local month; past snapshots and future months cannot be changed.
- This tool cannot accidentally establish an incomplete new month.
- At least one item is required.
- Duplicate category names are rejected case-insensitively.
- Categories must exist and be active.
- A missing category row is added to the existing month; an existing row is replaced.
- Zero is a valid allocation.
- Every supplied change succeeds together or none do.
- No earlier or later month is changed.

Example: after August has been carried forward, Groceries, Dining, and Health can all be changed in one atomic call.

### 5. `set_known_merchant`

Purpose: explicitly create or replace one merchant's current default category.

Input:

```json
{ "merchant": "Shoppers Drug Mart", "category": "Groceries" }
```

Output:

- The active merchant mapping.
- Whether it was newly created.
- The previous category when it changed.

Covered edge cases:

- Merchant matching is trimmed, case-insensitive exact matching.
- The category must exist and be active.
- An existing default is deliberately replaced.
- A mapping that points to an inactive category can be replaced with an active category.
- A same-category call does not create a duplicate.

Example: after Health is made inactive, “Make Shoppers default to Groceries” deliberately replaces its current mapping.

### 6. `add_transaction`

Purpose: record one expense and, when appropriate, establish an initial merchant default.

Input:

```json
{
  "amount": "20.00",
  "merchant": "Metro",
  "category": "Groceries",
  "date": "2026-08-14",
  "note": "Weekly groceries"
}
```

Required: `amount`, `merchant`.

Optional: `category`, `date`, `note`. Date defaults to the server's current local day.

Output:

- The complete transaction record.
- Category source: `provided` or `known_merchant`.
- Merchant mapping action: `created`, `matched`, `preserved`, or `replaced_inactive`.

Covered edge cases:

- Amount must be greater than zero.
- Date may be today or earlier; future dates return `invalid_input`.
- Invalid or inactive supplied category: no transaction or mapping is written.
- No supplied category and no exact mapping: `merchant_category_required`.
- No supplied category and mapping points to an inactive category: `merchant_category_inactive`.
- Supplied category always applies to this transaction.
- If no mapping record exists, the first categorized transaction creates one atomically.
- If an active mapping exists with another category, a one-off supplied category does not replace it.
- If a mapping points to an inactive category, a supplied active category records the transaction and replaces that unusable default atomically.
- Similar, non-exact merchant names are resolved by the LLM using `list_known_merchants`; SQLite does not guess.
- Adding a transaction never creates a monthly budget.

Examples:

- `Metro -> Groceries`; “$20 at Metro” uses Groceries.
- `Metro -> Groceries`; “$20 at Metro in Health” records Health but preserves the Metro default.
- `Metro grocery store` has no exact mapping; the LLM recognizes Metro, supplies Groceries, and the new spelling becomes another exact mapping if no record exists for it.
- Inactive-category `Shoppers -> Health`; an explicit Shoppers/Groceries purchase records Groceries and replaces the mapping. Re-enabling Health later cannot move it back.

### 7. `update_transaction`

Purpose: patch an existing transaction without changing merchant defaults.

Input:

```json
{
  "id": 42,
  "amount": "23.50",
  "merchant": "Metro grocery store",
  "category": "Groceries",
  "date": "2026-08-13",
  "note": null
}
```

Only ID is always required, but at least one mutable field must also be present.

Output: the complete updated transaction.

Covered edge cases:

- Omitted fields remain unchanged.
- Explicit `note: null` clears the note.
- Missing ID: `transaction_not_found`.
- Invalid amount, date, merchant, or category: nothing changes.
- A replacement date may be today or earlier; future dates return `invalid_input`.
- Merchant changed without category: existing transaction category remains.
- Category supplied: it affects only this transaction.
- Updating a transaction never creates, changes, activates, disables, or removes a merchant mapping.

Example: correcting `Metro grocery store` to `Metro` preserves the transaction category but leaves all mapping changes to `set_known_merchant`.

### 8. `remove_transaction`

Purpose: permanently remove one transaction, including a fully refunded purchase in Phase 1.

Input:

```json
{ "id": 42 }
```

Output: the removed transaction record as confirmation.

Covered edge cases:

- Missing or repeatedly removed ID: `transaction_not_found` rather than false success.
- Merchant mappings are never removed automatically.
- Budgets are never changed.
- Partial refunds and negative transactions remain out of scope.

Example: a full refund removes the original purchase; the merchant remains known for future purchases.

## Read operations

### 9. `list_categories`

Purpose: provide categories available to the LLM and user.

Input: `{}`.

Output: category records ordered alphabetically, case-insensitively, then by ID.

Covered edge cases:

- Inactive categories are hidden.
- Inactive categories remain available by name to historical transaction and summary reads.
- An empty database returns an empty array, not an error.
- No pagination is currently applied because category lists are expected to remain small.

### 10. `list_known_merchants`

Purpose: expose merchant defaults for exact lookup and LLM-assisted similarity matching.

Input:

```json
{
  "query": "metro",
  "limit": 50,
  "offset": 0
}
```

All fields are optional. Query is a trimmed, case-insensitive substring filter.

Output:

- Mapping records that state whether their current category is active.
- Pagination metadata: limit, offset, returned, total, and `has_more`.

Ordering: merchant ascending case-insensitively, then ID ascending.

Covered edge cases:

- Default page size is 50; maximum is 200.
- Invalid limits or offsets return `invalid_input`.
- Empty matches return an empty array and zero counts.
- A mapping to an inactive category remains visible but cannot categorize an ambiguous new transaction.
- Semantic similarity is performed by the LLM, not this query.

### 11. `list_transactions`

Purpose: return individual purchases, optionally filtered by inclusive dates and category.

Input:

```json
{
  "start_date": "2026-08-01",
  "end_date": "2026-08-31",
  "category": "Groceries",
  "limit": 50,
  "offset": 0
}
```

All fields are optional.

Output:

- Complete transaction records: ID, amount, merchant, date, category, note, and timestamps.
- Pagination metadata: limit, offset, returned, total, and `has_more`.

Ordering: transaction date descending, then ID descending. Newest purchases appear first.

Covered edge cases:

- Default page size is 50; maximum is 200.
- Either date bound may be used independently.
- Reversed or malformed ranges return `invalid_input`.
- Missing category returns `category_not_found`.
- Inactive categories may be used to retrieve historical transactions.
- Empty matches return an empty array, not an error.
- Relative phrases such as “four days ago” are resolved by the LLM before the tool call.

Example: “Show my August Groceries purchases” lists newest-first transactions for that category from August 1 through August 31.

### 12. `get_monthly_summary`

Purpose: compare the complete monthly budget with actual spending.

Input:

```json
{ "month": "2026-08" }
```

Output:

- Month.
- Total budget, total spending, and remaining amount.
- Per-category budget, spending, and remaining amount.

Ordering: category name ascending, case-insensitively.

Covered edge cases:

- No budget snapshot: `monthly_budget_not_found`, even if transactions exist.
- The error includes the latest earlier budget month so the LLM can offer carry-forward.
- The read never creates a budget.
- A category is included when its budget is greater than zero or it has spending.
- Zero-budget categories with no spending are hidden.
- Spending is never hidden merely because a category is zero-budget or inactive.
- Unbudgeted spending appears against `"0.00"`.
- Remaining is budget minus spending and may be negative.
- Totals are calculated from stored rows and transactions.

Example: an inactive category with an August purchase still appears in August spending, preventing the report from concealing real expenses.

### 13. `get_category_summary`

Purpose: compare one category's spending with its allocation for one month.

Input:

```json
{ "category": "Groceries", "month": "2026-08" }
```

Output:

- Category ID and name.
- Month.
- Budget, total spending, remaining amount, and transaction count.

Covered edge cases:

- Category does not exist: `category_not_found`.
- Inactive category is allowed for historical reporting.
- Month has no budget snapshot: `monthly_budget_not_found`.
- Month exists but has no row for the category: budget is `"0.00"`.
- Remaining may be negative.
- Transactions are not embedded in the summary.
- When the user asks for both summary and purchases, the LLM also calls `list_transactions` for the month's inclusive date range.

Example: “Give me my August Groceries summary with the transactions” uses this tool plus `list_transactions`.

## Cross-operation scenarios already covered

### Monthly rollover with changes

August does not exist. The LLM calls `create_monthly_budget` in carry-forward mode, copies July, and supplies every requested August override in the same atomic call. Later August changes use one `set_budgets` call. July remains unchanged, and September can inherit the final August snapshot.

### Missing transaction category

The LLM checks active categories. If the requested category does not exist, the write fails with the current categories and the LLM asks whether to create it. No transaction or mapping is partially written.

### One-off merchant category

Metro normally maps to Groceries. An explicit Metro/Health purchase is Health for that transaction only. The Metro default remains Groceries.

### Inactive merchant category

Shoppers maps to Health. Disabling Health leaves the mapping record intact but makes it unusable for an ambiguous new purchase. The LLM asks for an active category. If the user chooses Groceries, the transaction is recorded and the Shoppers mapping is replaced with Groceries. Re-enabling Health later does not change it back.

### Fully refunded purchase

The original transaction is removed. Its merchant mapping and category history remain. Partial refunds are deferred.

## Clarification decisions already resolved

1. Duplicate transactions after a lost response are an accepted Phase 1 risk. No idempotency key will be added.
2. Category names, merchant names, and notes have no product-level character limits. Amounts only need to fit their signed 64-bit stored representation.
3. Future-dated transactions are rejected in Phase 1 and may be added later.
4. Current dates and months use the operating system's local timezone.
5. `set_budgets` changes only the current month. Past monthly snapshots cannot be edited.
6. Monthly budgets cannot be created for future months. The current month is created only after it begins.
7. A carry-forward source with no active categories returns the distinct `budget_source_empty` error.
8. `list_categories` remains unpaginated.
9. Category renaming is deferred until after Phase 1.
10. Disabling a category affects only the current month. There is no backdating, future scheduling, or separate restoration workflow.
11. If intervening months were skipped, the current month copies the latest earlier snapshot. April may copy January without inventing February or March records.
12. Merchant mappings retain only their current category. There is no mapping restoration history or reactivation output.

## Questions still requiring clarification

None identified after the final simplification pass.

## Approval checkpoint

Phase 0 now contains thirteen operation contracts. With no remaining clarification questions, it is ready for final approval.
