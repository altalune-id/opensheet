# opensheet deployment

The template page is [`deployment`](README.md); workers and the `serve` flag matrix are in
[`workers`](workers.md). This page is what opensheet adds.

## Required secrets under Postgres

`security.encryptionKey` and `http.stateSecret` are both required under `db.driver=postgres`, in
every mode. See [`config/opensheet.md`](../config/opensheet.md#rules-opensheet-adds).

## Session sweep without the scheduler

NOTE: a Postgres deployment running with `scheduler.enabled=false` never runs `session-sweep`, so
it needs its own periodic `DELETE FROM <prefix>sessions WHERE expires_at < now()`.

## Registering opensheet in Othra

The steps are in [`deployment`](README.md#oidc). opensheet's resource servers take these scopes
(the catalog is in [`scopes`](../scopes/README.md)):

| Resource server | Scopes                                                                                                                                                           |
| --------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `<base>/mcp`    | `sheets:read`, `members:read`                                                                                                                                    |
| `<base>/api`    | `sheets:read`, `sheets:admin`, `spreadsheets:read`, `spreadsheets:write`, `credentials:read`, `credentials:write`, `cache:purge`, `apikeys:read`, `members:read` |

Not part of the registration:

- **The data plane** (`/api/v1/`) takes opensheet API keys only. It accepts no Othra token and
  serves no RFC 9728 document. Integrators keep calling it with an API key, unchanged.
- **Google Sheets and Drive OAuth** (`google.oauth.*`, `/credentials/google/callback`) is
  opensheet's own client of Google for reading spreadsheets. It is not sign-in and has nothing to
  register in Othra; it shares only `http.stateSecret` with the sign-in flow.
