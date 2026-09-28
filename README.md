# Northwind MCP

A toy [Model Context Protocol](https://modelcontextprotocol.io) server, written in
Go, that exposes the classic Northwind sample database (customers, products,
orders) over Streamable HTTP. It is a sandbox for working out how an MCP over an
orders / trading-book dataset should look before pointing it at real data.

## Layout

| Path | What |
| --- | --- |
| `mcp-server/` | Go module: MCP server (`main.go`), DB access (`internal/db`), smoke test |
| `db/` | Postgres image seeded with Northwind (`db/init/01-northwind.sql`) |
| `compose.yaml` | Dev stack: `db` service, plus optional `mcp` service (`--profile full`) |
| `Makefile` | Build / run / test / lint / DB helpers |
| `.github/workflows/ci.yml` | gofmt, vet, build, tests against a Postgres service container |
| `.claude/` | SessionStart hook so Claude Code on the web gets a seeded DB |
| `.vscode/mcp.json` | Points VS Code at `http://localhost:8080/mcp` |

## Prerequisites

- Go (the toolchain pinned in `mcp-server/go.mod` is fetched automatically by `go`)
- Docker Desktop or Podman, for the database
- Optionally `make` (on Windows: Git Bash / WSL, or run the underlying commands by hand)

Everything is pure Go (`pgx` has no cgo), so `windows/arm64` and `linux/arm64`
build and run natively. Both container images are multi-arch.

## Quick start

```sh
make db-up          # builds db/ image and waits for a healthy, seeded Postgres on :5432
make run            # go run ./mcp-server  ->  http://localhost:8080/mcp
make test           # go test (DB-backed smoke test skips if Postgres is unreachable)
make lint           # gofmt + go vet
```

Podman users: `make COMPOSE="podman compose" db-up`.

Without `make`:

```sh
docker compose up -d --wait db
cd mcp-server && NORTHWIND_DB_PASSWORD=northwind go run .
```

Run the server in a container too:

```sh
make up             # docker compose --profile full up -d --build --wait
make down
```

Reset the database (drops the data volume so the seed scripts run again):

```sh
make db-reset
```

## Configuration

All settings are environment variables; see `.env.example` for defaults.

| Variable | Default |
| --- | --- |
| `NORTHWIND_DB_HOST` | `localhost` |
| `NORTHWIND_DB_PORT` | `5432` |
| `NORTHWIND_DB_USER` | `northwind` |
| `NORTHWIND_DB_PASSWORD` | `northwind` |
| `NORTHWIND_DB_NAME` | `northwind` |
| `MCP_HTTP_ADDR` | `:8080` |
| `NORTHWIND_TEST_REQUIRE_DB` | unset (set to make tests fail instead of skip without a DB) |

## Talking to the server

The transport is Streamable HTTP; responses come back as `text/event-stream`
frames. Example calls, including the tools and resources exposed, are in
[`mcp-server/README.md`](mcp-server/README.md).

## Claude Code on the web

`.claude/hooks/session-start.sh` runs at the start of a remote session. It
downloads Go modules, starts the container's local Postgres, seeds Northwind
from `db/init/01-northwind.sql` and exports the `NORTHWIND_DB_*` variables, so
`go test` and `go run .` work without Docker. It is a no-op outside remote
sessions.

## Tracing what an agent does

Set `NORTHWIND_TRACE_FILE=/path/to/trace.jsonl` and the server appends one JSON
line per event: every MCP request and response it handles (`mcp.request`,
`mcp.response`), and for each one the SQL it ran (`sql.query` with bound args),
the rows the query returned (`sql.rows`) and the Postgres command tag
(`sql.result`). SQL events carry the `request_id` of the MCP request that
caused them. See `mcp-server/internal/trace`.

`scripts/agent_trace.py` uses this to record a whole conversation. It starts a
traced server, drives a headless Claude Code agent (a real MCP client) through
a list of prompts as one resumed session, and renders `report.md` interleaving
the agent transcript, the MCP messages and the SQL for each turn. Runs live
under `traces/`.

```sh
make build
python3 scripts/agent_trace.py --model claude-sonnet-5            # built-in 3-prompt script
python3 scripts/agent_trace.py --prompts-file my_prompts.json     # JSON array of prompts
```

Needs the `claude` CLI on PATH with credentials, and a reachable database.
