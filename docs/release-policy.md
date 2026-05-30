# Release and versioning policy

This document defines how `hush-server` is versioned, what counts as a
compatibility break, and what self-hosters can and cannot rely on today. It
is the policy companion to the mechanics in
[self-hosting.md](self-hosting.md) (image verify, install, upgrade) and the
schema guardrail in `internal/dbcompat`.

## Current state: immutable tags only

`hush-server` publishes exactly one Docker tag per release:
`ghcr.io/hushhq/hush-server:vX.Y.Z`, an immutable SemVer tag. Each is
multi-arch, cosign-signed, and SBOM-attested (see
[self-hosting.md → Verify the image](self-hosting.md#verify-the-image-recommended)).

**There is no `latest`, no `nightly`, and no `vX.Y` / `vX` moving tag.** This
is deliberate. A moving tag is an auto-upgrade channel, and an end-to-end
encrypted server with handshake-gated clients cannot safely auto-upgrade
until the compatibility contract that protects it is fully in place.

> No moving tag, no auto-update path, and no `latest` for self-hosters ship
> until the server/client/DB compatibility work (the gate described below)
> is complete. Until then, pin an exact `vX.Y.Z` and upgrade deliberately.

When moving tags do arrive, they will be gated by the rules in this document:
`latest` will only ever advance to a release that is not a compatibility
break and that has published release notes.

## What "compatibility break" means here

Hush is in MVP / alpha (`0.x.y`). We do not mechanically apply textbook
SemVer. The operational definition:

A release is a **compatibility break** when any of the following is true:

- it ships a migration whose `.meta.json` carries `compat_break: true` (a
  migration that is not safely reversible, or that changes a client / server
  / DB contract);
- it raises `version.MinCompatibleClientVersion` such that an in-the-wild
  client would be refused at handshake;
- it changes the MLS ciphersuite, the auth/handshake envelope, or any other
  protocol surface a current client depends on.

### Versioning rule during MVP / alpha (`0.x.y`)

- A compatibility break bumps the **minor** version (`0.X.0`) and the release
  notes carry a `BREAKING:` line.
- A non-breaking fix or additive feature bumps the **patch** version
  (`0.x.Z`).
- The eventual `latest` tag (future work) skips a `BREAKING` release until
  clients have caught up: self-hosters on `latest` stay on the prior
  compatible version rather than auto-upgrading into a break.

### After `1.0.0`

Revisit and move toward strict SemVer (major on break). Documented here when
that transition is made; do not assume it before then.

## The two guardrails that already exist

Even with immutable tags, two safety mechanisms are live:

1. **Boot-time DB schema gate** (`internal/dbcompat`). If the live database
   has been migrated past the binary's compiled-in
   `version.CurrentDBSchemaVersion`, the server refuses to start with an
   actionable error rather than running old code against a newer schema.
   This turns a silent-corruption rollback into a fail-safe stop. It does
   not make migrations reversible; rolling back across a forward migration
   still requires a database restore from a pre-upgrade backup.

2. **Handshake compatibility envelope** (`/api/handshake`). The server
   advertises `server_version`, `min_compatible_client_version`,
   `current_db_schema_version`, `min_compatible_db_schema_version`, and
   `crypto_compat_ranges`. A client below the minimum surfaces an
   "update required" flow and refuses normal traffic.

## Migration metadata

Every migration carries a `NNNNNN_slug.meta.json` sidecar declaring
`compat_break`, `supports_rollback`, and `min_prior_server_version`. A CI
lint fails the build if a migration is missing its sidecar or if the
declared facts disagree with the on-disk files. See
[migrations/README.md](../migrations/README.md).

## Release notes contract

Every published release carries a GitHub Release with a structured body.
The shape is fixed so a self-hoster (and the `/releases` page on the
landing site) can read the same facts every time, and so CI can fail a
release whose notes are missing a required section. The required sections:

- **Highlights**: user-facing bullets of what changed.
- **Migration required?**: `Yes` or `No`. If `Yes`, the steps, and whether
  the schema version advances.
- **Minimum client version**: the value the server advertises as
  `min_compatible_client_version`. Clients below it are gated at handshake.
- **BREAKING**: present only when the release ships a `compat_break`
  migration or otherwise breaks a client/server/DB contract (see the
  compatibility-break definition above). A `BREAKING` release does not
  advance the `latest` moving tag.
- **Verification**: the image reference, its multi-arch index digest, and
  the `cosign verify` command (or a pointer to `scripts/verify-release.sh`)
  so a self-hoster can confirm the artifact before deploying.

A minimal template:

```markdown
## Highlights
- ...

## Migration required?
No. Schema version unchanged at N.

## Minimum client version
0.0.0

## Verification
Image: ghcr.io/hushhq/hush-server:vX.Y.Z
Digest: sha256:...
./scripts/verify-release.sh vX.Y.Z
```

## Backups are mandatory before upgrading

Always take a backup before any upgrade. See
[RUNBOOK.md → Backup and restore](RUNBOOK.md#backup-and-restore). The schema
gate protects you from running incompatible code; it does not protect you
from a migration you did not back up before.
