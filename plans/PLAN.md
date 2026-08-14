# Local Finance MCP — Phase 0 and Phase 1

## Goal

Build a small local-first MCP server for monthly budgeting. Data lives in one local SQLite database.

## Version 1 scope

- Categories
- Monthly category budgets
- Transactions
- Known merchant-to-category mappings
- Monthly and category spending summaries

Not included: accounts, balances, net worth, bank syncing, imports, dashboards, cloud hosting, or multi-currency support.

## Phase 0 — Agree on the contract

Planning only. No implementation.

### Data model

Use four SQLite records:

- `categories` — an ID, a case-insensitively unique name, an active flag, and created/updated timestamps
- `budgets` — an ID, category ID, month, amount in integer hundredths, and created/updated timestamps; one row per category and month
- `transactions` — an ID, merchant, positive expense amount in integer hundredths, transaction date, category ID, optional note, and created/updated timestamps
- `known_merchants` — an ID, case-insensitively unique merchant name, category ID, and created/updated timestamps

Store amounts as integer hundredths in SQLite. MCP tools accept and return decimal strings such as `"20.00"`, avoiding floating-point arithmetic and avoiding any requirement for the LLM to convert the value into its stored representation. Amounts have at most two decimal places.

Phase 1 does not model currency. There is no currency setting, code, symbol, validation, conversion, or per-record currency field. The amount is interpreted entirely in the user's own context. Supporting other amount scales or explicit currencies can be designed in a future phase if needed.

Phase 1 models expenses only. Transaction amounts must be greater than zero; income and negative refund transactions are out of scope. A fully refunded purchase is handled by removing its transaction. Partial refunds can be added in a future phase.

Category and merchant equality uses trimmed, case-insensitive exact matching. Categories are never created implicitly while recording a transaction.

### First MCP tools

#### Writes

- `create_monthly_budget`
- `add_transaction`
- `update_transaction`
- `remove_transaction`
- `create_category`
- `disable_category`
- `set_budgets`
- `set_known_merchant`

#### Reads

- `list_categories`
- `list_known_merchants` — optionally accepts a search string so the LLM can inspect relevant mappings
- `list_transactions` — filter by date range and category
- `get_monthly_summary` — total budget, total spending, and category breakdown
- `get_category_summary` — monthly budget and spending for one category

### Category and merchant behavior

`add_transaction` validates that its submitted category exists. If it does not, the tool writes nothing and returns a structured `category_not_found` error containing the requested category and the existing categories. The LLM then asks the user whether to create it with `create_category`.

When a valid category is explicitly supplied to `add_transaction`, that category always applies to the new transaction. If the merchant has no exact mapping, the transaction and a new default mapping are created atomically. If a mapping already exists, an individual transaction never replaces it: the existing mapping is retained whether it matches the supplied category or the transaction is a one-off exception.

Only `set_known_merchant` intentionally creates or replaces a merchant's default category. Thus `Metro -> Groceries` remains the default after recording one Metro transaction as `Health`, while an explicit request such as `make Shoppers default to Health` replaces that mapping. The LLM distinguishes a category for one purchase from a request to change the merchant default and asks the user when that intent is genuinely ambiguous.

Disabling a category hides it from normal category selection and removes only its current-month budget row. It never deletes or changes transactions or merchant mappings. If a merchant still points to an inactive category, the mapping cannot categorize a new transaction without an explicit active category. When one is supplied, it replaces that unusable default atomically.

Calling `create_category` with the same name as an inactive category re-enables the existing category record. Merchant mappings have no history or restoration behavior: they retain only their current category. Thus, if Shoppers was remapped from inactive `Health` to `Groceries`, re-enabling Health later cannot move Shoppers back.

SQLite merchant lookup remains deterministic: trimmed, case-insensitive exact matching only. For a similar but non-exact name such as `Metro grocery store`, the LLM uses `list_known_merchants` to recognize a likely match and supplies the category. The new merchant spelling is then remembered as another exact mapping for future transactions. The server itself does not make fuzzy or semantic guesses.

### Initial budget setup

