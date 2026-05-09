#!/bin/sh
# Hush self-hoster onboarding script.
# Deploys a production Hush backend/media instance with TLS in under 10 minutes.
#
# Usage:
#   ./scripts/setup.sh                          # interactive mode
#   ./scripts/setup.sh --domain chat.example.com --email ops@example.com
#   ./scripts/setup.sh --ip 203.0.113.42        # IP-only with self-signed TLS
#   ./scripts/setup.sh --force --domain chat.example.com --email ops@example.com
#
# Flags:
#   --domain DOMAIN   Domain name pointing to this server (Let's Encrypt TLS)
#   --ip     IP       Server IP address (self-signed TLS via Caddy internal CA)
#   --email  EMAIL    Email for Let's Encrypt (required with --domain, ignored with --ip)
#   --force           Overwrite existing .env without prompting
#
# Modes:
#   Domain mode (--domain): Docker Compose starts Caddy, which obtains a real
#                           TLS cert from Let's Encrypt. Requires DNS pointing
#                           to this server on ports 80/443.
#   IP mode (--ip):         Docker Compose starts Caddy with a self-signed cert
#                           from its internal CA.
#                           Browsers show a certificate warning - E2EE is unaffected.
#
# Exit codes:
#   0  Success
#   1  Dependency or configuration failure
#   2  Health check failure after startup

set -eu

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------
COMPOSE_BASE_FILE="docker-compose.prod.yml"
COMPOSE_PROXY_FILE="docker-compose.caddy.yml"
CADDY_DOMAIN_TMPL="caddy/Caddyfile.self-hoster.tmpl"
CADDY_IP_TMPL="caddy/Caddyfile.ip.tmpl"
CADDY_OUT="caddy/Caddyfile.self-hoster"
LIVEKIT_TMPL="livekit/livekit.yaml"
HEALTH_URL="http://localhost:8080/api/health"
HANDSHAKE_URL="http://localhost:8080/api/handshake"
LOG_PREFIX="[hush]"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
log() { printf '%s %s\n' "$LOG_PREFIX" "$1"; }
err() { printf '%s ERROR: %s\n' "$LOG_PREFIX" "$1" >&2; }
die() { err "$1"; exit "${2:-1}"; }
# COMPOSE_PROFILE_ARGS is set later (after env decisions) so the s3-storage
# profile is enabled iff the bundled MinIO is wired in. Until set it is
# empty and compose_cmd behaves like before.
COMPOSE_PROFILE_ARGS=""
compose_cmd() { $DOCKER_COMPOSE -f "$COMPOSE_BASE_FILE" -f "$COMPOSE_PROXY_FILE" $COMPOSE_PROFILE_ARGS "$@"; }

# ---------------------------------------------------------------------------
# Parse arguments
# ---------------------------------------------------------------------------
domain=""
ip_addr=""
email=""
force=0

while [ $# -gt 0 ]; do
  case "$1" in
    --domain) domain="$2";  shift 2 ;;
    --ip)     ip_addr="$2"; shift 2 ;;
    --email)  email="$2";   shift 2 ;;
    --force)  force=1;      shift   ;;
    *) die "Unknown flag: $1" 1 ;;
  esac
done

# --domain and --ip are mutually exclusive
if [ -n "$domain" ] && [ -n "$ip_addr" ]; then
  die "Cannot use both --domain and --ip. Choose one." 1
fi

# ---------------------------------------------------------------------------
# Resolve script directory and move to project root
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_ROOT"

# ---------------------------------------------------------------------------
# Step 1: Dependency check
# ---------------------------------------------------------------------------
log "Checking dependencies..."

# Detect docker compose subcommand vs standalone docker-compose
DOCKER_COMPOSE=""
if docker compose version >/dev/null 2>&1; then
  DOCKER_COMPOSE="docker compose"
elif command -v docker-compose >/dev/null 2>&1; then
  DOCKER_COMPOSE="docker-compose"
fi

