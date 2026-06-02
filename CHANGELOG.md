# Changelog

All notable changes to the Hush server are recorded here. Format is based on
[Keep a Changelog](https://keepachangelog.com). Dates are ISO 8601. Versions
track the released image tag (`ghcr.io/hushhq/hush-server:vX.Y.Z`).

This file is the source for the **Highlights** section of each GitHub Release
(see `docs/release-policy.md`). The release's Migration, Minimum client version,
and Verification (image digest) sections are build-specific and are added per the
release-notes contract at publish time.

## [Unreleased]

## [0.2.0] - 2026-05-30

### Added
- Boot-time DB schema compatibility gate: the server refuses to start if the live
  database has been migrated past the binary's schema version, turning a silent
  rollback corruption into a fail-safe stop. (HUSHHQ-83)
- Browser invite landing pages at `/invite/<code>` and `/join/<host>/<code>` so
  opening an invite link in a browser shows join instructions instead of a raw
  reverse-proxy fallback. (HUSHHQ-88)
- Documented upgrade path plus release and versioning policy
  (`docs/release-policy.md`). (HUSHHQ-83, HUSHHQ-84)

### Notes
- No migration required: schema version unchanged at 37. The new guardrail is a
  no-op on the 0.1.0 -> 0.2.0 upgrade and protects future rollbacks.

## [0.1.0] - 2026-05-30

### Added
- Initial tagged, signed release of the Hush server image
  (`ghcr.io/hushhq/hush-server`), with the self-host install and release
  verification flow.