- `create_monthly_budget` — creates a new independent monthly budget either from explicit category amounts or by carrying forward the latest earlier monthly budget
- If categories are missing, the agent asks the user for them before calling the tool
- The monthly budget total is calculated from the category budgets, avoiding two totals that can disagree
- The operation fails without writing anything if the month already has any budgets. Later changes use `set_budgets`, preventing an accidental setup call from overwriting an existing month.
- Carry-forward copies rows into the new month; it does not make months reference a shared mutable template. Changing October therefore cannot change an August report.
- Carry-forward copies only active categories. Inactive categories are omitted from the new snapshot.
- Changes made with `set_budgets` apply only to the named month. A later month that carries forward uses the most recent earlier monthly budget, so the changed amounts naturally become the defaults going forward.
- Transaction tools never create or change monthly budgets. A transaction may exist in a month that does not yet have a budget snapshot.

### Confirmed safety behavior

- Invalid category references never create partial transactions, budgets, or merchant mappings.
- Full multi-record operations are atomic.
- No redundant monthly total is stored.
- Income, partial refunds, accounts, balances, and multiple currencies remain outside Phase 1.

### Transaction dates and listings

- A transaction date is stored as a calendar date in canonical `YYYY-MM-DD` form.
- If `add_transaction` receives no date, it defaults to the server's current local calendar date.
- The LLM resolves user phrases such as `yesterday` or `four days ago` relative to the user's current local date and submits the resolved date explicitly.
- `list_transactions` returns, at minimum, each transaction's ID, amount, merchant, transaction date, category, and optional note. The ID allows a returned transaction to be updated or removed unambiguously.

### Tool contract conventions

- Inputs and outputs use canonical JSON field names in `snake_case`.
- IDs are positive JSON integers. Transaction mutations target a transaction by ID; category-facing inputs use the category name because that is more natural for an LLM.
- Input amounts are decimal strings with zero, one, or two decimal places, such as `"20"`, `"20.5"`, or `"20.50"`. Outputs always normalize them to two places, such as `"20.00"`.
- Transaction amounts must be at least `"0.01"`. Budget amounts may be `"0.00"`.
- Months use `YYYY-MM`; dates use `YYYY-MM-DD`; timestamps use RFC 3339 UTC.
- User-provided text has no product-level length limit in Phase 1. SQLite's storage limits remain the only text ceiling. Amounts must fit the signed 64-bit integer representation after conversion to hundredths.
- Required strings are trimmed. A string that is empty after trimming is invalid.
- Date ranges include both `start_date` and `end_date`.
- Optional omitted fields are represented by absence in inputs. Nullable output fields, such as `note`, are returned explicitly as `null` when unset.
- Successful tools return structured JSON with `ok: true` and the fields documented below. Errors return `ok: false` under the error contract still to be finalized.
- Defaults and current-date validation use the operating system's local timezone. UTC is used only for stored RFC 3339 timestamps.
- Phase 1 accepts transaction dates from the current local day or earlier. Future-dated transactions are rejected.
- Phase 1 accepts monthly budget creation and changes only for the current local month. Past monthly snapshots are immutable, and future monthly budgets cannot be created in advance.
- Phase 1 accepts the risk of a duplicate transaction if a successful `add_transaction` response is lost and the caller retries. There is no idempotency key in the MVP; duplicates are corrected with `remove_transaction`.

Canonical returned records are flat:

```json
{
  "category": {
    "id": 1,
    "name": "Groceries",
    "active": true,
    "created_at": "2026-08-14T14:30:00Z",
    "updated_at": "2026-08-14T14:30:00Z"
  },
  "budget": {
    "id": 1,
    "month": "2026-08",
    "category_id": 1,
    "category": "Groceries",
    "amount": "500.00",
    "created_at": "2026-08-14T14:30:00Z",
    "updated_at": "2026-08-14T14:30:00Z"
  },
  "transaction": {
    "id": 1,
    "amount": "20.00",
    "merchant": "Metro",
    "date": "2026-08-14",
    "category_id": 1,
    "category": "Groceries",
    "note": null,
    "created_at": "2026-08-14T14:30:00Z",
    "updated_at": "2026-08-14T14:30:00Z"
  },
  "known_merchant": {
    "id": 1,
    "merchant": "Metro",
    "category_id": 1,
    "category": "Groceries",
    "category_active": true,
    "created_at": "2026-08-14T14:30:00Z",
    "updated_at": "2026-08-14T14:30:00Z"
  }
}
```