if ! command -v docker >/dev/null 2>&1 || [ -z "$DOCKER_COMPOSE" ]; then
  log "Docker is required but not installed."
  printf '%s Install Docker now? [y/N] ' "$LOG_PREFIX"
  read -r _install_docker
  case "$_install_docker" in
    [yY])
      log "Installing Docker via get.docker.com..."
      curl -fsSL https://get.docker.com | sh || die "Docker installation failed." 1
      # Ensure current user can run docker without sudo (takes effect on next login).
      if [ "$(id -u)" -ne 0 ] && command -v usermod >/dev/null 2>&1; then
        usermod -aG docker "$(whoami)" 2>/dev/null || true
      fi
      # Re-detect docker compose after install.
      if docker compose version >/dev/null 2>&1; then
        DOCKER_COMPOSE="docker compose"
      elif command -v docker-compose >/dev/null 2>&1; then
        DOCKER_COMPOSE="docker-compose"
      fi
      if ! command -v docker >/dev/null 2>&1 || [ -z "$DOCKER_COMPOSE" ]; then
        die "Docker installed but docker compose plugin not found. See https://docs.docker.com/compose/install/" 1
      fi
      log "Docker installed successfully."
      ;;
    *)
      die "Docker is required to run Hush. Install from https://docs.docker.com/engine/install/" 1
      ;;
  esac
fi
if ! command -v openssl >/dev/null 2>&1; then
  die "openssl is required. Install via your package manager (e.g. apt install openssl)" 1
fi

log "Dependencies OK."

# ---------------------------------------------------------------------------
# Step 2: Overwrite guard
# ---------------------------------------------------------------------------
if [ -f .env ] && [ "$force" -eq 0 ]; then
  printf '%s Existing .env found. Overwrite? [y/N] ' "$LOG_PREFIX"
  read -r _answer
  case "$_answer" in
    [yY]) ;;
    *) log "Aborted. Existing .env preserved."; exit 0 ;;
  esac
fi

# ---------------------------------------------------------------------------
# Step 3: Interactive prompts (skipped when flags provided)
# ---------------------------------------------------------------------------
if [ -z "$domain" ] && [ -z "$ip_addr" ]; then
  printf '%s Do you have a domain name? [y/N] ' "$LOG_PREFIX"
  read -r _has_domain
  case "$_has_domain" in
    [yY])
      printf '%s Enter your domain (e.g. chat.example.com): ' "$LOG_PREFIX"
      read -r domain
      [ -z "$domain" ] && die "Domain is required." 1
      ;;
    *)
      printf '%s Enter your server IP address: ' "$LOG_PREFIX"
      read -r ip_addr
      [ -z "$ip_addr" ] && die "IP address is required." 1
      ;;
  esac
fi

# Determine mode and host identifier
if [ -n "$domain" ]; then
  mode="domain"
  host="$domain"

  if [ -z "$email" ]; then
    printf '%s Enter your email (for Let'\''s Encrypt certificates): ' "$LOG_PREFIX"
    read -r email
    [ -z "$email" ] && die "Email is required for domain mode." 1
  fi

  log "Mode:   domain (Let's Encrypt TLS)"
  log "Domain: $domain"
  log "Email:  $email"
else
  mode="ip"
  host="$ip_addr"

  log "Mode:   IP (self-signed TLS)"
  log "IP:     $ip_addr"
  log ""
  log "NOTE: Browsers will show a certificate warning on first visit."
  log "      This is expected. Accept the warning to continue."
  log "      E2EE protects your data regardless of the TLS certificate."
fi

# ---------------------------------------------------------------------------
# Step 4: Secret generation
# ---------------------------------------------------------------------------
# When --force re-runs on a live instance, reuse secrets from the existing
# .env so the running Postgres volume stays compatible. Only generate fresh
# secrets for values that are missing.
# ---------------------------------------------------------------------------

_existing_env() {
  # Read a KEY=value from the existing .env, if present.
  if [ -f .env ]; then
    grep "^$1=" .env 2>/dev/null | head -1 | cut -d= -f2-
  fi
}

log "Generating secrets..."

JWT_SECRET="$(_existing_env JWT_SECRET)"
[ -z "$JWT_SECRET" ] && JWT_SECRET="$(openssl rand -hex 32)"

POSTGRES_PASSWORD="$(_existing_env POSTGRES_PASSWORD)"
[ -z "$POSTGRES_PASSWORD" ] && POSTGRES_PASSWORD="$(openssl rand -hex 16)"

