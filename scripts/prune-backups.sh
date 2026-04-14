#!/bin/sh
# Remove old Hush database backups beyond the retention threshold.
#
# Retention policy applied:
#   1. Always keep the N most recent backups (default: 5).
#   2. For backups older than that, keep the most recent backup of each
#      calendar month (monthly-anchor retention).
#   3. Remove everything else.
#
# Each .sql backup has a matching .meta file; both are removed together.
#
# Usage:
#   ./scripts/prune-backups.sh             # dry-run: show what would be removed
#   ./scripts/prune-backups.sh --apply     # apply deletions
#   ./scripts/prune-backups.sh --keep N    # keep N most recent (default: 5)
#   ./scripts/prune-backups.sh --dir PATH  # custom backup directory
#
# Exit codes:
#   0  Success (or nothing to prune)
#   1  Pre-flight failure

set -eu

LOG_PREFIX="[hush-prune]"
KEEP=5
APPLY=0
BACKUP_DIR="backups"

log() { printf '%s %s\n' "$LOG_PREFIX" "$1"; }
err() { printf '%s ERROR: %s\n' "$LOG_PREFIX" "$1" >&2; }
die() { err "$1"; exit "${2:-1}"; }

# ---------------------------------------------------------------------------
# Parse arguments
# ---------------------------------------------------------------------------
while [ $# -gt 0 ]; do
  case "$1" in
    --apply)       APPLY=1; shift ;;
    --keep)        KEEP="$2"; shift 2 ;;
    --dir)         BACKUP_DIR="$2"; shift 2 ;;
    *) die "Unknown flag: $1. Usage: $0 [--apply] [--keep N] [--dir PATH]" 1 ;;
  esac
done

# Validate --keep is a positive integer
case "$KEEP" in
  *[!0-9]*|"") die "--keep must be a positive integer, got: $KEEP" 1 ;;
esac
[ "$KEEP" -ge 1 ] || die "--keep must be at least 1" 1

# ---------------------------------------------------------------------------
# Resolve project root
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
BACKUP_DIR_ABS="$(cd "$PROJECT_ROOT" && printf '%s/%s' "$(pwd)" "$BACKUP_DIR")"

if [ ! -d "$BACKUP_DIR_ABS" ]; then
  log "Backup directory not found: $BACKUP_DIR_ABS — nothing to prune."
  exit 0
fi

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# Check if a value is in a space-separated list.
_contains() { case " $2 " in *" $1 "*) return 0 ;; esac; return 1; }

# Extract YYYYMM from backup filename: hush-20260413-222209.sql -> 202604
_month_of() {
  _b="$(basename "$1" .sql)"
  _b="${_b#hush-}"
  printf '%.6s' "$_b"
}

# ---------------------------------------------------------------------------
# Collect and sort backups (ascending = oldest first, newest last)
# ---------------------------------------------------------------------------
# shellcheck disable=SC2010
_backups="$(ls -1 "$BACKUP_DIR_ABS"/hush-*.sql 2>/dev/null | sort)"

if [ -z "$_backups" ]; then
  log "No backups found in $BACKUP_DIR_ABS."
  exit 0
fi

_count=0
for _f in $_backups; do _count=$((_count + 1)); done

log "Found $_count backup(s) in $BACKUP_DIR_ABS."
log "Keeping last $KEEP unconditionally plus one per calendar month for older entries."

# ---------------------------------------------------------------------------
# Determine what to keep
# ---------------------------------------------------------------------------
# Pass 1: iterate newest-first.
# - First KEEP entries: unconditional keep.
# - Beyond KEEP: keep the first (newest) occurrence of each calendar month.
# - Remaining: prune candidates.
# ---------------------------------------------------------------------------
_keep_list=""
_seen_months=""
_prune_list=""
_index=0

for _f in $(ls -1 "$BACKUP_DIR_ABS"/hush-*.sql 2>/dev/null | sort -r); do
  _index=$((_index + 1))

  if [ "$_index" -le "$KEEP" ]; then
    _keep_list="$_keep_list $_f"
    continue
  fi

  _month="$(_month_of "$_f")"

  if ! _contains "$_month" "$_seen_months"; then
    # Monthly anchor — newest backup of this month, keep it
    _seen_months="$_seen_months $_month"
    _keep_list="$_keep_list $_f"
    continue
  fi

  _prune_list="$_prune_list $_f"
done

# ---------------------------------------------------------------------------
# Report or apply
# ---------------------------------------------------------------------------
if [ -z "$_prune_list" ]; then
  log "Nothing to prune."
  exit 0
fi

_prune_count=0
for _f in $_prune_list; do _prune_count=$((_prune_count + 1)); done

if [ "$APPLY" -eq 0 ]; then
  log "Dry run — pass --apply to delete. Would remove $_prune_count backup(s):"
  for _f in $_prune_list; do
    _meta="${_f%.sql}.meta"
    if [ -f "$_meta" ]; then
      log "  $(basename "$_f")  +  $(basename "$_meta")"
    else
      log "  $(basename "$_f")"
    fi
  done
  exit 0
fi

log "Removing $_prune_count backup(s)..."
for _f in $_prune_list; do
  _meta="${_f%.sql}.meta"
  rm -f "$_f"
  [ -f "$_meta" ] && rm -f "$_meta"
  log "  Removed: $(basename "$_f")"
done

log "Done. $_prune_count backup(s) removed."
