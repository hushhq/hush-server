# Release and Deployment

This document defines the canonical release process and deployment paths for `hush-server`.

---

## Release tagging convention

All releases are tagged `vMAJOR.MINOR.PATCH` on `main`.

```
v1.0.0   — stable release
v1.1.0   — feature release
v1.1.1   — patch release
```

Pushing a `v*` tag triggers the CI `docker` job, which builds the image and publishes it to GHCR tagged with the exact version. There is no `:latest` tag.

| Tag example | Image published |
|-|-|
| `v1.1.0` | `ghcr.io/hushhq/hush-server:v1.1.0` |
| `v1.1.0-rc1` | `ghcr.io/hushhq/hush-server:v1.1.0-rc1` |
| `v1.1.0-beta.2` | `ghcr.io/hushhq/hush-server:v1.1.0-beta.2` |

Deployers consuming GHCR images must pin the exact tag they intend to run. No floating tag exists.

**Only tag from a tested, passing `main` commit.**

---

## Deployment paths

There are two deployment paths. They are independent — choose one per instance.

### Path A: Self-host via source (default)

Used by `scripts/setup.sh` and `scripts/update.sh`. Requires `git clone` on the deploy host and builds the Docker image locally.

**Initial setup:**

```bash
git clone https://github.com/hushhq/hush-server
cd hush-server
./scripts/setup.sh --domain chat.example.com --email ops@example.com
```

**Upgrade:**

```bash
cd ~/hush-server
./scripts/update.sh
```

`update.sh` will:
1. Check pre-flight requirements (Docker, `.env`)
2. Dump a timestamped database backup to `backups/`
3. Pull latest code (`git pull --ff-only`)
4. Rebuild the `hush-api` image and pull runtime images
5. Restart the stack
6. Health-check the API

### Path B: Pre-built GHCR image (custom setups)

If you prefer to consume the pre-built image rather than building from source, override the `hush-api` service in your compose file:

```yaml
# docker-compose.override.yml
services:
  hush-api:
    image: ghcr.io/hushhq/hush-server:v1.1.0
    build: !reset null
```

Then run:

```bash
docker compose -f docker-compose.prod.yml -f docker-compose.caddy.yml -f docker-compose.override.yml up -d
```

Always substitute an exact version tag (e.g. `v1.1.0`). There is no `:latest` tag.

---

## Rollback

### Path A: Source-build rollback

If an upgrade produces a broken instance, roll back in this exact order. Reversing the order risks running old code against a schema it does not understand.

**Step 1 — Check out the previous code before touching the database:**

```bash
cd ~/hush-server
git fetch --tags
git checkout v<previous-version>
```

**Step 2 — Restore the pre-upgrade database backup:**

Use the restore script, which stops the API before restoring and handles both old and new dump formats:

```bash
# List available backups (update.sh creates one before each upgrade)
ls backups/

./scripts/restore.sh backups/hush-YYYYMMDD-HHMMSS.sql
```

The restore script will:
- Stop `hush-api` to prevent concurrent writes
- Drop and recreate the database from the backup
- Leave `hush-api` stopped for operator verification

**Step 3 — Rebuild and restart at the previous version:**

```bash
docker compose -f docker-compose.prod.yml -f docker-compose.caddy.yml build hush-api
docker compose -f docker-compose.prod.yml -f docker-compose.caddy.yml up -d
```

**Step 4 — Verify:**

```bash
curl http://localhost:8080/api/health
curl http://localhost:8080/api/handshake
```

#### Migration note

`golang-migrate` applies migrations forward at server startup and does not support automatic down-migrations.

| Scenario | Database restore required? |
|-|-|
| New version added no migrations | No — restart old code directly |
| New version added migrations AND they were applied | Yes — restore pre-upgrade backup first (Step 2) |
| Upgrade failed before migrations ran | No — database schema unchanged |

If migrations were applied and you skip the database restore, the old code will start against a schema it does not understand and will likely crash or corrupt data.

---

