<p align="center">
  <img src="assets/local-ledger-logo.png" alt="Local Ledger logo" width="240">
</p>

# Local Finance MCP

A local-first budgeting MCP server.

Inspiration:

After working to create a similar system/solution across my local file storage system or a "AI Second Brain" some would call it, I realized this could be useful to set up as an mcp server as my implementation at the time was basically:

Skills with defined queries -> Write to local SQLite DB -> Read from local SQLite DB

While this works and is still effective for my needs, I thought an MCP would be a great way to build sharable way of doing this so others could potential have the same capabilities. Not to say my solution couldn't be done in one prompt by any frontier model but this solution creates an easy way to just setup the functionality without wasting your usage. It is also kind of just for my learning too and familiarizing myself with MCP and supplementing my Go skill development. 

## Configuration

Set `LOCAL_FINANCE_DB_PATH` to an absolute path before starting the server:

```sh
export LOCAL_FINANCE_DB_PATH=/Users/you/.local/share/local-finance-mcp/finance.db
go run ./cmd/local-finance-mcp
```

The database file is your finance ledger. Back it up using your normal file-backup process; the server does not provide a backup subsystem.

## Tools

The server exposes thirteen finance tools:

- `add_transaction`
- `create_category`
- `list_categories`
- `disable_category`
- `create_monthly_budget`
- `set_budgets`
- `set_known_merchant`
- `list_known_merchants`
- `list_transactions`
- `update_transaction`
- `remove_transaction`
- `get_monthly_summary`
- `get_category_summary`

## Build

```sh
go build ./cmd/local-finance-mcp
```

## Test

```sh
go test ./...
```