The wrapper above illustrates the four record shapes; no tool returns all four together unless its individual contract says so.

### Exact write-tool inputs and outputs

#### `create_category`

Input:

```json
{ "name": "Groceries" }
```

Output:

```json
{
  "ok": true,
  "category": <category>,
  "created": true,
  "reactivated": false
}
```

If the name matches an inactive category case-insensitively, the existing record is made active and returned with `created: false` and `reactivated: true`. Current merchant mappings remain exactly as they are. If the name already belongs to an active category, the tool returns `category_already_exists`. It never creates a differently cased duplicate.

#### `disable_category`

Input:

```json
{ "name": "Dining" }
```

The operation makes the category inactive and removes its budget row only from the server's current local month when one exists. Earlier monthly budgets, all transactions, and all merchant mappings remain unchanged. The operation is atomic.

```json
{
  "ok": true,
  "category": <category>,
  "changed": true,
  "removed_budget": <budget-or-null>
}
```

Calling the tool for an already inactive category succeeds with `changed: false` and `removed_budget: null`. Re-enabling uses `create_category` with the same name; there is no separate restoration tool.

#### `create_monthly_budget`

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

The caller supplies exactly one creation mode: either `budgets`, or `carry_forward: true`. In explicit mode, `budgets` must contain at least one item. In carry-forward mode, the server copies the latest monthly budget earlier than the target month; `overrides` is optional and is applied atomically to the copied rows. If no earlier monthly budget exists, the operation fails without writing anything.

The requested `month` must equal the server's current local month. Past months cannot be created retroactively, and future months cannot be prepared in advance. Each month is created only after that calendar month begins.

Budget and override arrays cannot repeat a category case-insensitively. Every referenced category must already exist. Explicit mode contains exactly the supplied categories. Carry-forward mode retains every copied category, replaces amounts named in `overrides`, and adds an override category that was not present in the source month.

Inactive categories cannot be included explicitly or in overrides. Carry-forward omits them even when the source month contains older budget rows for them.

If carry-forward finds an earlier snapshot but every source row belongs to an inactive category, it returns `budget_source_empty` and writes nothing.

Output budgets are ordered by category name, case-insensitively:

```json
{
  "ok": true,
  "month": "2026-10",
  "creation_mode": "carry_forward",
  "source_month": "2026-09",
  "total_budget": "1500.00",
  "budgets": [<budget>]
}
```

`creation_mode` is `explicit` or `carry_forward`. `source_month` is `null` for explicit creation.

#### `set_budgets`

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

The target month must equal the server's current local month and must already have at least one budget row. Past snapshots cannot be edited and future months cannot be prepared. For a new current month, the caller uses `create_monthly_budget` first, optionally carrying forward the latest month and applying overrides in that same atomic call. This prevents a single category update from accidentally creating an incomplete monthly snapshot.

`budgets` must contain at least one item and cannot repeat a category case-insensitively. Every category must already exist and be active. The tool creates a category row within the existing monthly snapshot when absent and replaces its amount when present. All changes validate and commit atomically.

Output changes are ordered by category name, case-insensitively:

```json
{
  "ok": true,
  "month": "2026-08",
  "changes": [
    { "budget": <budget>, "created": false }
  ]
}
```

This operation never changes another month. Once changed, this month becomes the source inherited by a later carry-forward month when it is the latest earlier budget.

#### `set_known_merchant`

Input:

```json
{ "merchant": "Metro", "category": "Groceries" }
```

Creates the trimmed, case-insensitive merchant mapping if absent and intentionally replaces its category if present. Output includes the former category when a mapping was replaced:

```json
{
  "ok": true,
  "known_merchant": <known_merchant>,
  "created": false,
  "previous_category": "Groceries"
}
```