### Path B: Pre-built GHCR image rollback

**Step 1 — Identify the previous working image tag.**

The current pinned version is in `docker-compose.override.yml` (or the compose file you used). Check git history or the running container:

```bash
docker inspect hush-api --format '{{.Config.Image}}'
```

**Step 2 — Check whether migrations were applied** (same rule as Path A above).

If the broken version applied migrations, restore the database first:

```bash
./scripts/restore.sh backups/hush-YYYYMMDD-HHMMSS.sql
```

**Step 3 — Update your override file to the previous tag:**

```yaml
# docker-compose.override.yml
services:
  hush-api:
    image: ghcr.io/hushhq/hush-server:v<previous-version>
    build: !reset null
```

**Step 4 — Pull and restart:**

```bash
docker compose -f docker-compose.prod.yml -f docker-compose.caddy.yml \
  -f docker-compose.override.yml pull hush-api
docker compose -f docker-compose.prod.yml -f docker-compose.caddy.yml \
  -f docker-compose.override.yml up -d
```

**Step 5 — Verify** as in Path A.

---

### When restore-from-backup is mandatory

You must restore the pre-upgrade database backup if **all** of the following are true:
1. The broken upgrade applied at least one new migration to the database.
2. You intend to roll back to code that does not include that migration.

If you are unsure whether migrations ran, check the `schema_migrations` table:

```bash
docker compose -f docker-compose.prod.yml -f docker-compose.caddy.yml exec -T postgres \
  psql -U hush hush -c "SELECT version, applied_at FROM schema_migrations ORDER BY applied_at DESC LIMIT 10;"
```

Compare the latest entry with the migration files in `migrations/` at the target rollback tag.

---

## Version compatibility

| Component | Where version is declared | Coupling |
|-|-|-|
| `hush-server` | Git tag (`vX.Y.Z`) | Independent |
| `hush-web` | `package.json` version field | Independent |
| `hush-crypto` (WASM) | `Cargo.toml` → npm `@gethush/hush-crypto` | `hush-web` pins a specific npm version |

There is no automated cross-repo version coupling. When a `hush-crypto` release is published, `hush-web`'s `@gethush/hush-crypto` pin in `package.json` and `compatibility.json` must be updated manually in a separate PR.

---

## Pinned infrastructure images

Third-party images used in the compose stack are pinned to explicit versions. Floating tags (`:latest`) are not used.

| Image | Pinned version | Source |
|-|-|-|
| `livekit/livekit-server` | `v1.10.1` | [github.com/livekit/livekit/releases](https://github.com/livekit/livekit/releases) |

### Upgrading LiveKit

1. Check the [LiveKit releases page](https://github.com/livekit/livekit/releases) for the target stable version.
2. Read the release notes for breaking changes to the config format or webhook API.
3. Update the `image:` tag in both `docker-compose.prod.yml` and `docker-compose.yml`.
4. Update the version in `ARCHITECTURE.md`.
5. Test locally with `docker-compose up -d livekit` before rolling to production.
6. On the deploy host, run `./scripts/update.sh` to pull the new image and restart the stack.
7. Verify voice rooms are functional after restart.

Never upgrade LiveKit during a period when voice rooms are known to be active.

---

## What requires deploy-host access to verify

The following cannot be confirmed from this repository:

- The actual running version on the production server
- Whether the deploy host has Docker and `git` installed
- The state of the `.env` file on the production server
- Whether pending migrations exist on the live database
- The `CORS_ORIGIN` setting on the production instance
- LiveKit UDP port availability (`50020–50100/udp`)
- **The LiveKit version currently running on the deploy host.** The repo is now pinned to `v1.10.1`. If the host was previously running a different version pulled via `:latest`, the next `./scripts/update.sh` will upgrade (or downgrade) LiveKit to `v1.10.1`. Verify the running version before and after: `docker exec hush-livekit /livekit-server --version`

Run `GET /api/handshake` against the live instance to verify it is reachable and to read its reported version metadata.
