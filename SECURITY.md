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

The Caddy reverse proxy sets the following headers on all responses:

| Header | Value | Purpose |
|-|-|-|
| `X-Content-Type-Options` | `nosniff` | Disable MIME sniffing |
| `X-Frame-Options` | `DENY` | Mitigate clickjacking |
| `Cross-Origin-Opener-Policy` | `same-origin` | Window isolation |
| `Strict-Transport-Security` | `max-age=31536000` | HSTS (production with `--domain`) |

**Note on COEP:** `Cross-Origin-Embedder-Policy: require-corp` is intentionally not set. The LiveKit E2EE layer uses Insertable Streams with Transferable objects, not SharedArrayBuffer. Enabling COEP would break browser extensions and cross-origin resources without benefit.

---

## 5. WebSocket Security

- WebSocket upgrade is gated by a valid JWT session token.
- Each WebSocket connection is subscribed to only the guilds the authenticated user is a member of; this is enforced at upgrade time via a membership check.
- The `BroadcastToServer(serverID, msg)` hub call fans out only to connections subscribed to that specific guild. Cross-guild data leakage is structurally prevented.

---

## 6. Key Transparency

Hush implements a signed Merkle tree of key operations per instance.

- All key operations (registration, device add, device revoke, KeyPackage rotation) are recorded in the log.
- Leaf nodes are signed with an Ed25519 key seeded from `TRANSPARENCY_LOG_PRIVATE_KEY`.
- Clients verify inclusion proofs at login and on key changes via `GET /api/transparency/verify`.

**Operational note:** `TRANSPARENCY_LOG_PRIVATE_KEY` must never change after the first log entry. Rotating it invalidates all existing proofs. Back it up separately from other `.env` values.

---

## 7. Production Hardening Checklist

| Item | Action |
|-|-|
| **CORS** | Set `CORS_ORIGIN` to your frontend origin. Never use `*` in production. |
| **HSTS** | Use `--domain` with `setup.sh`; Caddy sets HSTS automatically. |
| **Secrets** | Do not use default values. `setup.sh` generates all secrets. |
| **Transparency key** | Never rotate `TRANSPARENCY_LOG_PRIVATE_KEY` after first log entry. Back it up. |
| **Database access** | PostgreSQL should not be exposed to the public internet. Use Docker networking or firewall rules. |
| **Redis access** | Same as PostgreSQL - internal network only. |
| **Dependencies** | Run `go mod verify` and check for CVEs before production deployment. |

---

## 8. Responsible Disclosure

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
