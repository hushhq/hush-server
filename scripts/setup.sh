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

# 3. Prompt for domain
printf 'Domain name (e.g. hush.example.com) [localhost]: '
read -r domain
if [ -z "$domain" ]; then domain=localhost; fi
if [ "$domain" = "localhost" ]; then
  LIVEKIT_URL="ws://localhost:7880"
  LIVEKIT_API_KEY=devkey
  LIVEKIT_API_SECRET=devsecret
else
  LIVEKIT_URL="ws://${domain}:7880"
  LIVEKIT_API_KEY="$(openssl rand -hex 16)"
  LIVEKIT_API_SECRET="$(openssl rand -hex 32)"
fi

JWT_SECRET="$(openssl rand -hex 32)"
POSTGRES_PASSWORD="$(openssl rand -hex 16)"

# 4. Write .env from .env.example with substitutions
cp .env.example .env
_env_tmp=".env.tmp"
sed "s|^LIVEKIT_API_KEY=.*|LIVEKIT_API_KEY=$LIVEKIT_API_KEY|" .env > "$_env_tmp" && mv "$_env_tmp" .env
sed "s|^LIVEKIT_API_SECRET=.*|LIVEKIT_API_SECRET=$LIVEKIT_API_SECRET|" .env > "$_env_tmp" && mv "$_env_tmp" .env
sed "s|^LIVEKIT_URL=.*|LIVEKIT_URL=$LIVEKIT_URL|" .env > "$_env_tmp" && mv "$_env_tmp" .env
sed "s|^JWT_SECRET=.*|JWT_SECRET=$JWT_SECRET|" .env > "$_env_tmp" && mv "$_env_tmp" .env
sed "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=$POSTGRES_PASSWORD|" .env > "$_env_tmp" && mv "$_env_tmp" .env
rm -f "$_env_tmp"

# 5. Summary
echo ""
echo "=== Hush setup complete ==="
echo ""
echo "Generated .env with:"
echo "  LIVEKIT_API_KEY     = $LIVEKIT_API_KEY"
echo "  LIVEKIT_API_SECRET  = (hidden)"
echo "  JWT_SECRET          = (hidden)"
echo "  POSTGRES_PASSWORD   = (hidden)"
echo ""
echo "Next steps:"
echo "  1. docker-compose up -d"
echo "  2. Open http://$domain (or http://localhost:5173 for dev)"
echo ""
echo "For production (gethush.live / custom domain):"
echo "  - Set LIVEKIT_URL, LIVEKIT_API_KEY, LIVEKIT_API_SECRET from LiveKit Cloud"
echo "  - Use: docker-compose -f docker-compose.yml -f docker-compose.prod.yml up -d"
echo ""
