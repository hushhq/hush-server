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
| Attachment records | UUIDs, channel UUID, owner UUID, storage key, ciphertext size, content type, timestamps | Presigned upload/download and cleanup |
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
| Attachment plaintext bytes | Encrypted client-side before upload to object storage |
| Attachment encryption keys or IVs | Stored only inside the MLS-encrypted message envelope |
| Original attachment filenames | Stored only inside the MLS-encrypted message envelope |
| Role labels | Exist only in encrypted guild metadata |
| Private keys or mnemonics | Never transmitted; client-generated and client-held |
| Voice/video frame content | LiveKit SFU forwards encrypted frames; frame keys are never sent to the server |

### Admin dashboard

The admin dashboard uses local instance-admin accounts authenticated by secure session cookies. It sees only opaque data: UUIDs, member counts, message counts, timestamps. It cannot read guild names, channel names, or message content.

---

## 2. Chat and Attachment Encryption

Chat messages and attachments use the same end-to-end trust boundary, but they are not encrypted in the same physical place.

In plain terms: a chat message is small enough to go directly through the MLS group. The client turns the message into MLS ciphertext, and the server stores only that ciphertext. An attachment can be much larger, so the client encrypts the file separately, uploads the encrypted bytes to storage, and sends only a small encrypted reference through the MLS group. That reference contains the attachment id, display filename, MIME type, and the one-time AES key and IV needed to open the file.

This creates two planes:

| Plane | What is encrypted | Where ciphertext is stored | Who has the key |
|-|-|-|-|
| Chat control plane | Message envelope JSON | PostgreSQL `messages.ciphertext` | Current MLS group members |
| Attachment data plane | File bytes | Attachment storage backend, usually MinIO/S3 | MLS group members who can decrypt the message envelope |

### Attachment flow

1. The client generates a fresh AES-GCM-256 key and 96-bit IV for each file.
2. The client encrypts the whole file locally. The storage backend receives only ciphertext bytes.
3. The client asks the server for a short-lived presigned upload URL. The server validates channel membership, ciphertext size, and declared content type, then records an attachment row with a storage key.
4. The client uploads the ciphertext directly to the configured storage backend.
5. The client places an `AttachmentRef` in the message envelope. The `AttachmentRef` includes the attachment id, filename for display, ciphertext size, MIME type, AES key, and IV.
6. The message envelope is encrypted as an MLS application message and stored by the server as opaque ciphertext.
7. A recipient decrypts the MLS message, reads the `AttachmentRef`, requests a presigned download URL, downloads ciphertext, and decrypts locally with AES-GCM.

The server can authorize and route attachment access, but it cannot decrypt attachment contents because it never receives the AES key or IV.

### Forward secrecy boundary

Attachment keys are independent random per-file keys. They are not derived from the MLS epoch exporter. MLS protects the delivery of the `AttachmentRef`, but once a client has decrypted and cached the message envelope, that client may retain the attachment key according to the local client persistence policy.

This means:

- MLS epoch rotation does not rotate existing attachment keys.
- Removing a member prevents access to future MLS messages, but it does not revoke attachment keys already delivered to that member.
- Server or object-storage compromise exposes attachment ciphertext and metadata, not plaintext.
- Endpoint compromise can expose any plaintext or attachment keys already decrypted or cached on that endpoint.

Hush does use MLS `export_secret` for other epoch-bound domains such as voice frame keys and encrypted metadata. Attachments intentionally use the separate per-file-key model described above. A future format could wrap per-file keys with an MLS-exported key encryption key, but that is not the current attachment format.

### Storage integrity and cleanup

AES-GCM authenticates attachment ciphertext during local decryption. If the stored object is tampered with or the wrong key/IV is used, decryption fails instead of producing modified plaintext.

Attachment uploads currently do not have a server-side post-upload confirmation step. The link-device archive flow has an S3 checksum confirmation endpoint; chat attachments do not. Attachment deletion is best-effort at the storage backend after the database row is soft-deleted. If backend deletion fails, the row remains hidden from users and the object may require later operator cleanup.

See [docs/ATTACHMENTS.md](docs/ATTACHMENTS.md) for the full attachment security design.

---

## 3. Rate Limiting

| Endpoint | Limit |
|-|-|
| `POST /api/auth/*` (registration, challenge) | 5 req/min per IP |
| WebSocket message send | 30 msg/min per user |
| `POST /api/mls/key-packages` | 10 req/min per user |
| General API | 100 req/min per IP |

Rate limiting is implemented server-side using Redis counters. Exceeding limits returns HTTP 429.

---

## 4. Input Validation

API request bodies are validated server-side in the Go backend before any database operation.

| Field | Rule |
|-|-|
| `username` | Non-empty string, max 64 chars |
| `publicKey` | Valid Ed25519 public key (32 bytes) |
| `encrypted_metadata` | Accepted as opaque BYTEA; server never parses contents |
| Attachment size | Positive ciphertext size, capped at 25 MiB |
| Attachment content type | Declared type must match the server allowlist; advisory only because bytes are encrypted |
| `permission_level` | Integer 0–3 |
| Admin session cookie | Required for `/api/admin/*` after bootstrap/login; stored as `HttpOnly` and validated server-side |

Chat messages are stored as MLS ciphertext blobs. The server never processes plaintext content.

---

## 5. Security Headers

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

## 6. WebSocket Security

- WebSocket upgrade is gated by a valid JWT session token.
- Each WebSocket connection is subscribed to only the guilds the authenticated user is a member of; this is enforced at upgrade time via a membership check.
- The `BroadcastToServer(serverID, msg)` hub call fans out only to connections subscribed to that specific guild. Cross-guild data leakage is structurally prevented.

---

## 7. Secrets Lifecycle

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

**Operational note:** `TRANSPARENCY_LOG_PRIVATE_KEY` must never change after the first log entry. Rotating it invalidates all existing proofs. Back it up separately from other `.env` values. See §7 (Secrets Lifecycle) for the full rotation classification.

---

## 9. Production Hardening Checklist

| Item | Action |
|-|-|
| **CORS** | Set `CORS_ORIGIN` to your frontend origin. Never use `*` in production. |
| **HSTS** | Use `--domain` with `setup.sh`. If you serve `hush-web` yourself, configure HSTS on that frontend origin too. |
| **Secrets** | Do not use default values. `setup.sh` generates all secrets. See §7 for rotation classification. |
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
