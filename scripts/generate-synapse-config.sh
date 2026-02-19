#!/bin/bash
set -euo pipefail

# Generate Synapse configuration from template
# This script generates signing keys, secrets, and creates homeserver.yaml

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
SYNAPSE_DIR="$PROJECT_ROOT/synapse"
TEMPLATE_FILE="$SYNAPSE_DIR/homeserver.yaml.template"
DATA_DIR="$SYNAPSE_DIR/data"
OUTPUT_FILE="$DATA_DIR/homeserver.yaml"

# Load .env if present (for SYNAPSE_REGISTRATION_SHARED_SECRET etc.)
if [ -f "$PROJECT_ROOT/.env" ]; then
  set -a
  # shellcheck source=/dev/null
  source "$PROJECT_ROOT/.env"
  set +a
fi

# Default values (can be overridden by environment variables)
MATRIX_SERVER_NAME="${MATRIX_SERVER_NAME:-localhost}"
MATRIX_PUBLIC_BASEURL="${MATRIX_PUBLIC_BASEURL:-http://localhost:8008}"
POSTGRES_USER="${POSTGRES_USER:-synapse}"
POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-synapse_password}"
POSTGRES_DB="${POSTGRES_DB:-synapse}"
REGISTRATION_SHARED_SECRET="${SYNAPSE_REGISTRATION_SHARED_SECRET:-changeme}"

echo "==================================="
echo "Synapse Configuration Generator"
echo "==================================="
echo ""
echo "Server name: $MATRIX_SERVER_NAME"
echo "Public URL: $MATRIX_PUBLIC_BASEURL"
echo ""

# Create data directory if it doesn't exist
mkdir -p "$DATA_DIR"
mkdir -p "$DATA_DIR/media_store"

# Copy log config to data directory
LOG_CONFIG_TEMPLATE="$SYNAPSE_DIR/MATRIX_SERVER_NAME.log.config"
LOG_CONFIG_FILE="$DATA_DIR/$MATRIX_SERVER_NAME.log.config"
if [ -f "$LOG_CONFIG_TEMPLATE" ] && [ ! -f "$LOG_CONFIG_FILE" ]; then
    cp "$LOG_CONFIG_TEMPLATE" "$LOG_CONFIG_FILE"
    echo "✓ Log config copied: $LOG_CONFIG_FILE"
fi

# Generate ed25519 signing key in Synapse format: "ed25519 a_<keyid> <base64_key>"
SIGNING_KEY_FILE="$DATA_DIR/$MATRIX_SERVER_NAME.signing.key"
if [ -f "$SIGNING_KEY_FILE" ]; then
    echo "✓ Signing key already exists: $SIGNING_KEY_FILE"
else
    echo "→ Generating ed25519 signing key..."
    KEY_ID=$(openssl rand -hex 4)
    KEY_DATA=$(openssl rand -base64 32 | tr -d '\n')
    echo "ed25519 a_${KEY_ID} ${KEY_DATA}" > "$SIGNING_KEY_FILE"
    chmod 600 "$SIGNING_KEY_FILE"
    echo "✓ Signing key generated: $SIGNING_KEY_FILE"
fi

# Generate macaroon secret
echo "→ Generating macaroon secret..."
MACAROON_SECRET=$(openssl rand -base64 32 | tr -d '\n=')

# Generate form secret
echo "→ Generating form secret..."
FORM_SECRET=$(openssl rand -base64 32 | tr -d '\n=')

# Replace placeholders in template
echo "→ Generating homeserver.yaml from template..."
sed -e "s|MATRIX_SERVER_NAME|$MATRIX_SERVER_NAME|g" \
    -e "s|MATRIX_PUBLIC_BASEURL|$MATRIX_PUBLIC_BASEURL|g" \
    -e "s|POSTGRES_USER|$POSTGRES_USER|g" \
    -e "s|POSTGRES_PASSWORD|$POSTGRES_PASSWORD|g" \
    -e "s|POSTGRES_DB|$POSTGRES_DB|g" \
    -e "s|MACAROON_SECRET|$MACAROON_SECRET|g" \
    -e "s|FORM_SECRET|$FORM_SECRET|g" \
    -e "s|REGISTRATION_SHARED_SECRET|$REGISTRATION_SHARED_SECRET|g" \
    "$TEMPLATE_FILE" > "$OUTPUT_FILE"

echo "✓ Configuration generated: $OUTPUT_FILE"
echo ""
echo "==================================="
echo "Next steps:"
echo "1. Review $OUTPUT_FILE"
echo "2. Start services: docker-compose up -d"
echo "3. Test Synapse: curl http://localhost:8008/_matrix/client/versions"
echo "==================================="
