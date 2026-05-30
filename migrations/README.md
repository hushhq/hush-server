# Migrations

Forward-only SQL migrations applied by `golang-migrate` at server boot
(`cmd/hush/main.go`). Files follow the `NNNNNN_slug.{up,down}.sql` naming the
tool expects.

## Every migration needs a metadata sidecar

Each `NNNNNN_slug.up.sql` must ship a sibling `NNNNNN_slug.meta.json`. The
sidecar declares the compatibility facts the release and upgrade pipeline
needs but cannot infer from the SQL:

```json
{
  "version": 38,
  "summary": "short human-readable description",
  "min_prior_server_version": "0.0.0",
  "compat_break": false,
  "supports_rollback": true
}
```

| Field | Meaning |
|-|-|
| `version` | Integer matching the `NNNNNN` filename prefix. |
| `summary` | One-line description of the migration. |
| `min_prior_server_version` | Lowest server version this migration assumes was already running. Use `"0.0.0"` when no floor applies. |
| `compat_break` | `true` only when the migration is not safely reversible or changes a client/server/DB contract. A release shipping a compat break must not advance a moving tag (`latest`) and must raise `version.MinCompatibleDBSchemaVersion` to this version. |
| `supports_rollback` | Must equal whether a `.down.sql` exists. The lint cross-checks it. |

## What the lint enforces

`internal/migrationmeta` is parsed and validated by `go test ./...`. The build
fails when:

- a `.up.sql` has no `.meta.json` sidecar;
- the sidecar does not parse, or `version` disagrees with the filename;
- `supports_rollback` disagrees with the on-disk `.down.sql`;
- `min_prior_server_version` is empty;
- migration versions are not contiguous `1..N`;
- the highest migration version does not equal
  `version.CurrentDBSchemaVersion` (bump that constant when adding a
  migration);
- a declared `compat_break` migration sits above
  `version.MinCompatibleDBSchemaVersion`.

## Adding a migration

1. Write `NNNNNN_slug.up.sql` and `NNNNNN_slug.down.sql`.
2. Add `NNNNNN_slug.meta.json` (copy an existing one and edit).
3. Bump `version.CurrentDBSchemaVersion` to the new number.
4. If the migration is a compat break, set `compat_break: true` and raise
   `version.MinCompatibleDBSchemaVersion` to the new number.
5. Run `go test ./internal/migrationmeta/...` to confirm the lint passes.