ADMIN_BOOTSTRAP_SECRET="$(_existing_env ADMIN_BOOTSTRAP_SECRET)"
[ -z "$ADMIN_BOOTSTRAP_SECRET" ] && ADMIN_BOOTSTRAP_SECRET="$(openssl rand -hex 24)"

ADMIN_SESSION_TTL_HOURS="$(_existing_env ADMIN_SESSION_TTL_HOURS)"
[ -z "$ADMIN_SESSION_TTL_HOURS" ] && ADMIN_SESSION_TTL_HOURS="24"

SERVICE_IDENTITY_MASTER_KEY="$(_existing_env SERVICE_IDENTITY_MASTER_KEY)"
[ -z "$SERVICE_IDENTITY_MASTER_KEY" ] && SERVICE_IDENTITY_MASTER_KEY="$(openssl rand -hex 32)"

LIVEKIT_API_KEY="$(_existing_env LIVEKIT_API_KEY)"
[ -z "$LIVEKIT_API_KEY" ] && LIVEKIT_API_KEY="$(openssl rand -hex 16)"

LIVEKIT_API_SECRET="$(_existing_env LIVEKIT_API_SECRET)"
[ -z "$LIVEKIT_API_SECRET" ] && LIVEKIT_API_SECRET="$(openssl rand -hex 32)"

# Ed25519 seed - MUST persist across restarts; used for transparency log signing
TRANSPARENCY_LOG_PRIVATE_KEY="$(_existing_env TRANSPARENCY_LOG_PRIVATE_KEY)"
[ -z "$TRANSPARENCY_LOG_PRIVATE_KEY" ] && TRANSPARENCY_LOG_PRIVATE_KEY="$(openssl rand -hex 32)"

CORS_ORIGIN="$(_existing_env CORS_ORIGIN)"
[ -z "$CORS_ORIGIN" ] && CORS_ORIGIN="https://app.gethush.live"

# Self-host MinIO credentials and the matching STORAGE_S3_* values that
# point hush-api at it. Domain mode wires MinIO via a `storage.<domain>`
# Caddy subdomain (DNS A record required); IP mode leaves the bulk
# plane on `postgres_bytea` because there is no usable browser-trusted
# external URL for an IP-only deploy.
MINIO_ROOT_USER="$(_existing_env MINIO_ROOT_USER)"
[ -z "$MINIO_ROOT_USER" ] && MINIO_ROOT_USER="hush-minio-$(openssl rand -hex 4)"

MINIO_ROOT_PASSWORD="$(_existing_env MINIO_ROOT_PASSWORD)"
[ -z "$MINIO_ROOT_PASSWORD" ] && MINIO_ROOT_PASSWORD="$(openssl rand -hex 24)"

if [ "$mode" = "domain" ]; then
  STORAGE_BACKEND="s3"
  STORAGE_S3_ENDPOINT="storage.$domain"
  STORAGE_S3_REGION="us-east-1"
  STORAGE_S3_BUCKET="hush-link-archive"
  STORAGE_S3_ACCESS_KEY="$MINIO_ROOT_USER"
  STORAGE_S3_SECRET_KEY="$MINIO_ROOT_PASSWORD"
  STORAGE_S3_USE_SSL="true"
  ATTACHMENT_STORAGE_BACKEND="$(_existing_env ATTACHMENT_STORAGE_BACKEND)"
  [ -z "$ATTACHMENT_STORAGE_BACKEND" ] && ATTACHMENT_STORAGE_BACKEND="s3"
  ATTACHMENT_STORAGE_S3_ENDPOINT="$(_existing_env ATTACHMENT_STORAGE_S3_ENDPOINT)"
  [ -z "$ATTACHMENT_STORAGE_S3_ENDPOINT" ] && ATTACHMENT_STORAGE_S3_ENDPOINT="$STORAGE_S3_ENDPOINT"
  ATTACHMENT_STORAGE_S3_REGION="$(_existing_env ATTACHMENT_STORAGE_S3_REGION)"
  [ -z "$ATTACHMENT_STORAGE_S3_REGION" ] && ATTACHMENT_STORAGE_S3_REGION="$STORAGE_S3_REGION"
  ATTACHMENT_STORAGE_S3_BUCKET="$(_existing_env ATTACHMENT_STORAGE_S3_BUCKET)"
  [ -z "$ATTACHMENT_STORAGE_S3_BUCKET" ] && ATTACHMENT_STORAGE_S3_BUCKET="hush-attachments"
  ATTACHMENT_STORAGE_S3_ACCESS_KEY="$(_existing_env ATTACHMENT_STORAGE_S3_ACCESS_KEY)"
  [ -z "$ATTACHMENT_STORAGE_S3_ACCESS_KEY" ] && ATTACHMENT_STORAGE_S3_ACCESS_KEY="$MINIO_ROOT_USER"
  ATTACHMENT_STORAGE_S3_SECRET_KEY="$(_existing_env ATTACHMENT_STORAGE_S3_SECRET_KEY)"
  [ -z "$ATTACHMENT_STORAGE_S3_SECRET_KEY" ] && ATTACHMENT_STORAGE_S3_SECRET_KEY="$MINIO_ROOT_PASSWORD"
  ATTACHMENT_STORAGE_S3_USE_SSL="$(_existing_env ATTACHMENT_STORAGE_S3_USE_SSL)"
  [ -z "$ATTACHMENT_STORAGE_S3_USE_SSL" ] && ATTACHMENT_STORAGE_S3_USE_SSL="$STORAGE_S3_USE_SSL"
  MINIO_SERVER_URL="https://storage.$domain"
  # Bring up the bundled MinIO + bootstrap one-shot.
  COMPOSE_PROFILE_ARGS="--profile s3-storage"
