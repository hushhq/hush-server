#!/usr/bin/env bash
# Reset all dev data: truncate Postgres tables + print browser cleanup instructions.
# Usage: ./scripts/reset-dev.sh
set -euo pipefail

CONTAINER="hush-postgres"
DB_USER="hush"
DB_NAME="hush"

echo "Resetting Hush dev database..."

if ! docker ps --format '{{.Names}}' | grep -q "^${CONTAINER}$"; then
  echo "ERROR: container '${CONTAINER}' is not running. Start it with: docker-compose up -d postgres"
  exit 1
fi

docker exec "$CONTAINER" psql -U "$DB_USER" -d "$DB_NAME" -c "
  TRUNCATE
    messages,
    signal_one_time_pre_keys,
    signal_identity_keys,
    devices,
    invite_codes,
    channel_config,
    channels,
    server_members,
    servers,
    sessions,
    users
  CASCADE;
"

echo ""
echo "DB reset done. All tables truncated."
echo ""
echo "Now clear browser state — paste this in the browser console:"
echo ""
echo "  sessionStorage.clear(); localStorage.clear(); indexedDB.databases().then(dbs => dbs.forEach(db => indexedDB.deleteDatabase(db.name))); location.reload();"
echo ""
