# Operator Runbook

This document contains step-by-step procedures for self-hosters. It assumes
`./scripts/setup.sh` has already been run from the `hush-server` repository and
the stack is operational. `setup.sh` starts Docker Compose itself; do not follow
it with a second manual `docker compose up -d` unless you are intentionally
restarting services.

For security model details, see [SECURITY.md](../SECURITY.md).
For release and deployment paths, see [RELEASE.md](../RELEASE.md).

---

## Contents

1. [Secret Preservation Checklist](#1-secret-preservation-checklist)
2. [Backup Procedure](#2-backup-procedure)
3. [Restore Procedure](#3-restore-procedure)
4. [Rollback Procedure](#4-rollback-procedure)
5. [Reference: Secrets Classification](#5-reference-secrets-classification)
6. [Device-link Bulk Transfer (MinIO/S3)](#6-device-link-bulk-transfer-minios3)

---

## 1. Secret Preservation Checklist

Run this checklist immediately after `setup.sh` and after any secret rotation.

```
[ ] .env is backed up to a secure location separate from the database backup
    (password manager vault, offline encrypted storage, secrets manager)

[ ] TRANSPARENCY_LOG_PRIVATE_KEY is backed up separately and labeled as
    "NEVER ROTATE - transparency log signing key"

[ ] POSTGRES_PASSWORD is noted as "do not change without ALTER USER in postgres"

[ ] SERVICE_IDENTITY_MASTER_KEY is noted as "do not change without re-issuing
    service identity"

[ ] The backup location and access method is documented for any other operator
    who might need to perform a restore

[ ] Domain mode only: storage.<DOMAIN> has an A record pointing at this server
    if you want full device-link bulk transfer through bundled MinIO
```

**Why `.env` and database backup must be stored together (but separately from each other):**

A database backup restored without the matching `.env` is inoperable:
- `POSTGRES_PASSWORD` mismatch → postgres rejects all connections
- `TRANSPARENCY_LOG_PRIVATE_KEY` mismatch → all key-operation proofs are invalid
- `SERVICE_IDENTITY_MASTER_KEY` mismatch → service identity private key is unreadable

Store them together in concept (paired by date), but physically separate (different vault entries or different storage locations) so that compromise of one does not expose the other.

---

## 2. Backup Procedure

### Automatic backup (on upgrade)

`update.sh` creates a timestamped database backup in `backups/` before every upgrade. No action required.

### Manual backup (on demand)

Take a manual backup before any of the following:
- Risky configuration changes
- Manual database operations
- LiveKit version upgrades
- Any change to secrets in `.env`

```bash
cd ~/hush-server
./scripts/backup.sh
```

Output: `backups/hush-YYYYMMDD-HHMMSS.sql` and `backups/hush-YYYYMMDD-HHMMSS.meta`

### Backup metadata file

Every backup produces a `.meta` file alongside the `.sql` file with the same stem:

```
backups/hush-20260413-222209.sql
backups/hush-20260413-222209.meta
```

The `.meta` file records the state at backup time:

```
timestamp=2026-04-13T22:22:09Z
backup_file=hush-20260413-222209.sql
hush_server_git_sha=abc123...
hush_server_version=v1.2.0
livekit_version=v1.10.1
compose_files=docker-compose.prod.yml docker-compose.caddy.yml
env_continuity_required=POSTGRES_PASSWORD,TRANSPARENCY_LOG_PRIVATE_KEY,SERVICE_IDENTITY_MASTER_KEY
```

**Treat `.meta` files as read-only.** When identifying which backup to restore, read its `.meta` to confirm the version and check the `env_continuity_required` fields against your current `.env`. Do not delete `.meta` files independently — they are only useful paired with their `.sql`.

### What is backed up

| Data | Included | Notes |
|-|-|-|
| PostgreSQL database | Yes | Full dump with DROP/CREATE statements |
| Redis data | No | Ephemeral (session cache, rate-limiting counters reset safely) |
| MinIO `minio_data` volume | No | Short-lived device-link archive chunks only; back up separately if you need in-flight archive continuity |
| `.env` | No | Must be backed up separately |
| `livekit/livekit.yaml` | No | Template only; regenerated from `.env` on restart |
| TLS certificates | No | Managed by Caddy; re-obtained automatically from Let's Encrypt |

### Backup retention

Use `scripts/prune-backups.sh` to apply the retention policy:

```bash
# Dry-run: show what would be removed (no deletions)
./scripts/prune-backups.sh

# Apply deletions
./scripts/prune-backups.sh --apply

# Override keep count (default: 5)
./scripts/prune-backups.sh --keep 10 --apply
```

**Policy enforced by the script:**
- Keep the last 5 backups unconditionally
- Keep the most recent backup per calendar month for all older entries
- Remove `.sql` and `.meta` together

Run this manually when disk space is constrained, or after completing a stable upgrade. There is no automatic scheduled pruning.

### Verifying a backup (dev-machine safe)

To confirm a backup is readable SQL without restoring it:

```bash
head -50 backups/hush-YYYYMMDD-HHMMSS.sql
```

A valid backup starts with `--` comments and `DROP TABLE IF EXISTS` or `SET` statements. An empty or partial file indicates a failed backup.

---

## 3. Restore Procedure

### Before you begin

Answer all of these before running restore:

```
[ ] Do I have .env that matches this backup?
    (Same POSTGRES_PASSWORD, TRANSPARENCY_LOG_PRIVATE_KEY, SERVICE_IDENTITY_MASTER_KEY)

[ ] Have I identified the correct backup file?
    (Check the timestamp against when the problem was introduced)

[ ] Have I taken a current backup in case the restore itself needs to be undone?
    ./scripts/backup.sh

[ ] Is the postgres container running?
    docker compose -f docker-compose.prod.yml -f docker-compose.caddy.yml ps postgres
```

### Restore steps

```bash
cd ~/hush-server

# Verify available backups
ls -lh backups/

# Run the restore script
./scripts/restore.sh backups/hush-YYYYMMDD-HHMMSS.sql
```

The script will:
1. Confirm you want to proceed (type `YES`)
2. Stop `hush-api`
3. Terminate existing postgres connections to the hush database
4. Drop and recreate the `hush` database
5. Restore the backup
6. Leave `hush-api` stopped

### After restore

```bash
# Verify restored data manually if needed
docker compose -f docker-compose.prod.yml -f docker-compose.caddy.yml exec postgres \
  psql -U hush hush -c "SELECT COUNT(*) FROM users;"

# Restart the stack
docker compose -f docker-compose.prod.yml -f docker-compose.caddy.yml up -d

# Verify health
curl http://localhost:8080/api/health
curl http://localhost:8080/api/handshake
```

### Redis after restore

Redis does not need to be restored. After the stack restarts:
- Session cache is empty — users will re-authenticate transparently
- Rate-limiting counters reset — no impact on legitimate traffic
- No data is lost; Redis does not hold source-of-truth state

### What restore does NOT fix

| Problem | Restore fixes it? | What to do instead |
|-|-|-|
| Lost `.env` | No | Restore is impossible without matching `.env`. Prevent by backing up `.env`. |
| Rotated `TRANSPARENCY_LOG_PRIVATE_KEY` | No | Once rotated after log entries exist, historical proofs are permanently broken. Do not rotate. |
| Corrupted postgres volume | Yes | Restore creates a fresh database from the backup. |
| Failed migration applied | Yes | Restore reverts the schema to the pre-migration state. |

---

## 4. Rollback Procedure

Use this procedure when an upgrade produces a broken instance and you need to return to the previous version.

### Determine whether database restore is required

Migrations run forward at server startup and cannot be automatically reversed.

**Database restore is required if:** the broken upgrade applied new migrations to the database AND you intend to roll back to code that does not include those migrations.

To check whether migrations were applied:

```bash
docker compose -f docker-compose.prod.yml -f docker-compose.caddy.yml exec -T postgres \
  psql -U hush hush -c \
  "SELECT version, applied_at FROM schema_migrations ORDER BY applied_at DESC LIMIT 10;"
```

Compare the latest entry against `migrations/` in the target rollback tag. If the timestamp is after the upgrade started and the migration file does not exist at the rollback tag, a database restore is required.

### Path A: Source-build rollback

**Order matters. Check out old code BEFORE restoring the database.**

```bash
cd ~/hush-server

# Step 1: Check out the previous version
git fetch --tags
git checkout v<previous-version>

# Step 2: Restore the pre-upgrade backup (only if migrations were applied)
./scripts/restore.sh backups/hush-YYYYMMDD-HHMMSS.sql

# Step 3: Rebuild at the previous version and restart
docker compose -f docker-compose.prod.yml -f docker-compose.caddy.yml build hush-api
docker compose -f docker-compose.prod.yml -f docker-compose.caddy.yml up -d

# Step 4: Verify
curl http://localhost:8080/api/health
curl http://localhost:8080/api/handshake
```

### Path B: Pre-built GHCR image rollback

```bash
cd ~/hush-server

# Step 1: Check whether migrations were applied (see above)

# Step 2: Restore the pre-upgrade backup if needed
./scripts/restore.sh backups/hush-YYYYMMDD-HHMMSS.sql

# Step 3: Update docker-compose.override.yml to the previous tag
#   services:
#     hush-api:
#       image: ghcr.io/hushhq/hush-server:v<previous-version>
#       build: !reset null
$EDITOR docker-compose.override.yml

# Step 4: Pull the previous image and restart
docker compose -f docker-compose.prod.yml -f docker-compose.caddy.yml \
  -f docker-compose.override.yml pull hush-api
docker compose -f docker-compose.prod.yml -f docker-compose.caddy.yml \
  -f docker-compose.override.yml up -d

# Step 5: Verify
curl http://localhost:8080/api/health
curl http://localhost:8080/api/handshake
```

### Rollback checklist

```
[ ] Identified which backup to restore (timestamp matches pre-upgrade)

[ ] Checked whether new migrations were applied (required if rolling back code)

[ ] Checked out (or pinned) the previous codebase version BEFORE restoring DB

[ ] Restored the database backup (if migrations were applied)

[ ] Rebuilt / pulled the previous image

[ ] Verified /api/health returns 200

[ ] Verified /api/handshake returns valid JSON with transparency_url and
    log_public_key (confirms transparency log signing key is still active)
```

---

## 5. Reference: Secrets Classification

| Secret | Class | Notes |
|-|-|-|
| `TRANSPARENCY_LOG_PRIVATE_KEY` | **Permanent** — never rotate | Ed25519 seed for Merkle log. Rotation breaks all historical proofs. |
| `POSTGRES_PASSWORD` | **Permanent** — cannot change via .env alone | Must match volume-initialized password. Change requires `ALTER USER`. |
| `SERVICE_IDENTITY_MASTER_KEY` | **Permanent** — cannot rotate | Wraps service identity private key at rest. Rotation loses identity. |
| `JWT_SECRET` | **Rotatable** | Invalidates all active user sessions. Users re-authenticate automatically. |
| `LIVEKIT_API_KEY` | **Rotatable** | Terminates active voice rooms. Coordinated update (hush-api + livekit). |
| `LIVEKIT_API_SECRET` | **Rotatable** | Same as above. |
| `ADMIN_BOOTSTRAP_SECRET` | **One-time use** | No effect after first owner account is claimed. |

Full explanation in [SECURITY.md §6](../SECURITY.md#6-secrets-lifecycle).

---

## 6. Device-link Bulk Transfer (MinIO/S3)

The chunked archive plane that backs `/api/auth/link-archive-*` uses an
S3-compatible object store. Two modes are supported:

- **Self-host MinIO** — the bundled `minio` service in
  `docker-compose.prod.yml` (gated by the `s3-storage` profile).
  Domain-mode self-hosters get this wired automatically by `setup.sh`.
- **External S3 / R2 / etc.** — point `STORAGE_S3_*` at a managed
  bucket. The `minio` service stays off.

`STORAGE_BACKEND=postgres_bytea` keeps chunks in Postgres BYTEA
without any object storage. Acceptable for tiny self-host installs;
hits the same proxy body-size limits as the prior in-API path.

### 6.1 Self-host MinIO (domain mode, recommended)

`setup.sh --domain chat.example.com --email …` does the following
automatically:

- Generates `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` and writes them
  to `.env`.
- Sets `STORAGE_BACKEND=s3` plus the `STORAGE_S3_*` block pointing at
  `storage.<DOMAIN>` (TLS via Caddy + Let's Encrypt).
- Adds the `--profile s3-storage` flag so the `minio` and
  `minio-bootstrap` containers are brought up alongside the rest of
  the stack.
- The `minio-bootstrap` one-shot creates the `hush-link-archive`
  bucket for device-link archives and the `hush-attachments` bucket
  for chat attachments (both idempotent), then applies the bucket CORS
  policy that permits the browser client to PUT/GET ciphertext
  directly with the `x-amz-checksum-sha256` header.

**One operator step is still required:** create a DNS A record

```
storage.<DOMAIN>   A   <server public IP>
```

before clients can use the bulk plane. Caddy will obtain a Let's
Encrypt certificate for `storage.<DOMAIN>` on first request to it.

To verify the profile is active after setup:

```bash
docker compose -f docker-compose.prod.yml -f docker-compose.caddy.yml \
  --profile s3-storage ps minio minio-bootstrap
```

`minio` should be running. `minio-bootstrap` is a one-shot container and may
show as exited successfully after applying the bucket CORS policy.

### 6.2 External S3 / R2 (recommended for production)

Provision the bucket out-of-band:

1. Create two buckets. Recommended names: `hush-link-archive` for
   device-link archives and `hush-attachments` for chat attachments.
   Region: wherever your API is.
2. Create an IAM user / role with PUT, GET, HEAD, DELETE on that
   bucket pair.
3. Enable native SHA-256 object checksums (default on AWS S3 since
   2022; MinIO supports the same since RELEASE.2022-09-25).
4. Apply the CORS policy (replace origin):

   ```json
   {
     "CORSRules": [
       {
         "AllowedOrigin": ["https://app.gethush.live"],
         "AllowedMethod": ["GET", "PUT", "HEAD"],
         "AllowedHeader": [
           "Content-Type",
           "x-amz-checksum-sha256",
           "x-amz-content-sha256",
           "x-amz-date",
           "authorization"
         ],
         "ExposeHeader": ["ETag", "x-amz-checksum-sha256"],
         "MaxAgeSeconds": 3000
       }
     ]
   }
   ```

5. Set in `.env`:

   ```
   STORAGE_BACKEND=s3
   STORAGE_S3_ENDPOINT=s3.us-east-1.amazonaws.com
   STORAGE_S3_REGION=us-east-1
   STORAGE_S3_BUCKET=hush-link-archive
   STORAGE_S3_ACCESS_KEY=<from IAM>
   STORAGE_S3_SECRET_KEY=<from IAM>
   STORAGE_S3_USE_SSL=true

   ATTACHMENT_STORAGE_BACKEND=s3
   ATTACHMENT_STORAGE_S3_ENDPOINT=s3.us-east-1.amazonaws.com
   ATTACHMENT_STORAGE_S3_REGION=us-east-1
   ATTACHMENT_STORAGE_S3_BUCKET=hush-attachments
   ATTACHMENT_STORAGE_S3_ACCESS_KEY=<from IAM>
   ATTACHMENT_STORAGE_S3_SECRET_KEY=<from IAM>
   ATTACHMENT_STORAGE_S3_USE_SSL=true
   ```

6. Restart the API: `docker compose -f docker-compose.prod.yml -f
   docker-compose.caddy.yml up -d hush-api`. **Do not** pass
   `--profile s3-storage` — the bundled MinIO is not used.

### 6.3 IP-mode self-host caveat

The bundled MinIO is not wired in IP mode because there is no
browser-trusted hostname for `storage.<IP>`. `setup.sh --ip` falls
back to `STORAGE_BACKEND=postgres_bytea`. Operators who need the
full bulk plane in IP mode should:

- point `STORAGE_S3_*` at an external S3 bucket (above), or
- separately provision DNS + a Caddy block for a hostname they own
  and re-run `setup.sh --domain` against that hostname.

### 6.4 Verifying the bulk plane is live

After `setup.sh` completes, check:

```
docker logs hush-api 2>&1 | grep -i 'storage backend selected'
```

Expected:

```
link-archive: storage backend selected kind=s3
```

Then run the smoke test:

```
curl -fsS https://<DOMAIN>/api/handshake | jq .server_version
curl -fsS -X POST https://<DOMAIN>/api/auth/link-archive-init \
     -H "Authorization: Bearer <some valid jwt>" -H 'Content-Type: application/json' \
     -d '{"totalChunks":1,"totalBytes":16,"chunkSize":4194304,
          "manifestHash":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
          "archiveSha256":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}' | jq .backendKind
```

A `kind=s3` log line and `"backendKind":"s3"` from the init JSON
confirm the bulk plane is wired correctly.

### 6.5 Operational containment knobs

| Env var | Default | Purpose |
|-|-|-|
| `LINK_ARCHIVE_USER_QUOTA` | `1` | Concurrent active archives per user. |
| `LINK_ARCHIVE_STAGING_BYTES_CAP` | `8589934592` (8 GiB) | Sum of `total_bytes` across all non-terminal archives. New `link-archive-init` returns 503 once this is reached. |

The sliding 60-min expiry and 7-day hard deadline are constants in
the API binary; tiered-TTL GC reaps acknowledged/aborted archives
immediately, terminal_failure/imported (no-ack) after 24 h, and
active states once the sliding window lapses.

### 6.6 Rotating MinIO credentials

The bundled MinIO uses `MINIO_ROOT_USER` + `MINIO_ROOT_PASSWORD` and
the same values as `STORAGE_S3_ACCESS_KEY` + `STORAGE_S3_SECRET_KEY`.
To rotate:

1. Generate new values (`openssl rand -hex 24`).
2. Update all four env vars in `.env`.
3. Restart MinIO, the bootstrap one-shot, and the API:

   ```bash
   docker compose -f docker-compose.prod.yml -f docker-compose.caddy.yml \
     --profile s3-storage up -d minio minio-bootstrap hush-api
   ```

4. The bootstrap container is idempotent on re-run; it will re-apply
   CORS but will not re-create the bucket if it already exists.

### 6.7 Backup considerations

`minio_data` is a Docker named volume; back it up the same way you
back up `postgres_data`. The bucket holds short-lived chunk
ciphertext only (purger cleans archives within hours of completion);
losing it during a backup window does not lose long-term user data,
but does abort any in-flight archive transfers.
