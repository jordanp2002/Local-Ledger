# Install, update, and backup

## Install

Requires Git and Go 1.26 or newer.

```sh
git clone https://github.com/jordanp2002/local-finance-mcp.git
cd local-finance-mcp
mkdir -p "$HOME/LocalLedger/bin"
go build -o "$HOME/LocalLedger/bin/local-finance-mcp" ./cmd/local-ledger
```

Run it once:

```sh
~/LocalLedger/bin/local-finance-mcp
```

The database is created automatically at `~/LocalLedger/finance.db`.

## MCP client

Use this command in your MCP client configuration:

```json
{
  "command": "/Users/you/LocalLedger/bin/local-finance-mcp"
}
```

Replace `/Users/you` with your home directory.

## Custom database location

Set an absolute path in the MCP client environment:

```json
{
  "command": "/Users/you/LocalLedger/bin/local-finance-mcp",
  "env": {
    "LOCAL_FINANCE_DB_PATH": "/absolute/path/finance.db"
  }
}
```

## Update

From the cloned repository:

```sh
git pull --ff-only
go build -o "$HOME/LocalLedger/bin/local-finance-mcp" ./cmd/local-ledger
```

Restart the MCP client after rebuilding. Database updates run automatically.

## Backup

Stop the MCP client, then copy the database:

```sh
cp "$HOME/LocalLedger/finance.db" "$HOME/Documents/local-ledger-backup.db"
```

To restore, stop the MCP client and copy the backup over `~/LocalLedger/finance.db`.