`previous_category` is `null` when the mapping was newly created or its category did not change. The submitted category must be active.

#### `add_transaction`

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

`category`, `date`, and `note` are optional. If `date` is omitted, the server uses its current operating-system-local date. A supplied date may be today or earlier; future dates return `invalid_input`. If `category` is omitted, an exact known-merchant mapping to an active category must exist. A supplied active category applies to the transaction. It creates a default mapping when no exact mapping exists. It preserves a different active default as a one-off exception, but replaces a mapping that points to an inactive category because that default is no longer usable. Output:

```json
{
  "ok": true,
  "transaction": <transaction>,
  "category_source": "provided",
  "merchant_mapping_action": "created"
}
```

`category_source` is either `provided` or `known_merchant`.
`merchant_mapping_action` is:

- `created` when this transaction established the merchant's first mapping
- `matched` when the effective transaction category matched the existing mapping
- `preserved` when a different explicitly supplied category applied only to this transaction and the existing mapping was left unchanged
- `replaced_inactive` when an explicitly supplied active category replaced a mapping that pointed to an inactive category

An explicitly supplied inactive category returns `category_inactive`. If `category` is omitted and the exact mapping points to an inactive category, the tool returns `merchant_category_inactive` and the LLM asks the user for an active category. Supplying one records the transaction and makes it the merchant's new default atomically.

#### `update_transaction`

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

Only `id` is always required. At least one mutable field must also be present. Omitted fields remain unchanged; an explicit `note: null` clears the note. Output:

```json
{
  "ok": true,
  "transaction": <transaction>
}
```

An update never creates, replaces, or removes a merchant mapping. If `merchant` changes without `category`, the transaction retains its current category. If `category` is supplied, it applies only to this transaction. A deliberate default change uses `set_known_merchant` separately.

A supplied replacement date may be today or earlier in the operating system's local timezone. Future dates return `invalid_input` and leave the transaction unchanged.

#### `remove_transaction`

Input:

```json
{ "id": 42 }
```

The transaction is permanently removed. Merchant mappings are not removed automatically because other transactions and future entries may still use them. The deleted record is returned as confirmation:

```json
{ "ok": true, "removed_transaction": <transaction> }
```

If the ID does not exist, the tool returns `transaction_not_found`. A repeated deletion is therefore visible rather than reported as a false success.

### Exact read-tool inputs and outputs

#### `list_categories`

Input: `{}`.

Only active categories are returned. Inactive categories remain available to historical transaction and summary queries by name, but they are not shown in the normal category list.

Output categories are ordered by name ascending, case-insensitively, then by ID ascending:

```json
{ "ok": true, "categories": [<category>] }
```

#### `list_known_merchants`

Input:

```json
{ "query": "metro", "limit": 50, "offset": 0 }
```

All fields are optional. `query` is a trimmed, case-insensitive substring filter. Returned records expose whether their category is active so the LLM does not treat an unusable mapping as a valid default. `limit` defaults to 50 and has a maximum of 200; `offset` defaults to 0. Results are ordered by merchant ascending, case-insensitively, then by ID ascending:

```json
{
  "ok": true,
  "known_merchants": [<known_merchant>],
  "page": { "limit": 50, "offset": 0, "returned": 1, "total": 1, "has_more": false }
}
```

#### `list_transactions`

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

All fields are optional. `limit` defaults to 50 and has a maximum of 200; `offset` defaults to 0. Results are ordered by transaction date descending, then ID descending, so the newest transactions appear first. This ordering and pagination are part of the approved Phase 1 contract:

```json
{
  "ok": true,
  "transactions": [<transaction>],
  "page": { "limit": 50, "offset": 0, "returned": 12, "total": 12, "has_more": false }
}
```

#### `get_monthly_summary`

Input:

```json
{ "month": "2026-08" }
```

If the requested month has no budget rows, the tool returns `monthly_budget_not_found` even if transactions exist in that month. The error identifies the latest earlier budget month when one exists, allowing the LLM to offer to carry it forward with `create_monthly_budget`. This read tool never creates a budget as a side effect.

