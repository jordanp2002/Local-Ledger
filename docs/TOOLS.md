# Tool flow

## 1. Set up categories

| Tool | Use |
| --- | --- |
| `list_categories` | List active categories. |
| `create_category` | Create or re-enable a category. |
| `rename_category` | Rename a category. |
| `disable_category` | Disable a category. |

## 2. Set up asset accounts

Asset accounts are local records only. These tools never contact a bank.

| Tool | Use |
| --- | --- |
| `create_account` | Create an asset account or reactivate a matching inactive account. |
| `update_account` | Rename an account or change its note. |
| `list_accounts` | List and filter accounts with their derived balances. |
| `disable_account` | Retire an account whose derived balance is zero without deleting its history. |
| `record_account_activity` | Record a local deposit or withdrawal without affecting budgets. |
| `reconcile_account_balance` | Reconcile an account to a reported balance with an auditable delta. |
| `list_account_activity` | List activity with running balances in stable order. |
| `reverse_account_activity` | Reverse one activity entry with an offsetting entry. |
| `transfer_between_accounts` | Record both sides of a completed local transfer atomically. |
| `list_account_transfers` | List completed local transfers and both account identities. |
| `reverse_account_transfer` | Record an inverse local transfer without deleting the original. |

Activity write responses return the resulting balance at the original write,
including on an exact retry. `list_account_activity` calculates running balances
in date, creation timestamp, and ID order, so later backdated activity can change
the historical running balances without changing an earlier write's retry result.
Transfers are local records only: these tools never contact a bank or execute an
external transfer. A transfer changes only the two account balances; it does not
create income, spending, budget, or expense-transaction records.

## 3. Set up a monthly budget

| Tool | Use |
| --- | --- |
| `create_monthly_budget` | Create a month from amounts or an earlier month. |
| `set_budgets` | Change amounts in an existing month. |

## 4. Set up merchant defaults

| Tool | Use |
| --- | --- |
| `list_known_merchants` | List or search merchant defaults. |
| `set_known_merchant` | Set a merchant's default category. |
| `rename_known_merchant` | Rename a merchant default. |
| `remove_known_merchant` | Remove a merchant default. |

## 5. Record purchases

| Tool | Use |
| --- | --- |
| `add_transaction` | Add one purchase. |
| `add_transactions` | Add up to 100 purchases together. |
| `list_transactions` | Find purchases by date, category, or merchant. |
| `update_transaction` | Correct a purchase. |
| `remove_transaction` | Delete a purchase. |

## 6. Review spending

| Tool | Use |
| --- | --- |
| `get_monthly_summary` | Compare one month's budget and spending. |
| `get_category_summary` | Review one category in one month. |
| `get_spending_summary` | Total spending for a date range. |
| `list_top_merchants` | Rank merchants by spending. |
| `compare_months` | Compare two budgeted months. |
| `get_monthly_series` | Review up to 24 months in order, optionally with one consistently ordered category row per month. |

## 7. Explicit budget rollovers

| Tool | Use |
| --- | --- |
| `create_budget_rollover` | Explicitly charge eligible overspending from a category to the immediately following month. |
| `list_budget_rollovers` | Audit rollover sources, amounts, status, and optional transaction links. |
| `remove_budget_rollover` | Remove one rollover after checking that later dependent rollovers are handled first. |

## 8. Sinking funds

| Tool | Use |
| --- | --- |
| `enable_sinking_fund` | Start carrying a category's unused balance from the current month. |
| `disable_sinking_fund` | End the current fund period and explicitly release its balance. |
| `list_sinking_funds` | Review current fund balances, optionally including completed periods. |

Transaction writes never create rollovers automatically. When `add_transaction`,
`add_split_transaction`, `add_transactions`, `update_transaction`, or
`materialize_due_transactions` returns a non-empty `rollover_offers` array,
show the offer to the user and ask whether to call `create_budget_rollover`.
The create tool rechecks eligibility atomically, and the target month is always
the immediate next calendar month. Rollover adjustments are shown separately
from base budgets in monthly, category, comparison, and series reports.

## Recurring-expense flow

1. Create recurring templates with `create_recurring_transaction`.
2. Call `preview_due_transactions` to see what is due. Preview does not write anything.
3. Show the preview to the user and ask them to confirm it.
4. After confirmation, call `materialize_due_transactions` to record the due expenses.
5. If materialization returns a `rollover_offers` entry, show it and ask whether
   to create the explicit rollover.

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