else
  # IP mode: bulk plane stays on postgres_bytea by default. Operator
  # can flip this manually once they wire an external S3 endpoint.
  STORAGE_BACKEND="postgres_bytea"
  STORAGE_S3_ENDPOINT=""
  STORAGE_S3_REGION="us-east-1"
  STORAGE_S3_BUCKET="hush-link-archive"
  STORAGE_S3_ACCESS_KEY=""
  STORAGE_S3_SECRET_KEY=""
  STORAGE_S3_USE_SSL="true"
  ATTACHMENT_STORAGE_BACKEND="$(_existing_env ATTACHMENT_STORAGE_BACKEND)"
  [ -z "$ATTACHMENT_STORAGE_BACKEND" ] && ATTACHMENT_STORAGE_BACKEND="$STORAGE_BACKEND"
  ATTACHMENT_STORAGE_S3_ENDPOINT="$(_existing_env ATTACHMENT_STORAGE_S3_ENDPOINT)"
  ATTACHMENT_STORAGE_S3_REGION="$(_existing_env ATTACHMENT_STORAGE_S3_REGION)"
  [ -z "$ATTACHMENT_STORAGE_S3_REGION" ] && ATTACHMENT_STORAGE_S3_REGION="$STORAGE_S3_REGION"
  ATTACHMENT_STORAGE_S3_BUCKET="$(_existing_env ATTACHMENT_STORAGE_S3_BUCKET)"
  [ -z "$ATTACHMENT_STORAGE_S3_BUCKET" ] && ATTACHMENT_STORAGE_S3_BUCKET="hush-attachments"
  ATTACHMENT_STORAGE_S3_ACCESS_KEY="$(_existing_env ATTACHMENT_STORAGE_S3_ACCESS_KEY)"
  ATTACHMENT_STORAGE_S3_SECRET_KEY="$(_existing_env ATTACHMENT_STORAGE_S3_SECRET_KEY)"
  ATTACHMENT_STORAGE_S3_USE_SSL="$(_existing_env ATTACHMENT_STORAGE_S3_USE_SSL)"
  [ -z "$ATTACHMENT_STORAGE_S3_USE_SSL" ] && ATTACHMENT_STORAGE_S3_USE_SSL="$STORAGE_S3_USE_SSL"
  MINIO_SERVER_URL=""
fi

STORAGE_BROWSER_ORIGIN="$(_existing_env STORAGE_BROWSER_ORIGIN)"
[ -z "$STORAGE_BROWSER_ORIGIN" ] && STORAGE_BROWSER_ORIGIN="$CORS_ORIGIN"

LINK_ARCHIVE_USER_QUOTA="$(_existing_env LINK_ARCHIVE_USER_QUOTA)"
[ -z "$LINK_ARCHIVE_USER_QUOTA" ] && LINK_ARCHIVE_USER_QUOTA="1"

