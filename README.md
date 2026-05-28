# mysql-copy

A CLI tool to interactively copy rows from one MySQL table to another.

## Prerequisites

- Go 1.18+
- Access to source and destination MySQL databases

## Run

Without building (requires Go installed):

```bash
go run main.go
```

Or build a standalone binary first:

```bash
go build -o mysql-copy .
./mysql-copy
```

## Usage

The tool walks you through the copy in steps:

1. **Source database** — pick a saved connection or enter new details (host, port, user, password, database)
2. **Connection test** — verifies the source connection before proceeding
3. **Save prompt** — if the connection is new, optionally save it with a name for reuse
4. **Pick source table** — arrow-key selection from available tables
5. **Destination database** — same flow as above
6. **Pick destination table** — arrow-key selection from available tables
7. **Confirm** — shows `sourcedb.table → destdb.table` before any data is written
8. **Copy** — streams rows in batches of 500 with a live progress count

### Saved connections

Connections are stored in `~/.mysql-copy/connections.json` with `0600` permissions (readable only by you). This file is outside the project directory and will never be committed to git.

To remove a saved connection, edit or delete `~/.mysql-copy/connections.json` directly.

> The destination table must already exist with compatible columns. Rows are appended via `INSERT` — existing rows are not modified or deleted.

## Notes

- Passwords are masked during input
- Table and column names are quoted, so reserved words and special characters are handled correctly
- For large tables, rows are batched (500 at a time) to avoid hitting MySQL's `max_allowed_packet` limit
