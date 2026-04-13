#!/bin/sh
# Restore the Hush database from a backup created by backup.sh or update.sh.
#
# Usage:
#   ./scripts/restore.sh <backup-file.sql>
#
# Exit codes:
#   0  Success (restore applied; hush-api left stopped for operator verification)
#   1  Pre-flight failure or restore error
#
# ---- PRECONDITIONS (operator must verify before running) ------------------
#
#   1. .env is present AND matches the backup.
#      The following .env values must be identical to those in effect when the
#      backup was taken. If they differ, the restored database will be
#      inoperable:
#        - POSTGRES_PASSWORD          (postgres will reject all connections)
#        - TRANSPARENCY_LOG_PRIVATE_KEY  (log proof verification breaks permanently)
#        - SERVICE_IDENTITY_MASTER_KEY   (service identity private key unreadable)
#
#   2. You have verified which backup file to restore.
#      The wrong backup file applied to a live database cannot be undone
#      without another backup. If in doubt, take a fresh backup first:
#        ./scripts/backup.sh
#
#   3. Postgres container is running (hush-api does not need to be running;
#      this script stops it before restoring).
#
# ---- WHAT THIS SCRIPT DOES ------------------------------------------------
#
#   1. Verifies preconditions.
#   2. Stops hush-api to prevent writes during restore.
#   3. Drops and recreates the hush database (safe for both plain and
#      --clean dumps).
#   4. Restores the backup.
#   5. Leaves hush-api stopped. Operator restarts after verifying the data.
#
# ---- REDIS ----------------------------------------------------------------
#
#   Redis does not need to be restored. Redis holds session cache and
#   rate-limiting counters — both reset safely. Active sessions will
#   re-authenticate on next request.
#
# ---- MIGRATIONS -----------------------------------------------------------
#
#   When hush-api restarts, golang-migrate applies any migrations not recorded
#   in the restored schema_migrations table. If you are rolling back to an
#   older codebase, ensure the restored database schema matches what that
#   codebase expects. See docs/RUNBOOK.md for the full rollback procedure.
#
# ---------------------------------------------------------------------------

set -eu

COMPOSE_BASE_FILE="docker-compose.prod.yml"
COMPOSE_PROXY_FILE="docker-compose.caddy.yml"
LOG_PREFIX="[hush-restore]"

log() { printf '%s %s\n' "$LOG_PREFIX" "$1"; }
err() { printf '%s ERROR: %s\n' "$LOG_PREFIX" "$1" >&2; }
die() { err "$1"; exit "${2:-1}"; }
compose_cmd() { $DOCKER_COMPOSE -f "$COMPOSE_BASE_FILE" -f "$COMPOSE_PROXY_FILE" "$@"; }

