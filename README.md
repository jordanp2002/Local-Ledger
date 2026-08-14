# Local Finance MCP

A local-first budgeting MCP server. The current foundation runs over stdio and does not expose finance tools yet.

## Configuration

Set `LOCAL_FINANCE_DB_PATH` to an absolute path before starting the server:

```sh
export LOCAL_FINANCE_DB_PATH=/Users/you/.local/share/local-finance-mcp/finance.db
go run ./cmd/local-finance-mcp
```

The database file is your finance ledger. Back it up using your normal file-backup process; the server does not provide a backup subsystem.

## Build

```sh
go build ./cmd/local-finance-mcp
```

## Test

```sh
go test ./...
```
