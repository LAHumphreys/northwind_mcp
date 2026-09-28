# CLAUDE.md

Go MCP server over the Northwind Postgres sample database. See README.md for
the full layout and workflow.

## Commands (run from repo root)

- `make db-up` starts a seeded Postgres on localhost:5432 via compose.
  In Claude Code on the web the SessionStart hook has already started and
  seeded a local Postgres; do not use Docker there.
- `make build` / `make run` build or run the server (`mcp-server/`, port 8080).
- `make test` runs `go test -race`. The smoke test in `mcp-server/server_test.go`
  needs the DB; it skips when unreachable unless `NORTHWIND_TEST_REQUIRE_DB=1`.
- `make lint` runs gofmt and go vet. CI (`.github/workflows/ci.yml`) runs the
  same plus the tests against a Postgres service container.

## Conventions

- Go module lives in `mcp-server/`; run `go` commands from there.
- All server wiring is in `newServer` in `mcp-server/main.go`. Add new tools,
  resources and prompts there; put SQL in `mcp-server/internal/db`.
- Config is env vars only (`NORTHWIND_DB_*`, `MCP_HTTP_ADDR`); defaults in
  `.env.example`. Never commit a `.env`.
- Do not commit build output; `bin/` and the server binary are gitignored.
- Seed SQL is vendored at `db/init/01-northwind.sql`; the DB image copies it.

## Running the containers inside Claude Code on the web

Docker works in the sandbox but needs three workarounds (verified once):

1. The daemon is not running: `nohup dockerd >/tmp/dockerd.log 2>&1 &`.
2. Stop the hook-started local Postgres first so compose can bind 5432:
   `pg_ctlcluster 16 main stop`.
3. Build through `.claude/compose.sandbox.yaml`, which runs the Go build stage
   on the host network with the sandbox CA. If Docker Hub answers 429, pull the
   base image from `mirror.gcr.io/library/<image>` and `docker tag` it to the
   `docker.io/library/<image>` name; buildkit then uses the local copy.

```sh
docker compose -f compose.yaml -f .claude/compose.sandbox.yaml --profile full up -d --build --wait
```