LINK_ARCHIVE_STAGING_BYTES_CAP="$(_existing_env LINK_ARCHIVE_STAGING_BYTES_CAP)"
[ -z "$LINK_ARCHIVE_STAGING_BYTES_CAP" ] && LINK_ARCHIVE_STAGING_BYTES_CAP="8589934592"

# ---------------------------------------------------------------------------
# Step 5: Write .env
# ---------------------------------------------------------------------------
log "Writing .env..."

# Set NODE_IP for IP-mode deployments (helps LiveKit discover the correct ICE candidate)
if [ "$mode" = "ip" ]; then
  node_ip="$ip_addr"
else
  node_ip=""
fi

cat > .env <<ENVEOF
# Hush production environment - generated by scripts/setup.sh
# DO NOT commit this file to version control.

# --- Host -----------------------------------------------------------------
# The public hostname or IP address of this server.
DOMAIN=$host
# Enable production-only safeguards (e.g. persistent transparency signer).
PRODUCTION=true

# Browser client origin allowed to open WebSocket/API connections to this instance.
# Default points at the official hosted web client. Change this if you self-host hush-web.
CORS_ORIGIN=$CORS_ORIGIN

# --- PostgreSQL -----------------------------------------------------------
POSTGRES_USER=hush
POSTGRES_DB=hush
# Generated password - do not change after first startup (breaks DB access)
POSTGRES_PASSWORD=$POSTGRES_PASSWORD

# --- Authentication -------------------------------------------------------
# JWT signing secret - changing this invalidates all active sessions
JWT_SECRET=$JWT_SECRET

# --- Instance admin -------------------------------------------------------
# One-time bootstrap secret for creating the first local owner account
ADMIN_BOOTSTRAP_SECRET=$ADMIN_BOOTSTRAP_SECRET
# Dashboard session lifetime in hours
ADMIN_SESSION_TTL_HOURS=$ADMIN_SESSION_TTL_HOURS
# 32-byte wrapping key used to protect the service identity private key at rest
SERVICE_IDENTITY_MASTER_KEY=$SERVICE_IDENTITY_MASTER_KEY

# --- LiveKit (self-hosted) ------------------------------------------------
LIVEKIT_API_KEY=$LIVEKIT_API_KEY
LIVEKIT_API_SECRET=$LIVEKIT_API_SECRET

# --- Transparency log -----------------------------------------------------
# Ed25519 seed (hex) - MUST NOT change after first message is logged
TRANSPARENCY_LOG_PRIVATE_KEY=$TRANSPARENCY_LOG_PRIVATE_KEY

# --- Device-link bulk transfer storage ------------------------------------
# Self-host MinIO is wired automatically in domain mode; IP-mode deploys
# fall back to postgres_bytea until the operator points STORAGE_S3_* at
# an external bucket.
STORAGE_BACKEND=$STORAGE_BACKEND
STORAGE_S3_ENDPOINT=$STORAGE_S3_ENDPOINT
STORAGE_S3_REGION=$STORAGE_S3_REGION
STORAGE_S3_BUCKET=$STORAGE_S3_BUCKET
STORAGE_S3_ACCESS_KEY=$STORAGE_S3_ACCESS_KEY
STORAGE_S3_SECRET_KEY=$STORAGE_S3_SECRET_KEY
STORAGE_S3_USE_SSL=$STORAGE_S3_USE_SSL

# --- Chat attachment storage ---------------------------------------------
# Defaults to the same S3 endpoint/credentials as device-link storage but
# uses a separate bucket so chat blobs and link-device archives cannot mix.
ATTACHMENT_STORAGE_BACKEND=$ATTACHMENT_STORAGE_BACKEND
ATTACHMENT_STORAGE_S3_ENDPOINT=$ATTACHMENT_STORAGE_S3_ENDPOINT
ATTACHMENT_STORAGE_S3_REGION=$ATTACHMENT_STORAGE_S3_REGION
ATTACHMENT_STORAGE_S3_BUCKET=$ATTACHMENT_STORAGE_S3_BUCKET
ATTACHMENT_STORAGE_S3_ACCESS_KEY=$ATTACHMENT_STORAGE_S3_ACCESS_KEY
ATTACHMENT_STORAGE_S3_SECRET_KEY=$ATTACHMENT_STORAGE_S3_SECRET_KEY
ATTACHMENT_STORAGE_S3_USE_SSL=$ATTACHMENT_STORAGE_S3_USE_SSL

