# opensheet configuration

The template page is [`config`](README.md): precedence, awareness tags, modes and every template
key. This page is the keys opensheet adds and the rules it tightens. The Google connect flow and
its OAuth notes are in [`gworkspace/README.md`](../../gworkspace/README.md).

## Keys

| Key                         | Default    | Awareness                      | Meaning                                                                           |
| --------------------------- | ---------- | ------------------------------ | --------------------------------------------------------------------------------- |
| `api.keyPrefix`             | `osk_`     | `-`                            | opensheet's API keys are `osk_…`. Changing it orphans every minted key.           |
| `google.oauth.clientID`     | `""`       | `required, mode:cloud`         | OAuth client for the Google connect flow. Empty hides the flow.                   |
| `google.oauth.clientSecret` | `""`       | `required, mode:cloud, secret` | Its secret. Set without `clientID`, boot is refused.                              |
| `google.picker.apiKey`      | `""`       | `required, mode:cloud`         | Browser API key for the Google Picker that grants `drive.file` on a spreadsheet.  |
| `google.timeout`            | `15s`      | `-`                            | Per-call budget for a Google request.                                             |
| `cache.driver`              | `auto`     | `bootstrap`                    | Sheet snapshot store: `auto`, `postgres` or `memory`. `auto` follows `db.driver`. |
| `cache.defaultTTL`          | `30s`      | `-`                            | Snapshot TTL for a sheet whose own TTL is `0`.                                    |
| `cache.maxBytes`            | `67108864` | `-`                            | Bounds the `memory` driver only.                                                  |
| `sheets.publicEnabled`      | `false`    | `bootstrap`                    | Allows `public` sheets. Checked on every read, not only at publish.               |
| `sheets.maxPayloadBytes`    | `8388608`  | `-`                            | Bounds a stored snapshot. An unfiltered read of a larger tab is `413`.            |
| `sheets.maxQueryRows`       | `1000`     | `-`                            | Cap and default for `?limit=` on a filtered read.                                 |
| `sheets.maxSortPages`       | `20`       | `-`                            | How many pages deep a `?sort=` walk may go (`SHT040` past it).                    |

`security.encryptionKey` is the template's key. opensheet also seals stored Google credentials with
it, so a lost or rotated key makes every stored credential unreadable until it is reconnected.

## Rules opensheet adds

- `cache.driver=postgres` requires `db.driver=postgres`.
- More than one replica on the `memory` cache driver needs `queue.enabled=true`, so a write on
  one replica drops the others' snapshots ([`sheet.snapshot_invalidated`](../queue/opensheet.md)).
- `db.driver=postgres` requires `http.stateSecret` (`OPENSHEET_HTTP_STATE_SECRET`) in every mode.
  It is the HMAC key for the session cookie; with it empty boot mints an ephemeral key, so every
  persisted session stops verifying on restart. Under `driver: sqlite` the ephemeral fallback
  stands.
- `security.encryptionKey` and `http.stateSecret` share one format: 32 bytes, as 64 hex characters
  or as base64. Generate either with `openssl rand -hex 32`. Hex is tried first, base64 second.
  `config.Validate()` checks a non-empty secret, so a malformed one is refused before any listener
  opens.
