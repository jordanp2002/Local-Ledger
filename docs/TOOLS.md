# Tool flow

## 1. Set up categories

| Tool | Use |
| --- | --- |
| `list_categories` | List active categories. |
| `create_category` | Create or re-enable a category. |
| `rename_category` | Rename a category. |
| `disable_category` | Disable a category. |

## 2. Set up a monthly budget

| Tool | Use |
| --- | --- |
| `create_monthly_budget` | Create a month from amounts or an earlier month. |
| `set_budgets` | Change amounts in an existing month. |

## 3. Set up merchant defaults

| Tool | Use |
| --- | --- |
| `list_known_merchants` | List or search merchant defaults. |
| `set_known_merchant` | Set a merchant's default category. |
| `rename_known_merchant` | Rename a merchant default. |
| `remove_known_merchant` | Remove a merchant default. |

## 4. Record purchases

| Tool | Use |
| --- | --- |
| `add_transaction` | Add one purchase. |
| `add_transactions` | Add up to 100 purchases together. |
| `list_transactions` | Find purchases by date, category, or merchant. |
| `update_transaction` | Correct a purchase. |
| `remove_transaction` | Delete a purchase. |

## 5. Review spending

| Tool | Use |
| --- | --- |
| `get_monthly_summary` | Compare one month's budget and spending. |
| `get_category_summary` | Review one category in one month. |
| `get_spending_summary` | Total spending for a date range. |
| `list_top_merchants` | Rank merchants by spending. |
| `compare_months` | Compare two budgeted months. |
| `get_monthly_series` | Review up to 24 months in order. |

## Recurring-expense flow

1. Create recurring templates with `create_recurring_transaction`.
2. Call `preview_due_transactions` to see what is due. Preview does not write anything.
3. Show the preview to the user and ask them to confirm it.
4. After confirmation, call `materialize_due_transactions` to record the due expenses.

Local Ledger does not run continuously and has no internal scheduler. Recurring
expenses are checked only when an MCP client calls the preview or materialization
tools. An external automation may initiate that check, but scheduling remains
outside Local Ledger.

Materialization is atomic and retry-safe: either the full due set is recorded or
none of it is, and retrying does not create duplicates. Generated rows are
ordinary expense transactions, so they appear in transaction lists and every
spending report.

## Basic flow

1. Create categories.
2. Create the monthly budget.
3. Add merchant defaults if useful.
4. Record purchases.
5. Review summaries and correct mistakes when needed.
