# Security

This document describes the server's security surface, threat model, and responsible disclosure policy. It is written for security auditors, penetration testers, and self-hosters who want to understand what the server stores, what it cannot read, and how to harden a deployment.

For the full cryptographic design (MLS, BIP39, key transparency), see the client-side documentation at `gethush.live`.

---

## 1. Threat Model: Server as Blind Relay

The Hush server is designed to be a blind relay. It routes and stores encrypted data without access to plaintext. A fully compromised server database exposes the following:

### What the server stores

| Data | Format | Purpose |
|-|-|-|
| Chat messages | MLS ciphertext (BYTEA) | Storage and routing to channel subscribers |
| Guild/channel metadata | AES-256-GCM ciphertext (BYTEA) | Stored as `encrypted_metadata`; opaque to server |
| Sender UUID | UUID | Message routing |
| Timestamp | UTC timestamp | Message ordering |
| Channel UUID | UUID | Routing to correct MLS group |
| MLS KeyPackages | Public key material (BYTEA) | Used by other clients to add group members |
| MLS Commits, Proposals, Welcome messages | MLS protocol messages | Distributed to group members |
| Permission levels | Integer 0–3 | Guild-level role enforcement |
| Access policy | Enum string (open/invite-only) | Join flow control |
| Member count | Integer | Rate limiting and metrics |
| User root public key | Ed25519 public key | Authentication verification |

### What the server never sees

| Data | Why |
|-|-|
| Plaintext message content | Encrypted with MLS before transmission |
| Guild names | Encrypted with MLS-derived AES-256-GCM before transmission |
| Channel names | Encrypted with MLS-derived AES-256-GCM before transmission |
| Role labels | Exist only in encrypted guild metadata |
| Private keys or mnemonics | Never transmitted; client-generated and client-held |
| Voice/video frame content | LiveKit SFU forwards encrypted frames; frame keys are never sent to the server |

### Admin dashboard

The admin dashboard uses local instance-admin accounts authenticated by secure session cookies. It sees only opaque data: UUIDs, member counts, message counts, timestamps. It cannot read guild names, channel names, or message content.

---

## 2. Rate Limiting

| Endpoint | Limit |
|-|-|
| `POST /api/auth/*` (registration, challenge) | 5 req/min per IP |
| WebSocket message send | 30 msg/min per user |
| `POST /api/mls/key-packages` | 10 req/min per user |
| General API | 100 req/min per IP |

Rate limiting is implemented server-side using Redis counters. Exceeding limits returns HTTP 429.

---

## 3. Input Validation

API request bodies are validated server-side in the Go backend before any database operation.

| Field | Rule |
|-|-|
| `username` | Non-empty string, max 64 chars |
| `publicKey` | Valid Ed25519 public key (32 bytes) |
| `encrypted_metadata` | Accepted as opaque BYTEA; server never parses contents |
| `permission_level` | Integer 0–3 |
| Admin session cookie | Required for `/api/admin/*` after bootstrap/login; stored as `HttpOnly` and validated server-side |

Chat messages are stored as MLS ciphertext blobs. The server never processes plaintext content.

---

## 4. Security Headers

The default Caddy self-host proxy terminates TLS and routes API, WebSocket,
LiveKit, admin-dashboard, and storage traffic. The backend-only self-host path
does not serve the Hush web client, so browser application headers belong to the
client origin you use (`https://app.gethush.live` by default, or your own
`hush-web` deployment).

For direct browser visits to the instance root, the generated Caddy fallback
responds with:

| Header | Value | Purpose |
|-|-|-|
| `X-Content-Type-Options` | `nosniff` | Disable MIME sniffing |
| `X-Frame-Options` | `DENY` | Mitigate clickjacking |
| `Cross-Origin-Opener-Policy` | `same-origin` | Window isolation |
| `Strict-Transport-Security` | `max-age=31536000` | HSTS (production with `--domain`) |

If you self-host `hush-web` on your own domain, configure equivalent browser
security headers on that frontend origin. Do not add COEP unless you have tested
the full browser stack you serve.

**Note on COEP:** `Cross-Origin-Embedder-Policy: require-corp` is intentionally not set. The LiveKit E2EE layer uses Insertable Streams with Transferable objects, not SharedArrayBuffer. Enabling COEP would break browser extensions and cross-origin resources without benefit.

---

## 5. WebSocket Security

- WebSocket upgrade is gated by a valid JWT session token.
- Each WebSocket connection is subscribed to only the guilds the authenticated user is a member of; this is enforced at upgrade time via a membership check.
- The `BroadcastToServer(serverID, msg)` hub call fans out only to connections subscribed to that specific guild. Cross-guild data leakage is structurally prevented.

---

## 6. Secrets Lifecycle

Not all secrets in `.env` have the same rotation safety. This table defines the operational classification:

| Secret | Rotation safety | Effect of rotation | Backup priority |
|-|-|-|-|
| `TRANSPARENCY_LOG_PRIVATE_KEY` | **Never rotate** after first log entry | Permanently invalidates all existing key-operation proofs for all users | CRITICAL — store separately from other secrets |
| `POSTGRES_PASSWORD` | **Cannot rotate** without coordinated DB password change | Breaks all DB connections until postgres user password is updated to match | CRITICAL |
| `SERVICE_IDENTITY_MASTER_KEY` | **Cannot rotate** without re-issuing service identity | Existing wrapped private key is unreadable; instance service identity is lost | CRITICAL |
| `JWT_SECRET` | Rotatable — invalidates active sessions | All users are logged out on next request; re-authentication works normally | Important |
| `LIVEKIT_API_KEY` / `LIVEKIT_API_SECRET` | Rotatable — requires coordinated restart | In-progress voice rooms are terminated; new rooms work immediately after restart | Important |
| `ADMIN_BOOTSTRAP_SECRET` | Rotatable after first owner account is created | Once bootstrap is claimed, this secret is no longer used | Low (one-time use) |

### Rules

1. **Back up `.env` immediately after `setup.sh`** and after any rotation. Store it in a location separate from the database backup. A database backup without its corresponding `.env` is inoperable.

2. **`TRANSPARENCY_LOG_PRIVATE_KEY` is a cryptographic commitment.** The server signs every Merkle leaf with the Ed25519 key derived from this seed. Once users have verified proofs against the public key published via `/api/handshake`, changing the seed breaks all existing proofs. Treat it like a long-lived signing certificate: generate once, preserve forever, back up offline.

3. **`SERVICE_IDENTITY_MASTER_KEY` protects the instance's Ed25519 private key at rest.** The wrapped private key is stored in the `instance_service_identity` table. If this key is rotated without also re-wrapping the stored private key, the instance service identity is permanently lost.

4. **`POSTGRES_PASSWORD` cannot be changed by editing `.env` alone.** The password is baked into the postgres data volume at initialization. Changing `.env` without also running `ALTER USER hush PASSWORD '...'` in postgres will break database connectivity on next restart.

### Secret rotation procedure

If a rotatable secret (`JWT_SECRET`, LiveKit credentials) needs to change:

```bash
# 1. Take a backup before any change
./scripts/backup.sh

# 2. Edit .env with the new value
$EDITOR .env

# 3. Restart the affected service
docker compose -f docker-compose.prod.yml -f docker-compose.caddy.yml up -d hush-api

# 4. Verify health
curl http://localhost:8080/api/health
```

---

## 8. Key Transparency

Hush implements a signed Merkle tree of key operations per instance.

- All key operations (registration, device add, device revoke, KeyPackage rotation) are recorded in the log.
- Leaf nodes are signed with an Ed25519 key seeded from `TRANSPARENCY_LOG_PRIVATE_KEY`.
- Clients verify inclusion proofs at login and on key changes via `GET /api/transparency/verify`.

**Operational note:** `TRANSPARENCY_LOG_PRIVATE_KEY` must never change after the first log entry. Rotating it invalidates all existing proofs. Back it up separately from other `.env` values. See §6 (Secrets Lifecycle) for the full rotation classification.

---

## 9. Production Hardening Checklist

| Item | Action |
|-|-|
| **CORS** | Set `CORS_ORIGIN` to your frontend origin. Never use `*` in production. |
| **HSTS** | Use `--domain` with `setup.sh`. If you serve `hush-web` yourself, configure HSTS on that frontend origin too. |
| **Secrets** | Do not use default values. `setup.sh` generates all secrets. See §6 for rotation classification. |
| **Transparency key** | Never rotate `TRANSPARENCY_LOG_PRIVATE_KEY` after first log entry. Back it up offline and separately from the database backup. |
| **`.env` backup** | Back up `.env` immediately after `setup.sh`. A database backup without its matching `.env` is inoperable. |
| **Database access** | PostgreSQL should not be exposed to the public internet. Use Docker networking or firewall rules. |
| **Redis access** | Same as PostgreSQL — internal network only. |
| **Device-link storage** | In domain mode, create `storage.<DOMAIN>` DNS before relying on the bundled MinIO bulk plane. If you use external S3/R2, apply the CORS policy from `docs/RUNBOOK.md`. |
| **Dependencies** | Run `go mod verify` and check for CVEs before production deployment. |
| **Restore tested** | Verify restore procedure on a non-production copy before you need it. See `docs/RUNBOOK.md`. |

---

## 10. Responsible Disclosure

If you discover a security vulnerability in Hush Server, please report it responsibly.

**Email:** `security@gethush.live`

**Scope:** All code in this repository and the hosted instance at `gethush.live`.

**Response timeline:**
- Acknowledge within 48 hours
- Triage and severity assessment within 7 days
- Fix timeline communicated within 14 days

**Out of scope:**
- Vulnerabilities requiring physical access to a server
- Denial-of-service attacks that require volumetric traffic (no reasonable defense applies)

**Guidelines:**
- Do not access or modify data that is not yours
- Do not disclose publicly until a fix is available (coordinated disclosure)
- Provide sufficient detail to reproduce the issue
- We will credit researchers who report valid vulnerabilities (unless you prefer anonymity)
