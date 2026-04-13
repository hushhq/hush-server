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

If an upgrade produces a broken instance:

1. Restore the database backup created by `update.sh`:

```bash
cd ~/hush-server
# Identify the backup file created before the broken upgrade
ls backups/
docker compose -f docker-compose.prod.yml -f docker-compose.caddy.yml exec -T postgres \
  psql -U hush hush < backups/hush-YYYYMMDD-HHMMSS.sql
```

2. Check out the previous release tag and rebuild:

```bash
git fetch --tags
git checkout v<previous-version>
docker compose -f docker-compose.prod.yml -f docker-compose.caddy.yml build hush-api
docker compose -f docker-compose.prod.yml -f docker-compose.caddy.yml up -d
```

**Migration note:** Migrations run at server startup (`golang-migrate`). There is no automatic down-migration. If the new release added migrations, rolling back after applying them requires restoring the database backup from step 1 first.

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
