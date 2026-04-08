#!/bin/sh
# Hush upgrade script - updates the self-host backend/media stack and Caddy proxy.
# NEVER overwrites secrets (.env is preserved as-is).
#
# Usage:
#   ./scripts/update.sh
#
# Prerequisites:
#   - setup.sh must have been run at least once (.env must exist)
#   - Docker and docker-compose must be running
#
# Exit codes:
#   0  Success
#   1  Pre-flight check failure
#   2  Health check failure after restart

set -eu

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------
COMPOSE_BASE_FILE="docker-compose.prod.yml"
COMPOSE_PROXY_FILE="docker-compose.caddy.yml"
HEALTH_URL="http://localhost:8080/api/health"
HANDSHAKE_URL="http://localhost:8080/api/handshake"
LOG_PREFIX="[hush]"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
log() { printf '%s %s\n' "$LOG_PREFIX" "$1"; }
err() { printf '%s ERROR: %s\n' "$LOG_PREFIX" "$1" >&2; }
die() { err "$1"; exit "${2:-1}"; }
compose_cmd() { $DOCKER_COMPOSE -f "$COMPOSE_BASE_FILE" -f "$COMPOSE_PROXY_FILE" "$@"; }

# ---------------------------------------------------------------------------
# Resolve project root
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_ROOT"

# ---------------------------------------------------------------------------
# Detect docker compose invocation
# ---------------------------------------------------------------------------
DOCKER_COMPOSE=""
if docker compose version >/dev/null 2>&1; then
  DOCKER_COMPOSE="docker compose"
elif command -v docker-compose >/dev/null 2>&1; then
  DOCKER_COMPOSE="docker-compose"
fi

# ---------------------------------------------------------------------------
# Step 1: Pre-flight checks
# ---------------------------------------------------------------------------
log "Running pre-flight checks..."

if ! command -v docker >/dev/null 2>&1; then
  die "Docker is not installed. Install from https://docs.docker.com/engine/install/" 1
fi

if [ -z "$DOCKER_COMPOSE" ]; then
  die "docker-compose (or docker compose plugin) is not installed." 1
fi

if [ ! -f .env ]; then
  die ".env not found. Run scripts/setup.sh first to initialise your instance." 1
fi

if ! docker info >/dev/null 2>&1; then
  die "Docker daemon is not running. Start it and try again." 1
fi

log "Pre-flight checks passed."

# ---------------------------------------------------------------------------
# Step 2: Backup database
# ---------------------------------------------------------------------------
BACKUP_DIR="backups"
mkdir -p "$BACKUP_DIR"
BACKUP_FILE="$BACKUP_DIR/hush-$(date +%Y%m%d-%H%M%S).sql"

log "Backing up database to $BACKUP_FILE..."

# Source .env for configurable DB credentials (defaults match setup.sh)
PG_USER="${POSTGRES_USER:-hush}"
PG_DB="${POSTGRES_DB:-hush}"
if [ -f .env ]; then
  PG_USER="$(grep -m1 '^POSTGRES_USER=' .env | cut -d= -f2 || echo "$PG_USER")"
  PG_DB="$(grep -m1 '^POSTGRES_DB=' .env | cut -d= -f2 || echo "$PG_DB")"
fi

if compose_cmd ps postgres 2>/dev/null | grep -q "Up\|running"; then
  compose_cmd exec -T postgres \
    pg_dump -U "$PG_USER" "$PG_DB" > "$BACKUP_FILE" 2>/dev/null || {
    err "Database backup failed. Aborting update to protect your data."
    err "If postgres is not running, start it first: $DOCKER_COMPOSE -f $COMPOSE_BASE_FILE -f $COMPOSE_PROXY_FILE up -d postgres"
    exit 1
  }
  log "Database backup saved: $BACKUP_FILE"
else
  err "Postgres container is not running. Cannot create backup."
  err "Start the stack first: $DOCKER_COMPOSE -f $COMPOSE_BASE_FILE -f $COMPOSE_PROXY_FILE up -d"
  exit 1
fi

# ---------------------------------------------------------------------------
# Step 3: Pull latest code and rebuild images
# ---------------------------------------------------------------------------
log "Pulling latest code..."
git pull --ff-only || log "WARNING: git pull failed - building from current local code."

log "Rebuilding hush-api and pulling runtime dependencies..."
compose_cmd build hush-api
compose_cmd pull --ignore-buildable

# ---------------------------------------------------------------------------
# Step 4: Restart services
# ---------------------------------------------------------------------------
log "Restarting Hush stack..."
compose_cmd up -d

# ---------------------------------------------------------------------------
# Step 5: Health check with retry
# ---------------------------------------------------------------------------
wait_for_health() {
  _attempt=1
  _max=3
  _delay=5

  log "Waiting for API to be ready..."

  while [ "$_attempt" -le "$_max" ]; do
    log "Health check attempt $_attempt of $_max (waiting ${_delay}s)..."
    sleep "$_delay"

    if curl -sf "$HEALTH_URL" >/dev/null 2>&1; then
      log "API is healthy."

      _handshake="$(curl -sf "$HANDSHAKE_URL" 2>/dev/null || true)"
      if [ -z "$_handshake" ]; then
        err "Handshake endpoint returned empty response."
      else
        log "Handshake OK."
      fi
      return 0
    fi

    _attempt=$((_attempt + 1))
    _delay=$((_delay * 2))
  done

  err "API did not become healthy after $_max attempts."
  err "Check logs with: $DOCKER_COMPOSE -f $COMPOSE_BASE_FILE -f $COMPOSE_PROXY_FILE logs hush-api"
  return 1
}

if ! wait_for_health; then
  die "Post-update health check failed. Backup is at $BACKUP_FILE" 2
fi

# ---------------------------------------------------------------------------
# Step 6: Success message
# ---------------------------------------------------------------------------
printf '\n'
log "================================================================"
log " Update complete."
log " Backup saved to $BACKUP_FILE"
log "================================================================"
printf '\n'