# Self-host MinIO root credentials. The bundled MinIO container reads
# these; STORAGE_S3_ACCESS_KEY/SECRET_KEY above are intentionally set
# to the same values for self-host installs (single tenant).
MINIO_ROOT_USER=$MINIO_ROOT_USER
MINIO_ROOT_PASSWORD=$MINIO_ROOT_PASSWORD
MINIO_SERVER_URL=$MINIO_SERVER_URL

# Operational containment for the bulk plane.
LINK_ARCHIVE_USER_QUOTA=$LINK_ARCHIVE_USER_QUOTA
LINK_ARCHIVE_STAGING_BYTES_CAP=$LINK_ARCHIVE_STAGING_BYTES_CAP

# Origin allowed by the bucket CORS policy. minio-bootstrap reads this.
STORAGE_BROWSER_ORIGIN=$STORAGE_BROWSER_ORIGIN

# --- Optional / advanced --------------------------------------------------
# NODE_IP: public IP of this server for LiveKit ICE candidates.
# Auto-set for --ip mode; leave blank to auto-detect for domain mode.
NODE_IP=$node_ip
ENVEOF

# ---------------------------------------------------------------------------
# Step 6: Generate Caddy config from template
# ---------------------------------------------------------------------------
log "Writing Caddy config..."

if [ "$mode" = "domain" ]; then
  CADDY_TMPL="$CADDY_DOMAIN_TMPL"
  if [ ! -f "$CADDY_TMPL" ]; then
    die "Caddy template not found: $CADDY_TMPL" 1
  fi
  sed "s/__DOMAIN__/$domain/g; s/__EMAIL__/$email/g" "$CADDY_TMPL" > "$CADDY_OUT"
else
  CADDY_TMPL="$CADDY_IP_TMPL"
  if [ ! -f "$CADDY_TMPL" ]; then
    die "Caddy template not found: $CADDY_TMPL" 1
  fi
  sed "s/__IP__/$ip_addr/g" "$CADDY_TMPL" > "$CADDY_OUT"
fi

log "Caddy config written to $CADDY_OUT"

# ---------------------------------------------------------------------------
# Step 7: Generate LiveKit config
# ---------------------------------------------------------------------------
log "Writing LiveKit config..."

if [ ! -f "$LIVEKIT_TMPL" ]; then
  # Create a minimal template if it doesn't exist yet
  mkdir -p "$(dirname "$LIVEKIT_TMPL")"
  cat > "$LIVEKIT_TMPL" <<LKEOF
# LiveKit Server Configuration (template - populated at container startup)
port: 7880

rtc:
  port_range_start: 50020
  port_range_end: 50100
  tcp_port: 7881
  use_external_ip: false
  node_ip: __NODE_IP__

keys:
  __LIVEKIT_API_KEY__: __LIVEKIT_API_SECRET__

room:
  auto_create: true
  empty_timeout: 0
  enabled_codecs:
    - mime: audio/opus
    - mime: video/h264
    - mime: video/vp8
    - mime: video/vp9

webhook:
  urls:
    - http://hush-api:8080/api/livekit/webhook
  api_key: __LIVEKIT_API_KEY__

logging:
  level: info
LKEOF
  log "Created livekit/livekit.yaml template."
fi

# The livekit.yaml is a template - key substitution happens at container startup
# via the docker-compose.prod.yml entrypoint (sed on __LIVEKIT_API_KEY__ etc.)
# Nothing to modify here; the .env values are picked up by the compose env block.
log "LiveKit config OK (using $LIVEKIT_TMPL template; keys injected at container startup)."