Output category rows are ordered by category name ascending, case-insensitively. A category is included when its budget is greater than zero or it has spending in the month. A zero-budget category with no spending is omitted, while real spending is never hidden merely because its budget is zero or its category is now inactive:

```json
{
  "ok": true,
  "month": "2026-08",
  "total_budget": "650.00",
  "total_spending": "120.00",
  "remaining": "530.00",
  "categories": [
    {
      "category_id": 1,
      "category": "Groceries",
      "budget": "500.00",
      "spending": "90.00",
      "remaining": "410.00"
    }
  ]
}
```

An absent budget is reported as `"0.00"`. `remaining` is budget minus spending and may be negative.

#### `get_category_summary`

Input:

```json
{
  "category": "Groceries",
  "month": "2026-08"
}
```

The category and month are both required. If the month has no budget snapshot, the tool returns `monthly_budget_not_found`, using the same behavior as `get_monthly_summary`. If the monthly snapshot exists but has no row for this category, its budget is reported as `"0.00"`.

Historical summaries may target an inactive category. Disabling affects normal selection and current/future allocations, not access to existing history.

Output:

```json
{
  "ok": true,
  "category_id": 1,
  "category": "Groceries",
  "month": "2026-08",
  "budget": "500.00",
  "total_spending": "90.00",
  "remaining": "410.00",
  "transaction_count": 4
}
```

`remaining` is budget minus spending and may be negative. The summary does not embed individual transactions. When the user asks for both a category summary and its purchases, the LLM also calls `list_transactions` with that category and the month's inclusive start and end dates.

### Final approval before Phase 1

The final review is in `plans/PHASE_0_REVIEW.md`. No unresolved edge-case questions remain after the simplified inactive-category design. Phase 0 exits when this contract and the structured errors below receive final approval.

### Structured error contract

Tool failures set the MCP result's error status and return structured JSON in this common shape:

```json
{
  "ok": false,
  "error": {
    "code": "transaction_not_found",
    "message": "Transaction 42 was not found.",
    "retryable": false,
    "details": { "id": 42 }
  }
}
```

- `code` is a stable machine-readable value used by the LLM and tests.
- `message` is a concise human-readable explanation and must not expose SQL or internal implementation details.
- `retryable` says whether repeating the identical call could reasonably succeed without changing its input or surrounding state.
- `details` is always an object and contains only safe, relevant context needed to recover.
- Validation completes before any write begins. Any failure in a multi-record write rolls back the whole operation.

Phase 1 error codes:

#### `invalid_input`

Used for malformed dates or months, future transaction dates, non-current months supplied to budget write tools, invalid decimal strings, amounts that cannot fit the signed 64-bit hundredths representation, empty required strings, unsupported fields, invalid pagination, reversed date ranges, missing required fields, mutually exclusive creation modes, duplicate categories in one budget request, or update calls with no mutable fields.

```json
{
  "code": "invalid_input",
  "message": "One or more input fields are invalid.",
  "retryable": false,
  "details": {
    "fields": [
      { "field": "amount", "reason": "must be greater than zero" }
    ]
  }
}
```

All detectable invalid fields are returned together in input order so the LLM can correct them in one retry.

#### `category_not_found`

Used whenever a submitted category name does not exist, including transaction, budget, merchant, filter, and category-summary calls.

```json
{
  "code": "category_not_found",
  "message": "Category 'Pharmacy' does not exist.",
  "retryable": false,
  "details": {
    "requested_category": "Pharmacy",
    "categories": [<category>]
  }
}
```

The LLM can use the returned categories to choose an existing one or ask whether to create the requested category.

#### `category_already_exists`

Used only by `create_category` when the normalized name already belongs to an active category. An inactive match is re-enabled successfully instead:

```json
{
  "code": "category_already_exists",
  "message": "Category 'Groceries' already exists.",
  "retryable": false,
  "details": { "category": <category> }
}
```

#### `category_inactive`

Used when a write other than `create_category` attempts to assign an inactive category to a new transaction, budget, or merchant mapping:

```json
{
  "code": "category_inactive",
  "message": "Category 'Dining' is inactive.",
  "retryable": false,
  "details": {
    "category": <category>,
    "active_categories": [<category>]
  }
}
```

The LLM asks whether to re-enable the category with `create_category` or use an active category. Historical read filters are still allowed to reference inactive categories.

#### `merchant_category_required`

Used by `add_transaction` when `category` is omitted and no exact known-merchant mapping exists:

```json
{
  "code": "merchant_category_required",
  "message": "Merchant 'Metro grocery store' has no exact category mapping.",
  "retryable": false,
  "details": { "merchant": "Metro grocery store" }
}
```

The LLM then uses `list_known_merchants` for a possible semantic match or asks the user for a category.

#### `merchant_category_inactive`

Used by `add_transaction` when `category` is omitted and the exact merchant mapping points to an inactive category:

```json
{
  "code": "merchant_category_inactive",
  "message": "Merchant 'Shoppers Drug Mart' maps to inactive category 'Health'.",
  "retryable": false,
  "details": {
    "known_merchant": <known_merchant>,
    "active_categories": [<category>]
  }
}
```

The LLM asks the user for an active category. Supplying one records the transaction and replaces the unusable merchant mapping atomically. Calling `create_category` with the inactive category's name re-enables it without reconstructing any prior mapping state.

#### `transaction_not_found`

Used by `update_transaction` and `remove_transaction` when the ID is absent:

```json
{
  "code": "transaction_not_found",
  "message": "Transaction 42 was not found.",
  "retryable": false,
  "details": { "id": 42 }
}
```

#### `monthly_budget_already_exists`

Used by `create_monthly_budget` when the target month has one or more budget rows:

```json
{
  "code": "monthly_budget_already_exists",
  "message": "A monthly budget already exists for 2026-08.",
  "retryable": false,
  "details": { "month": "2026-08" }
}
```

The LLM uses `set_budgets` for deliberate changes to that month.

#### `monthly_budget_not_found`

Used by `get_monthly_summary`, `get_category_summary`, or `set_budgets` when the requested month has no budget rows:

```json
{
  "code": "monthly_budget_not_found",
  "message": "No monthly budget exists for 2026-10.",
  "retryable": false,
  "details": {
    "month": "2026-10",
    "latest_earlier_month": "2026-09"
  }
}
```

`latest_earlier_month` is `null` when there is none.

#### `budget_source_not_found`

Used by carry-forward `create_monthly_budget` when no earlier monthly budget exists:

```json
{
  "code": "budget_source_not_found",
  "message": "There is no earlier monthly budget to carry forward into 2026-08.",
  "retryable": false,
  "details": { "month": "2026-08" }
}
```

#### `budget_source_empty`

Used by carry-forward `create_monthly_budget` when an earlier snapshot exists but none of its category rows remain eligible because every source category is inactive:

```json
{
  "code": "budget_source_empty",
  "message": "The earlier monthly budget has no active categories to carry forward.",
  "retryable": false,
  "details": {
    "month": "2026-08",
    "source_month": "2026-07"
  }
}
```

The LLM asks the user to re-enable or create at least one active category and then create the current month's budget explicitly.

#### `internal_error`

Used for unexpected database or server failures. The operation is rolled back, internal details are logged locally, and the tool returns no SQL text, paths, or stack traces:

```json
{
  "code": "internal_error",
  "message": "The operation could not be completed.",
  "retryable": true,
  "details": {}
}
```

**Phase 0 exit:** we approve the records, tool names, inputs, outputs, and error behavior.

## Phase 1 — Build the budgeting MVP

Keep Phase 1 split into small pull requests:

1. **Project foundation:** Go module, basic stdio MCP server, SQLite connection, and the four tables.
2. **Budget setup:** category, budget, and known-merchant write tools.
3. **Transactions:** add, update, and remove transaction tools with known-merchant lookup.
4. **Reporting:** transaction listing, monthly summary, and category summary tools.

Each pull request includes only the tests and documentation needed for that slice.

**Phase 1 exit:** a user can create a monthly budget, record and correct transactions, reuse known merchant categories, and ask how the month or one category is doing.
