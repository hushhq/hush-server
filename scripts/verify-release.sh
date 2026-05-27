#!/bin/sh
# HUSHHQ-82: verify a published hush-server release tag using the
# cosign keyless signature issued by GitHub Actions during the CI
# publish flow.
#
# Usage:
#   ./scripts/verify-release.sh vX.Y.Z
#
# Exits 0 when the signature is valid; non-zero on any failure
# (missing cosign, unknown tag, mismatched OIDC identity).
#
# The expected identity is the workflow path + tag ref the publish job
# uses at `.github/workflows/ci.yml@refs/tags/vX.Y.Z`. Both this script
# and `setup.sh --from-image` reference the same regex; keep them in
# sync if the workflow moves.

set -eu

IMAGE="ghcr.io/hushhq/hush-server"
IDENTITY_REGEX='^https://github.com/hushhq/hush-server/\.github/workflows/ci\.yml@refs/tags/v.*'
OIDC_ISSUER='https://token.actions.githubusercontent.com'

if [ $# -ne 1 ]; then
  printf 'usage: %s vX.Y.Z\n' "$0" >&2
  exit 1
fi

tag="$1"

# Only accept strict release tags. Moving aliases (vX.Y, vX, latest,
# nightly) are deferred to HUSHHQ-84 and must not be accepted here.
if ! printf '%s' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
  printf 'error: expected a fully-qualified release tag (e.g. v0.1.38); got %s\n' "$tag" >&2
  exit 1
fi

if ! command -v cosign >/dev/null 2>&1; then
  printf 'error: cosign is required. Install from https://docs.sigstore.dev/cosign/installation/\n' >&2
  exit 1
fi

printf 'Verifying %s:%s...\n' "$IMAGE" "$tag"
cosign verify "$IMAGE:$tag" \
  --certificate-identity-regexp "$IDENTITY_REGEX" \
  --certificate-oidc-issuer "$OIDC_ISSUER" >/dev/null

printf 'OK: signature valid for %s:%s\n' "$IMAGE" "$tag"
