# Self-hosting hush-server

This guide covers the **image-based** self-host path: pull a verified
release image from GHCR and bring up the stack with one command. For
the source-build path see [README.md → Quick Start (Self-Hosting)](../README.md#quick-start-self-hosting).

## Audience and non-goals

- **Audience.** Operator running Hush on a VPS, homelab box, or single
  cloud VM with `docker compose`. Comfortable with Linux + DNS +
  reverse-proxy basics.
- **Non-goals.** Multi-node HA, Kubernetes, autoscaling, web-client
  hosting. Multi-instance federation is on the roadmap but not shipped
  — instances are closed networks today.

## What you actually deploy

Three artifacts make up a Hush deployment:

| Component | Source | Status in this guide |
|-|-|-|
| **`hush-server` container** | `ghcr.io/hushhq/hush-server:vX.Y.Z` | Primary artifact you self-host. |
| **Desktop client (DMG / EXE / AppImage / deb)** | [hush-desktop GitHub Releases](https://github.com/hushhq/hush-desktop/releases) | Canonical user surface in this phase. Each user installs it locally and points it at your instance URL. |
| **Web client (`hush-web`)** | Hosted by us at `https://app.gethush.live` | **Not part of the supported self-host path in this phase.** No `ghcr.io/hushhq/hush-web` image is published. The web bundle exists as the Electron renderer and as our convenience host. |

If a user only has a browser, they can still reach your instance by
visiting `https://app.gethush.live` and adding your domain in the
instance picker — the web client is instance-agnostic. We do not
publish a containerised `hush-web` for self-hosters at this point in
the roadmap.

## Prerequisites

- Docker Engine 24+.
- Docker Compose **v2.20+** (required for the `!reset` override
  semantics used by `docker-compose.selfhost.yml`). `docker compose
  version --short` must return at least `2.20.0`.
- A domain pointing at the host (Let's Encrypt) **or** a static
  public IP (self-signed via Caddy's internal CA).
- 80/tcp and 443/tcp reachable from the public internet.
- 7881/tcp + 50020-50100/udp open for LiveKit media (or proxied
  via Caddy; see [README.md → Reverse Proxy](../README.md#reverse-proxy)).
- Recommended: [cosign 2.x](https://docs.sigstore.dev/cosign/installation/)
  for image signature verification before deploy.

## Pick a release tag

Releases are published to `ghcr.io/hushhq/hush-server`. Browse them at
<https://github.com/hushhq/hush-server/pkgs/container/hush-server>.

**Only `vX.Y.Z` tags are published in this phase.** Moving aliases
(`vX.Y`, `vX`, `latest`, `nightly`) are intentionally not published
until the server/client/DB compatibility contract lands. Always pin to
an exact `vX.Y.Z` tag.

```sh
RELEASE=v0.1.38   # whatever the latest release is
```

## Verify the image (recommended)

Every release tag is signed via cosign keyless OIDC and carries an
SPDX SBOM attestation. Verify before pulling so a compromised registry
mirror cannot serve you a forged image:

```sh
./scripts/verify-release.sh $RELEASE
```

This wraps:

```sh
cosign verify ghcr.io/hushhq/hush-server:$RELEASE \
  --certificate-identity-regexp '^https://github.com/hushhq/hush-server/\.github/workflows/ci\.yml@refs/tags/v.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Expected output ends with `OK: signature valid for ...`. A failure
means **do not deploy** — the image was either not signed by our CI
or someone is tampering with the registry response.

To inspect the SBOM (Software Bill of Materials):

```sh
cosign download attestation ghcr.io/hushhq/hush-server:$RELEASE \
  | jq -r '.payload' | base64 -d | jq '.predicate'
```

## Install

```sh
git clone https://github.com/hushhq/hush-server
cd hush-server
./scripts/setup.sh \
  --from-image $RELEASE \
  --domain chat.example.com \
  --rtc-domain rtc.example.com \
  --email ops@example.com
```

What `setup.sh --from-image` does that the source-build flow does
not:

- Validates the tag against `^v[0-9]+\.[0-9]+\.[0-9]+$` (no moving
  aliases accepted).
- Enforces Docker Compose v2.20+ for the `!reset` override.
- Runs `cosign verify` if cosign is on `PATH`; hard-fails on bad
  signature.
- If cosign is **not** installed, prints a loud warning and continues
  — install cosign for strict supply-chain verification, or pass
  `--skip-verify` to acknowledge the gap explicitly.
- Layers `docker-compose.prod.yml` + `docker-compose.selfhost.yml` +
  `docker-compose.caddy.yml` (override order matters).
- Replaces the `compose_cmd build hush-api` step with a `compose pull`,
  so no Go toolchain or Node toolchain is required on the host.

The rest of the setup flow — secrets generation, Caddyfile template
substitution, MinIO bootstrap, health checks — is identical to the
source-build flow documented in [README.md → Quick Start](../README.md#quick-start-self-hosting).

### IP-only mode

```sh
./scripts/setup.sh --from-image $RELEASE --ip 203.0.113.42
```

Caddy serves a self-signed cert from its internal CA. Browser shows a
TLS warning; E2EE is unaffected. Useful for evaluation, not for
production.

## Upgrade path

```sh
cd hush-server
git pull
./scripts/setup.sh --from-image $NEW_RELEASE --domain chat.example.com --rtc-domain rtc.example.com --email ops@example.com --force
```

The `--force` flag preserves the existing `.env` and bypasses the
overwrite prompt. The new image is pulled, the stack is recreated, and
health checks run.

**Always back up before upgrading.** See [RUNBOOK.md → Backup and
restore](RUNBOOK.md#backup-and-restore) for the canonical procedure.
Rollback is supported per [RUNBOOK.md → Rollback paths](RUNBOOK.md#rollback-paths)
— you can `--from-image` an older release as long as no DB migration
has rolled forward irreversibly. The HUSHHQ-83 compatibility gate
(future work) will make this safer; until then, treat every server
version bump as potentially DB-forward-only.

## Storage backends

Three options, fully documented in [RUNBOOK.md → Storage backends](RUNBOOK.md#storage-backends):

- **postgres_bytea** (default, simplest, fine for small instances).
- **Bundled MinIO** (recommended for domain mode at scale; enables
  larger attachment / link-archive quotas).
- **External S3 / R2** (production-grade; bring your own bucket).

Storage choice is set in `.env` and applied by `setup.sh`; no compose
override needed.

## Required reading before deploy

Self-hosters touch the same code paths as the hosted instance. The
[CORE-INVARIANTS](https://github.com/hushhq/hush/blob/main/docs/CORE-INVARIANTS.md)
document spells out the rules a deployment must not break. The
sections most relevant to a self-hoster:

- **Authentication, Vault, and Device Identity** — device revocation
  must invalidate the local vault path. Do not patch the server to
  loosen this.
- **MLS, Messages, and Realtime Catch-up** — MLS ciphersuite and
  epoch handling are protocol invariants. Server upgrades that change
  these will be flagged in release notes as `BREAKING` once
  HUSHHQ-84 ships.
- **Voice Rooms and LiveKit** — the RTC subdomain (`rtc.example.com`)
  must be a separate origin from the API domain for the
  Safari/iCloud Private Relay path. `setup.sh` enforces this.
- **Federation, Instance Routing, and Credential Boundaries** —
  instances are closed networks today; federation is planned. A
  self-hosted instance does not accept logins from other instances.
- **Build, Release, and Hosted Deploy** — no public repo
  documentation that depends on non-public operational paths. The
  published image is audited at CI time to guarantee this.

## Federation status

Not shipped. Each Hush instance is a closed network today. Accounts on
`chat.example-a.com` do not federate with `chat.example-b.com`. Users
who participate in multiple instances add each one separately to their
desktop client.

## Troubleshooting

See [RUNBOOK.md → Operational runbook](RUNBOOK.md) for:

- log locations and what each service prints
- database connection failures
- LiveKit signaling failures
- MinIO bootstrap retries
- Caddy ACME challenges and rate limits
- safe restart vs. full rebuild

If `setup.sh` fails health checks within 60 seconds, the most common
causes are:

| Symptom | Likely cause |
|-|-|
| `getsockopt: connection refused` on `/api/health` | `hush-api` crashed — check `docker compose logs hush-api`. Usually a missing required env var. |
| TLS handshake errors after `setup.sh` completes | DNS not yet pointing at the host, or Caddy hit ACME rate limit. |
| LiveKit clients fail to connect | RTC subdomain not pointing at the host, or 7881/tcp blocked at firewall. |
| `manifest unknown` on `compose pull` | Wrong tag. Confirm at <https://github.com/hushhq/hush-server/pkgs/container/hush-server>. |

For anything else, open an issue at <https://github.com/hushhq/hush/issues>
with the output of `docker compose logs --tail=200`.
