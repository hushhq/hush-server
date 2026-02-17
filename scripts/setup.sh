#!/bin/sh
# Hush one-command self-host setup. POSIX sh compatible.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_ROOT"

# 1. Dependency checks
missing=""
for cmd in docker docker-compose openssl; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    missing="${missing:+$missing }$cmd"
  fi
done
if [ -n "$missing" ]; then
  echo "Missing required tools: $missing. Install them and try again."
  exit 1
fi

# 2. Overwrite guard
if [ -f .env ]; then
  printf '.env already exists. Overwrite? [y/N] '
  read -r answer
  case "$answer" in [yY]) ;; *) exit 0;; esac
fi

# 3. Generate secrets
LIVEKIT_API_KEY="$(openssl rand -hex 16)"
LIVEKIT_API_SECRET="$(openssl rand -hex 32)"
SYNAPSE_REGISTRATION_SHARED_SECRET="$(openssl rand -hex 32)"
SYNAPSE_MACAROON_SECRET_KEY="$(openssl rand -hex 32)"
POSTGRES_PASSWORD="$(openssl rand -hex 16)"

# 4. Prompt for domain
printf 'Domain name (e.g. hush.example.com) [localhost]: '
read -r domain
if [ -z "$domain" ]; then domain=localhost; fi
MATRIX_SERVER_NAME="$domain"
if [ "$domain" = "localhost" ]; then
  LIVEKIT_URL="ws://localhost:7880"
else
  LIVEKIT_URL="ws://${domain}:7880"
fi

# 5. Write .env from .env.example with substitutions
cp .env.example .env
_env_tmp=".env.tmp"
sed "s|^LIVEKIT_API_KEY=.*|LIVEKIT_API_KEY=$LIVEKIT_API_KEY|" .env > "$_env_tmp" && mv "$_env_tmp" .env
sed "s|^LIVEKIT_API_SECRET=.*|LIVEKIT_API_SECRET=$LIVEKIT_API_SECRET|" .env > "$_env_tmp" && mv "$_env_tmp" .env
sed "s|^LIVEKIT_URL=.*|LIVEKIT_URL=$LIVEKIT_URL|" .env > "$_env_tmp" && mv "$_env_tmp" .env
sed "s|^MATRIX_SERVER_NAME=.*|MATRIX_SERVER_NAME=$MATRIX_SERVER_NAME|" .env > "$_env_tmp" && mv "$_env_tmp" .env
sed "s|^SYNAPSE_REGISTRATION_SHARED_SECRET=.*|SYNAPSE_REGISTRATION_SHARED_SECRET=$SYNAPSE_REGISTRATION_SHARED_SECRET|" .env > "$_env_tmp" && mv "$_env_tmp" .env
sed "s|^SYNAPSE_MACAROON_SECRET_KEY=.*|SYNAPSE_MACAROON_SECRET_KEY=$SYNAPSE_MACAROON_SECRET_KEY|" .env > "$_env_tmp" && mv "$_env_tmp" .env
sed "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=$POSTGRES_PASSWORD|" .env > "$_env_tmp" && mv "$_env_tmp" .env
rm -f "$_env_tmp"

# 6. Call generate-synapse-config.sh if it exists
if [ -f "./scripts/generate-synapse-config.sh" ]; then
  export MATRIX_SERVER_NAME POSTGRES_PASSWORD
  export POSTGRES_USER=synapse POSTGRES_DB=synapse
  if [ "$domain" = "localhost" ]; then
    export MATRIX_PUBLIC_BASEURL="http://localhost:8008"
  else
    export MATRIX_PUBLIC_BASEURL="https://matrix.$domain"
  fi
  sh "./scripts/generate-synapse-config.sh" || true
fi

# 7. Summary
echo ""
echo "=== Hush setup complete ==="
echo ""
echo "Generated .env with:"
echo "  LIVEKIT_API_KEY     = $LIVEKIT_API_KEY"
echo "  LIVEKIT_API_SECRET  = (hidden)"
echo "  MATRIX_SERVER_NAME  = $MATRIX_SERVER_NAME"
echo ""
echo "Next steps:"
echo "  1. docker-compose up -d"
echo "  2. Open http://$domain (or http://localhost:5173 for dev)"
echo ""
echo "For production (gethush.live / custom domain):"
echo "  - Set LIVEKIT_URL, LIVEKIT_API_KEY, LIVEKIT_API_SECRET from LiveKit Cloud"
echo "  - Use: docker-compose -f docker-compose.yml -f docker-compose.prod.yml up -d"
echo ""
