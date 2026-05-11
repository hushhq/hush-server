![Build](https://img.shields.io/badge/build-passing-brightgreen)
![License](https://img.shields.io/badge/license-AGPL--3.0-blue)
![Go](https://img.shields.io/badge/go-1.25+-00ADD8)

# hush-server

Go backend for [Hush](https://gethush.live) - an end-to-end encrypted communication platform. The server is a blind relay: it routes and stores ciphertext it cannot read. All encryption happens in the client.

---

## Quick Start (Self-Hosting)

**Prerequisites:**
- Linux server (Ubuntu 22.04+ recommended, 1 GB RAM minimum)
- [Docker](https://docs.docker.com/engine/install/) with Docker Compose
- Ports 80, 443, 7880-7881/tcp, and 50020-50100/udp open

`hush-server` self-hosting provisions the backend/media plane only: API,
PostgreSQL, Redis, LiveKit, and the default Caddy reverse proxy. In domain
mode it also wires bundled MinIO for device-link bulk transfer. It does **not**
clone or build `hush-web`.

### With a domain (recommended)

A domain gives you a real TLS certificate from Let's Encrypt - no browser warnings, and no friction when users from other instances connect to yours.

**1. Point the production hostnames at your server** (A records in your DNS provider):

```
chat.example.com          ->  YOUR_SERVER_IP
rtc.example.com           ->  YOUR_SERVER_IP
storage.chat.example.com  ->  YOUR_SERVER_IP
```

**2. Clone and run setup:**

```bash
git clone https://github.com/hushhq/hush-server
cd hush-server
./scripts/setup.sh \
  --domain chat.example.com \
  --rtc-domain rtc.example.com \
  --email you@example.com
```

`setup.sh` writes `.env`, generates the Caddy and LiveKit configs, builds the
API image, pulls runtime images, starts Docker Compose, and health-checks the
instance. Do not run a separate `docker compose up -d` after it.

The RTC domain is the public LiveKit signaling endpoint. It is part of the
production deployment contract, not an optional troubleshooting path. If you
put the app domain behind Cloudflare, keep the RTC domain DNS-only so Safari
with iCloud Private Relay can establish LiveKit WebSocket signaling reliably.

**3. Connect with a Hush web client**

The default setup is designed to work with the official hosted client:

1. Open `https://app.gethush.live`
2. Add your instance URL: `https://chat.example.com`
3. Register or sign in against that instance

If you later self-host `hush-web`, update `CORS_ORIGIN` in `.env` to your own web-client origin.

Expected final output:

```text
[hush] ================================================================
[hush]  Hush is live at https://chat.example.com
[hush] ================================================================

[hush] Open https://app.gethush.live and add this instance URL:
[hush]   https://chat.example.com

[hush] Admin dashboard:          https://chat.example.com/admin/
[hush] Admin bootstrap secret:   <generated-secret>
```

### Admin dashboard

The admin dashboard is embedded in the Go binary and served at `/admin/`. After setup, visit `https://YOUR_DOMAIN/admin/` to bootstrap the first admin account using the secret printed during setup.

### With just an IP (development / LAN only)

```bash
git clone https://github.com/hushhq/hush-server
cd hush-server
./scripts/setup.sh --ip 203.0.113.42
```

After setup, visit `https://YOUR_SERVER_IP` once to accept the browser certificate warning. IP-only mode is for development / LAN testing; for normal use, prefer a domain with real TLS.

### Setup flags

| Flag | Purpose |
|-|-|
| `--domain <host>` | Public hostname (Let's Encrypt TLS) |
| `--rtc-domain <host>` | Public LiveKit signaling hostname (Let's Encrypt TLS) |
| `--ip <address>` | Server IP (self-signed TLS, no domain needed) |
| `--email <email>` | Let's Encrypt renewal (required with `--domain`) |
| `--force` | Re-run on an already-configured instance (overwrites config, preserves data) |

### What `setup.sh` does

1. Checks for Docker and docker-compose
2. Generates all required operational secrets: JWT signing key, admin bootstrap secret, PostgreSQL password, LiveKit credentials, key transparency seed, MinIO credentials (domain mode), and a wrapping key for the instance service identity
3. Writes `.env`, the active Caddy config, and verifies the LiveKit config template
4. Builds `hush-api` and pulls the runtime images
5. Starts the backend/media stack: Go API, PostgreSQL, Redis, LiveKit, Caddy, and MinIO when domain-mode storage is enabled
6. Health-checks `/api/health` and `/api/handshake`
7. Prints your instance URL, admin dashboard URL, admin bootstrap secret, storage-mode status, and `.env` backup warning

### Updating

```bash
./scripts/update.sh
```

Backs up the database, pulls the latest code, rebuilds images, and restarts.

### Manual backup

```bash
./scripts/backup.sh
```

Creates a timestamped snapshot in `backups/`. The database backup is incomplete without a matching `.env` — back up `.env` separately to a secure location.

### Restore and rollback

See [docs/RUNBOOK.md](docs/RUNBOOK.md) for:
- Restore procedure (with preconditions and ordering)
- Rollback procedure (Path A source-build and Path B GHCR image)
- Secrets lifecycle and rotation classification

---

## Manual Setup (Without Docker)

**Prerequisites:** Go 1.25+, Node.js 22+ (for admin dashboard build), PostgreSQL 16+, Redis 7+

```bash
# 1. Clone
git clone https://github.com/hushhq/hush-server
cd hush-server

# 2. Copy and fill in environment variables
cp .env.example .env
$EDITOR .env

# 3. Build admin dashboard (embedded in Go binary)
cd admin && npm ci && npm run build && cd ..

# 4. Run database migrations
go run github.com/golang-migrate/migrate/v4/cmd/migrate@latest \
  -database "$DATABASE_URL" \
  -path ./migrations up

# 5. Build and run
go build -o hush ./cmd/hush
./hush
```

---

## Configuration

The server reads configuration from environment variables (or `.env` in the project root). Core variables:

| Variable | Description |
|-|-|
| `PRODUCTION` | Set to `true`/`1` in production; requires a persistent transparency key |
| `HOST` | HTTP bind host |
| `PORT` | HTTP listen port |
| `DATABASE_URL` | PostgreSQL connection string |
| `JWT_SECRET` | Random secret for JWT signing (min 32 bytes) |
| `JWT_EXPIRY_HOURS` | Session token lifetime in hours |
| `ADMIN_BOOTSTRAP_SECRET` | One-time secret used only to create the first local admin owner |
| `ADMIN_SESSION_TTL_HOURS` | Dashboard session lifetime in hours |
| `DOMAIN` | Public instance hostname; also used to derive `CORS_ORIGIN` when omitted |
| `RTC_DOMAIN` | Public LiveKit signaling hostname |
| `CORS_ORIGIN` | Allowed frontend origin. For backend-only self-hosting with the official client, use `https://app.gethush.live`. Do not use `*` in production. |
| `WS_ALLOWED_ORIGINS` | Optional comma-separated extra WebSocket origins for trusted desktop shells, for example `app://localhost`. |
| `SERVICE_IDENTITY_MASTER_KEY` | 32-byte hex/base64 key used to wrap the instance service identity private key at rest |
| `LIVEKIT_API_KEY` | LiveKit API key |
| `LIVEKIT_API_SECRET` | LiveKit API secret |
| `LIVEKIT_URL` | Internal LiveKit URL used by hush-api for RoomService calls |
| `LIVEKIT_PUBLIC_URL` | Public browser LiveKit signaling URL returned by `/api/livekit/token` |
| `TRANSPARENCY_LOG_PRIVATE_KEY` | Hex-encoded 32-byte Ed25519 seed for key transparency log signing; never change after first log entry |
| `STORAGE_BACKEND` | `s3` in domain mode with bundled MinIO; `postgres_bytea` in IP mode unless you configure external S3 |
| `ATTACHMENT_STORAGE_*` | Optional chat-attachment storage override. `setup.sh` points this at bucket `hush-attachments` so attachments do not mix with link-device archives. |
| `STORAGE_BROWSER_ORIGIN` | Browser origin allowed by the storage bucket CORS policy. Defaults to `CORS_ORIGIN` |
| `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` | Bundled MinIO credentials generated by `setup.sh` in domain mode |

See `.env.example` for the full list with defaults and descriptions.

### Key transparency operational note

When `TRANSPARENCY_LOG_PRIVATE_KEY` is configured, the server:

- enables `/api/transparency/*`
- publishes `transparency_url` and `log_public_key` via `GET /api/handshake`
- signs the append-only Merkle log used for key-operation verification

Treat `TRANSPARENCY_LOG_PRIVATE_KEY` like a long-lived signing secret:

- generate it once
- back it up securely
- never rotate it after the log has entries unless you intentionally want to break historical verification

---

## API Overview

All endpoints are prefixed with `/api`. Authentication uses `Authorization: Bearer <jwt>`.

| Group | Description |
|-|-|
| `POST /api/auth/register` | Register with Ed25519 public key and BIP39 mnemonic credential |
| `POST /api/auth/challenge` | Request nonce for challenge-response authentication |
| `POST /api/auth/authenticate` | Sign nonce, receive JWT session token |
| `GET/POST /api/guilds` | List and create guilds (servers) |
| `POST /api/guilds/:id/join` | Join a guild |
| `GET/POST /api/guilds/:id/channels` | List and create channels |
| `GET /api/messages/:channel_id` | Fetch message history (ciphertext) |
| `POST /api/mls/key-packages` | Upload MLS KeyPackages |
| `GET /api/mls/key-packages/:user_id` | Fetch a KeyPackage for a user |
| `POST /api/mls/commit` | Deliver MLS commit to group members |
| `POST /api/transparency/append` | Append entry to key transparency log |
| `GET /api/transparency/verify` | Verify inclusion proof |
| `POST /api/admin/bootstrap/status` | Report whether first-owner bootstrap is still available |
| `POST /api/admin/bootstrap/claim` | Create the first local admin owner using the bootstrap secret |
| `POST /api/admin/session/login` | Log in to the admin dashboard and receive a secure session cookie |
| `POST /api/admin/session/logout` | Invalidate the current admin session |
| `GET /api/admin/session/me` | Return the authenticated local admin session identity |
| `GET /api/admin/*` | Admin dashboard endpoints (requires local admin session cookie) |

WebSocket endpoint: `GET /ws` - real-time message delivery, presence, MLS group operations.

### Admin control plane

Instance administration is now a separate local control plane:

- Hush users authenticate with cryptographic challenge-response and JWT sessions
- Instance admins authenticate with local username/password accounts plus `HttpOnly` session cookies
- The first `owner` account is created once through `ADMIN_BOOTSTRAP_SECRET`
- A non-discoverable instance service identity is provisioned and stored separately from human admin accounts

Normal admin traffic no longer uses `X-Admin-Key`.

For full API documentation including request/response schemas, see `ARCHITECTURE.md`.

---

## Docker Image

Versioned images are published to GitHub Container Registry on every tagged release:

```bash
docker pull ghcr.io/hushhq/hush-server:v1.0.0
```

There is no `:latest` tag. Pin the exact version you intend to run.

**Note:** `docker-compose.prod.yml` builds the image locally from source (`build: context: .`). The pre-built GHCR images are for custom deployment setups that prefer a pre-built artifact and are not used by the default `setup.sh` / `update.sh` self-host path.

---

## Reverse Proxy

### Caddy (default)

`scripts/setup.sh` uses `docker-compose.prod.yml` plus `docker-compose.caddy.yml` and writes the active config to `caddy/Caddyfile.self-hoster`.

The `caddy/` directory contains the templates used for that path:
- `caddy/Caddyfile` - development/local
- `caddy/Caddyfile.self-hoster.tmpl` - production template (replace `__DOMAIN__` and `__EMAIL__`)
- `caddy/Caddyfile.ip.tmpl` - IP-only template with Caddy internal CA

In domain mode, the generated Caddyfile serves two hostnames:
- `https://<DOMAIN>` for API, WebSocket, legacy `/livekit/`, and `/admin/`
- `https://<RTC_DOMAIN>` for production LiveKit signaling
- `https://storage.<DOMAIN>` for bundled MinIO presigned upload/download URLs

### nginx

If you already run nginx, use `docker-compose.prod.yml` for the backend/media services and copy `nginx/hush.conf` to `/etc/nginx/sites-available/`, replace `YOUR_DOMAIN` and `YOUR_RTC_DOMAIN`, obtain certificates for both names, and reload. The config proxies API, WebSocket, and LiveKit signaling.

The admin dashboard is embedded in the Go binary and proxied at `/admin/` by both Caddy and nginx configs. Neither bundles `hush-web`; if you want the browser client on your own domain, deploy `hush-web` separately.

See the Self-Hosting Guide in `ARCHITECTURE.md` for full instructions.

---

## Development

### Prerequisites

- Go 1.25+
- PostgreSQL 16+
- Redis 7+

### Setup

```bash
# Start dependencies
docker-compose up -d postgres redis livekit

# Run server in development mode
go run ./cmd/hush
```

### Running tests

```bash
go test ./...

# With race detector
go test -race ./...

# Specific package
go test ./internal/api/...
```

### Project structure

```
admin/                           # Admin dashboard SPA (React/Vite, embedded via go:embed)
cmd/
└── hush/main.go                 # Entry point, Chi router, graceful shutdown
internal/
├── api/                         # HTTP handlers (auth, guilds, channels, MLS, admin)
├── config/                      # Environment-based configuration
├── db/                          # PostgreSQL queries (store interface for DI)
├── models/                      # Shared data types
├── transparency/                # Key transparency service and Merkle tree
└── ws/                          # WebSocket hub, client relay, message routing

migrations/                      # Sequential SQL migration files (golang-migrate)
scripts/
├── setup.sh                     # First-run self-hoster setup
└── update.sh                    # Upgrade script
caddy/                           # Reverse proxy configs
nginx/                           # nginx config template
```

---

## Security

The server is designed so a compromised database or backup cannot read messages or private guild metadata without client-held keys. See [SECURITY.md](SECURITY.md) for:

- Threat model (blind relay model, what the server stores vs. never sees)
- Key Transparency guarantees
- Rate limiting
- Security headers
- Responsible disclosure

**Responsible disclosure:** `security@gethush.live`

---

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

---

## License

[AGPL-3.0](LICENSE). If you modify and deploy this server, you must share your changes under the same license.