# ---------------------------------------------------------------------------
# Step 8: Stop existing stack
# ---------------------------------------------------------------------------
# If a previous setup failed mid-init, Postgres may have a volume with the
# wrong password baked in. _existing_env() above already handles this by
# reusing the existing .env credentials. Never auto-remove volumes when
# containers are stopped - the operator may have stopped them for maintenance.
#
# If you need a completely fresh start after a failed first setup (no data to
# preserve), manually run: docker compose -f docker-compose.prod.yml down -v
if compose_cmd ps -q 2>/dev/null | grep -q .; then
  log "Stopping existing Hush stack..."
  # Use down without -v to preserve data volumes. Only remove volumes if
  # no postgres data exists yet (fresh/failed first setup with no DB yet).
  if compose_cmd exec -T postgres pg_isready -U "${POSTGRES_USER:-hush}" >/dev/null 2>&1; then
    log "Existing database detected - preserving data volumes."
    compose_cmd down 2>/dev/null || true
  else
    log "No healthy database found - removing stale volumes from failed first setup."
    compose_cmd down -v 2>/dev/null || true
  fi
fi

# ---------------------------------------------------------------------------
# Step 9: Build and pull Docker images
# ---------------------------------------------------------------------------
log "Building hush-api and pulling runtime dependencies (this may take several minutes on first run)..."
compose_cmd build hush-api
compose_cmd pull --ignore-buildable

# ---------------------------------------------------------------------------
# Step 9: Start stack
# ---------------------------------------------------------------------------
log "Starting Hush stack..."
compose_cmd up -d

# ---------------------------------------------------------------------------
# Step 10: Health check with retry
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

      # Verify handshake endpoint returns valid JSON
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
  die "Startup health check failed. See logs above." 2
fi

# ---------------------------------------------------------------------------
# Step 11: Success message
# ---------------------------------------------------------------------------
printf '\n'
log "================================================================"
log " Hush is live at https://$host"
log "================================================================"
printf '\n'
if [ "$mode" = "domain" ]; then
  log "Open https://app.gethush.live and add this instance URL:"
  log "  https://$host"
  log "If you self-host hush-web later, update CORS_ORIGIN in .env to that origin."
  printf '\n'
fi
if [ "$mode" = "ip" ]; then
  log "Self-signed TLS: your browser will show a certificate warning."
  log "Accept the warning to proceed. E2EE is unaffected."
  printf '\n'
fi
log "Admin dashboard:          https://$host/admin/"
log "Admin bootstrap secret:   $ADMIN_BOOTSTRAP_SECRET"
printf '\n'

# Bulk-plane (device-link archive transfer) status banner
if [ "$STORAGE_BACKEND" = "s3" ]; then
  log "--- Device-link bulk transfer (MinIO/S3) ---"
  log "Bucket:    $STORAGE_S3_BUCKET on $STORAGE_S3_ENDPOINT"
  log "Attachments bucket: $ATTACHMENT_STORAGE_S3_BUCKET"
  log "Origin:    $STORAGE_BROWSER_ORIGIN (CORS allowed by bucket policy)"
  log ""
  log "ACTION REQUIRED: create a DNS A record"
  log "  storage.$host  ->  this server's public IP"
  log ""
  log "Caddy will obtain a Let's Encrypt cert for storage.$host on first"
  log "request. The browser PUTs/GETs presigned URLs there directly;"
  log "chunks never traverse the API process."
  printf '\n'
else
  log "--- Device-link bulk transfer (Postgres BYTEA fallback) ---"
  log "STORAGE_BACKEND=postgres_bytea (no S3 backend wired)."
  log "Small-account device-link transfer works through the in-API path,"
  log "but the reverse-proxy body limit applies. To enable the full S3"
  log "bulk plane, set STORAGE_BACKEND=s3 and STORAGE_S3_* in .env, then"
  log "restart the stack. See docs/RUNBOOK.md."
  printf '\n'
fi
log "--- Preserve your secrets ---"
log "All secrets are saved in .env. Back it up to a secure location NOW."
log ""
log "CRITICAL: TRANSPARENCY_LOG_PRIVATE_KEY in .env is not rotatable."
log "Once the transparency log has entries, changing this key permanently"
log "invalidates all historical key-operation proofs for your users."
log "Store it separately from other secrets (e.g. offline or in a secrets manager)."
printf '\n'
log "To update Hush in the future, run:  ./scripts/update.sh"
log "To take a manual backup, run:       ./scripts/backup.sh"
log "To restore from backup, run:        ./scripts/restore.sh <file>"