# ---------------------------------------------------------------------------
# Arguments
# ---------------------------------------------------------------------------
if [ $# -lt 1 ]; then
  err "Usage: $0 <backup-file.sql>"
  err ""
  err "Example:"
  err "  $0 backups/hush-20260101-120000.sql"
  exit 1
fi

BACKUP_FILE="$1"

# ---------------------------------------------------------------------------
# Resolve project root
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_ROOT"

# ---------------------------------------------------------------------------
# Detect docker compose
# ---------------------------------------------------------------------------
DOCKER_COMPOSE=""
if docker compose version >/dev/null 2>&1; then
  DOCKER_COMPOSE="docker compose"
elif command -v docker-compose >/dev/null 2>&1; then
  DOCKER_COMPOSE="docker-compose"
else
  die "Docker Compose not found. Install from https://docs.docker.com/compose/install/" 1
fi

# ---------------------------------------------------------------------------
# Pre-flight
# ---------------------------------------------------------------------------
if [ ! -f .env ]; then
  die ".env not found.
You MUST have the .env that matches this backup before restoring.
Without the original POSTGRES_PASSWORD, TRANSPARENCY_LOG_PRIVATE_KEY, and
SERVICE_IDENTITY_MASTER_KEY, the restored database is inoperable." 1
fi

if [ ! -f "$BACKUP_FILE" ]; then
  die "Backup file not found: $BACKUP_FILE" 1
fi

PG_USER="$(grep -m1 '^POSTGRES_USER=' .env | cut -d= -f2 || echo "hush")"
PG_DB="$(grep -m1 '^POSTGRES_DB=' .env | cut -d= -f2 || echo "hush")"

if ! compose_cmd ps postgres 2>/dev/null | grep -qE "Up|running"; then
  die "Postgres container is not running.
Start it first:
  $DOCKER_COMPOSE -f $COMPOSE_BASE_FILE -f $COMPOSE_PROXY_FILE up -d postgres" 1
fi

# ---------------------------------------------------------------------------
# Confirmation
# ---------------------------------------------------------------------------
log "============================================================"
log " RESTORE OPERATION"
log "  Backup: $BACKUP_FILE"
log "  Target: database '$PG_DB' (user '$PG_USER')"
log "============================================================"
log ""
log "This will:"
log "  1. Stop hush-api (prevents concurrent writes)"
log "  2. Drop and recreate the '$PG_DB' database"
log "  3. Restore all data from the backup file"
log ""
log "Preconditions YOU must have verified:"
log "  [ ] .env matches the backup (POSTGRES_PASSWORD, TRANSPARENCY_LOG_PRIVATE_KEY,"
log "      SERVICE_IDENTITY_MASTER_KEY are identical to when the backup was taken)"
log "  [ ] This is the correct backup file"
log "  [ ] You have a current backup if you need to undo this restore"
log ""
printf '%s Type YES to proceed: ' "$LOG_PREFIX"
read -r _confirm
case "$_confirm" in
  YES) ;;
  *) log "Restore aborted."; exit 0 ;;
esac

# ---------------------------------------------------------------------------
# Step 1: Stop hush-api
# ---------------------------------------------------------------------------
log "Stopping hush-api..."
compose_cmd stop hush-api 2>/dev/null || true

# ---------------------------------------------------------------------------
# Step 2: Drop and recreate database
# ---------------------------------------------------------------------------
log "Dropping and recreating '$PG_DB' database..."

# Terminate existing connections to the target database before dropping
compose_cmd exec -T postgres \
  psql -U "$PG_USER" postgres \
  -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '$PG_DB' AND pid <> pg_backend_pid();" \
  >/dev/null 2>&1 || true

compose_cmd exec -T postgres \
  psql -U "$PG_USER" postgres \
  -c "DROP DATABASE IF EXISTS $PG_DB;" \
  -c "CREATE DATABASE $PG_DB OWNER $PG_USER;" || {
  err "Failed to recreate database. hush-api remains stopped."
  err "Investigate then restart manually:"
  err "  $DOCKER_COMPOSE -f $COMPOSE_BASE_FILE -f $COMPOSE_PROXY_FILE up -d"
  exit 1
}

# ---------------------------------------------------------------------------
# Step 3: Restore
# ---------------------------------------------------------------------------
log "Restoring $BACKUP_FILE into '$PG_DB'..."

compose_cmd exec -T postgres \
  psql -U "$PG_USER" "$PG_DB" < "$BACKUP_FILE" || {
  err "Restore failed. Database may be in a partial state."
  err "hush-api remains stopped."
  err ""
  err "Options:"
  err "  - Re-run this script with a known-good backup"
  err "  - Restart the stack and investigate: $DOCKER_COMPOSE -f $COMPOSE_BASE_FILE -f $COMPOSE_PROXY_FILE up -d"
  exit 1
}

# ---------------------------------------------------------------------------
# Done
# ---------------------------------------------------------------------------
log ""
log "============================================================"
log " Restore complete."
log " hush-api is STOPPED."
log "============================================================"
log ""
log "Before restarting:"
log "  - Verify the restored data looks correct"
log "  - If rolling back to an older codebase, check out that version now"
log "    (see docs/RUNBOOK.md for the full rollback procedure)"
log ""
log "When ready, restart:"
log "  $DOCKER_COMPOSE -f $COMPOSE_BASE_FILE -f $COMPOSE_PROXY_FILE up -d"
log ""
log "After restart:"
log "  - Check /api/health"
log "  - Check /api/handshake (verifies transparency log key is active)"
