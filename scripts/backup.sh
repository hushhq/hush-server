#!/bin/sh
# Standalone database backup for Hush self-hosters.
# Run this any time you need a manual snapshot: before risky config changes,
# before manual DB work, or on a cron schedule.
#
# Usage:
#   ./scripts/backup.sh              # saves to backups/ with timestamp
#   ./scripts/backup.sh --dir /path  # custom output directory
#
# Exit codes:
#   0  Success
#   1  Pre-flight or backup failure
#
# ---- SCOPE ----------------------------------------------------------------
# This script backs up the PostgreSQL database ONLY.
# Redis data (session cache, rate-limiting counters) is intentionally excluded.
# Redis state is ephemeral: counters reset safely after restart, and active
# sessions will re-authenticate on next request.
#
# The database backup is INCOMPLETE without the corresponding .env file.
# A restored database is inoperable without:
#   - POSTGRES_PASSWORD       (postgres will reject the connection)
#   - TRANSPARENCY_LOG_PRIVATE_KEY  (historical proof verification breaks)
#   - SERVICE_IDENTITY_MASTER_KEY   (service identity private key cannot be unwrapped)
#   - JWT_SECRET              (all active sessions become invalid)
#
# Store .env and the database backup in separate secure locations.
# ---------------------------------------------------------------------------

set -eu

COMPOSE_BASE_FILE="docker-compose.prod.yml"
COMPOSE_PROXY_FILE="docker-compose.caddy.yml"
LOG_PREFIX="[hush-backup]"
BACKUP_DIR="backups"

log() { printf '%s %s\n' "$LOG_PREFIX" "$1"; }
err() { printf '%s ERROR: %s\n' "$LOG_PREFIX" "$1" >&2; }
die() { err "$1"; exit "${2:-1}"; }
compose_cmd() { $DOCKER_COMPOSE -f "$COMPOSE_BASE_FILE" -f "$COMPOSE_PROXY_FILE" "$@"; }

# ---------------------------------------------------------------------------
# Parse arguments
# ---------------------------------------------------------------------------
while [ $# -gt 0 ]; do
  case "$1" in
    --dir) BACKUP_DIR="$2"; shift 2 ;;
    *) die "Unknown flag: $1. Usage: $0 [--dir <path>]" 1 ;;
  esac
done

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
  die ".env not found. Run scripts/setup.sh first." 1
fi

PG_USER="$(grep -m1 '^POSTGRES_USER=' .env | cut -d= -f2 || echo "hush")"
PG_DB="$(grep -m1 '^POSTGRES_DB=' .env | cut -d= -f2 || echo "hush")"

if ! compose_cmd ps postgres 2>/dev/null | grep -qE "Up|running"; then
  # If a container named hush-postgres exists but isn't visible to this compose
  # project, the stack was likely started with a custom -p flag or from a
  # different directory, making the project name mismatch.
  if docker ps --filter "name=hush-postgres" --format "{{.Names}}" 2>/dev/null | grep -q "^hush-postgres$"; then
    err "Postgres is running but not visible to compose project '$(basename "$PROJECT_ROOT")'."
    err ""
    err "The stack was probably started with a different project name. Check:"
    err "  docker inspect hush-postgres --format '{{index .Config.Labels \"com.docker.compose.project\"}}'"
    err ""
    err "To fix: restart the stack from this directory without a custom -p flag:"
    err "  $DOCKER_COMPOSE -f $COMPOSE_BASE_FILE -f $COMPOSE_PROXY_FILE up -d"
    exit 1
  fi
  die "Postgres container is not running. Start the stack first:
    $DOCKER_COMPOSE -f $COMPOSE_BASE_FILE -f $COMPOSE_PROXY_FILE up -d postgres" 1
fi

# ---------------------------------------------------------------------------
# Backup
# ---------------------------------------------------------------------------
mkdir -p "$BACKUP_DIR"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
BACKUP_FILE="$BACKUP_DIR/hush-${TIMESTAMP}.sql"

log "Backing up database to $BACKUP_FILE..."

# --clean --if-exists: dump includes DROP statements so restore is idempotent
# even against an existing non-empty database.
compose_cmd exec -T postgres \
  pg_dump -U "$PG_USER" --clean --if-exists "$PG_DB" > "$BACKUP_FILE" || {
  err "Backup failed. Removing partial file."
  rm -f "$BACKUP_FILE"
  exit 1
}

log "Backup complete: $BACKUP_FILE"
log ""
log "--- IMPORTANT: backup is incomplete without .env ---"
log "Also back up .env separately (use a secrets manager or offline storage)."
log "The following .env values are required to make this backup operable:"
log "  POSTGRES_PASSWORD          - required to start postgres"
log "  TRANSPARENCY_LOG_PRIVATE_KEY - required to verify historical log proofs"
log "  SERVICE_IDENTITY_MASTER_KEY  - required to decrypt service identity"
log "  JWT_SECRET                 - required for session continuity"
log ""
log "To restore: ./scripts/restore.sh $BACKUP_FILE"
