#!/bin/sh
# Render a runtime .env from a SOPS-encrypted deploy secrets file.
#
# Default behavior is intentionally conservative:
# - if no encrypted secrets source is configured, do nothing
# - if a secrets file is configured, require sops and render .env atomically
#
# Usage:
#   ./scripts/render-env.sh                 # no-op if no secrets source configured
#   ./scripts/render-env.sh --required      # fail if no secrets file configured
#   ./scripts/render-env.sh --secrets-file /path/to/file
#   ./scripts/render-env.sh --output /path/to/.env
#
# Optional host-local config:
#   .env.render.conf
#     HUSH_SERVER_SECRETS_FILE=/path/to/encrypted.env

set -eu

LOG_PREFIX="[hush-env]"

log() { printf '%s %s\n' "$LOG_PREFIX" "$1"; }
err() { printf '%s ERROR: %s\n' "$LOG_PREFIX" "$1" >&2; }
die() { err "$1"; exit "${2:-1}"; }

required=0
secrets_file=""
output_file=""

while [ $# -gt 0 ]; do
  case "$1" in
    --required) required=1; shift ;;
    --secrets-file) secrets_file="$2"; shift 2 ;;
    --output) output_file="$2"; shift 2 ;;
    *) die "Unknown flag: $1" 1 ;;
  esac
done

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
config_file="$PROJECT_ROOT/.env.render.conf"

if [ -f "$config_file" ]; then
  # shellcheck disable=SC1090
  . "$config_file"
fi

[ -n "$secrets_file" ] || secrets_file="${HUSH_SERVER_SECRETS_FILE:-}"
[ -n "$output_file" ] || output_file="$PROJECT_ROOT/.env"

if [ -z "$secrets_file" ]; then
  if [ "$required" -eq 1 ]; then
    die "No encrypted secrets source configured. Use --secrets-file or HUSH_SERVER_SECRETS_FILE." 1
  fi
  exit 0
fi

if [ ! -f "$secrets_file" ]; then
  if [ "$required" -eq 1 ]; then
    die "SOPS secrets file not found: $secrets_file" 1
  fi
  exit 0
fi

if ! command -v sops >/dev/null 2>&1; then
  die "sops is required to decrypt $secrets_file" 1
fi

output_dir="$(dirname "$output_file")"
mkdir -p "$output_dir"

tmp_file="$(mktemp "$output_dir/.env.tmp.XXXXXX")"
cleanup() { rm -f "$tmp_file"; }
trap cleanup EXIT INT TERM HUP

chmod 600 "$tmp_file"

if ! sops --decrypt "$secrets_file" > "$tmp_file"; then
  die "Failed to decrypt $secrets_file" 1
fi

if [ ! -s "$tmp_file" ]; then
  die "Decrypted env is empty: $secrets_file" 1
fi

chmod 600 "$tmp_file"
mv "$tmp_file" "$output_file"
trap - EXIT INT TERM HUP

log "Rendered $(basename "$output_file") from $secrets_file"
