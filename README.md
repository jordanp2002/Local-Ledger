
# Local Ledger

A local-first MCP server for monthly budgeting. Data is stored in one SQLite database on your computer.

## Inspiration

After working to create a similar system/solution across my local file storage system or a "AI Second Brain" some would call it, I realized this could be useful to set up as an mcp server as my implementation at the time was basically:

Skills with defined queries -> Write to local SQLite DB -> Read from local SQLite DB

While this works and is still effective for my needs, I thought an MCP would be a great way to build sharable way of doing this so others could potential have the same capabilities. Not to say my solution couldn't be done in one prompt by any frontier model but this solution creates an easy way to just setup the functionality without wasting your usage. It is also kind of just for my learning too and familiarizing myself with MCP and supplementing my Go skill development.

## Available tools

[See all available tools and what they are used for.](docs/TOOLS.md)

Local Ledger also supports a confirmation-first [recurring-expense workflow](docs/TOOLS.md#recurring-expense-flow).

## Quick start

Requires Go 1.26 or newer. From the Local Ledger project directory, run:

```sh
go run ./cmd/local-ledger
```

This starts the Local Ledger MCP server over stdio.

The database is created at:

```text
~/LocalLedger/finance.db
```

To use a different location:

```sh
LOCAL_FINANCE_DB_PATH=/absolute/path/finance.db go run ./cmd/local-ledger
```

## Documentation

- [Install, update, and backup](docs/INSTALL.md)

## Build

```sh
go build ./cmd/local-ledger
```

## Test

```sh
go test ./...
```
