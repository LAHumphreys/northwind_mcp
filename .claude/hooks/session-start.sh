#!/bin/bash
# SessionStart hook for Claude Code on the web.
#
# Downloads Go module dependencies and brings up a locally seeded Northwind
# Postgres inside the remote container so `go test`, `go run .` and curl
# smoke tests work without Docker. Idempotent; no-op outside remote sessions.
set -euo pipefail

if [ "${CLAUDE_CODE_REMOTE:-}" != "true" ]; then
  exit 0
fi

ROOT="${CLAUDE_PROJECT_DIR:-$(cd "$(dirname "$0")/../.." && pwd)}"
SEED="$ROOT/db/init/01-northwind.sql"
export PGPASSWORD=northwind

log() { echo "[session-start] $*"; }

log "downloading Go modules"
(cd "$ROOT/mcp-server" && go mod download)

setup_db() {
  if ! command -v pg_lsclusters >/dev/null 2>&1; then
    log "postgresql not installed; skipping DB setup"
    return 1
  fi

  local ver cluster status
  read -r ver cluster _ status _ < <(pg_lsclusters -h | head -n1)
  if [ -z "${ver:-}" ]; then
    log "no postgres cluster found; skipping DB setup"
    return 1
  fi

  if [ "$status" != "online" ]; then
    log "starting postgres $ver/$cluster"
    pg_ctlcluster "$ver" "$cluster" start
  fi

  local i
  for i in $(seq 1 30); do
    su postgres -c "pg_isready -q" && break
    sleep 1
  done

  su postgres -c "psql -v ON_ERROR_STOP=1 -q" <<'SQL'
DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'northwind') THEN
    CREATE ROLE northwind LOGIN PASSWORD 'northwind';
  END IF;
END
$$;
SQL

  if ! su postgres -c "psql -tAc \"SELECT 1 FROM pg_database WHERE datname = 'northwind'\"" | grep -q 1; then
    su postgres -c "createdb -O northwind northwind"
  fi

  # Seed only when the products table is missing or short (idempotent).
  local count
  count="$(psql -h localhost -U northwind -d northwind -tAc 'SELECT count(*) FROM products' 2>/dev/null || echo 0)"
  if [ "${count:-0}" -lt 77 ]; then
    log "seeding northwind from $SEED"
    psql -q -h localhost -U northwind -d northwind -v ON_ERROR_STOP=1 -f "$SEED"
  fi

  log "northwind db ready on localhost:5432"
}

if setup_db; then
  if [ -n "${CLAUDE_ENV_FILE:-}" ]; then
    cat >> "$CLAUDE_ENV_FILE" <<'ENV'
export NORTHWIND_DB_HOST=localhost
export NORTHWIND_DB_PORT=5432
export NORTHWIND_DB_USER=northwind
export NORTHWIND_DB_PASSWORD=northwind
export NORTHWIND_DB_NAME=northwind
export NORTHWIND_TEST_REQUIRE_DB=1
ENV
  fi
else
  log "WARNING: database not available; DB-backed tests will skip"
fi

log "done"
