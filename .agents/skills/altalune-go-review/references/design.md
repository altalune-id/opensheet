# Design pass

The test for every finding here: **when this needs to change next time, how many places must
change?** One is the goal. Two or more is a finding, unless the copies are meant to drift.

## One source of truth

Before accepting a new constant, type, helper or list, search for the existing owner. These are
the owners in this codebase; a second copy of anything they hold is a finding.

| Thing                            | Owner                                                       |
| -------------------------------- | ----------------------------------------------------------- |
| error codes, wire mapping        | `internal/apperror/codes.go`, `docs/errors/README.md`       |
| scopes per RPC / MCP tool        | `internal/controlplane/scopes.go`, `internal/mcp/scopes.go` |
| surface names, mounts, chains    | `internal/platform/surfaces`, `web.SurfaceChains`           |
| URL joining under a base path    | `web.Path`                                                  |
| config keys, defaults, awareness | `internal/platform/config` (one field, one default)         |
| tenant scope for a request       | `Deps.RequireProject` / `RequireOrg`, `tenant.From(ctx)`    |
| credentials, auth failures       | `internal/platform/authn`                                   |
| SQL NULLs, SQLite time           | `internal/platform/db/entity/{postgres,sqlite}` helpers     |
| outbound HTTP, retries           | `httpclient/`                                               |
| ids, slugs, request ids          | `nanoid/`, `slug/`, `reqid/`                                |
| webhook event catalog            | `internal/platform/events`                                  |
| UI strings                       | locale files via `d.Tr`, never a literal in a `.templ`      |
| test doubles                     | `internal/testutil/fakes/` — one fake per port, shared      |

## Checks

- **Duplicated knowledge.** The same literal, list, switch or mapping in two files. A string
  compared in one place and produced in another. A `switch` over a kind that a new kind would
  silently fall through. Fix: one constant, one table, or one registry the others read.
- **Reinvented wheel.** A helper that repeats a stdlib function, a `slices`/`maps` call, or an
  existing package from the table above. Fix: call the existing one.
- **Reuse blocked, not extended.** A near-copy made because the original did not quite fit.
  Fix: extend or extract the original so both callers use it. Moving it to `internal/platform/`
  or a root package follows [`platform`](../../../../docs/platform/README.md#root-or-internalplatform).
- **Extension needs edits in many places.** Adding the next surface, provider, event, locale,
  scope or error code should mean one new row or one new file. If it means touching N switches,
  that is a finding. `docs/howto/` recipes define the expected number of touch points.
- **Wrong layer.** A surface reaching a `Store`, a `Service` knowing about a surface, a root
  package importing `internal/`, domain policy inside a platform package —
  [`architecture`](../../../../docs/architecture/README.md#rules-each-layer-must-not-break).
- **Fork cost.** A signature change in a verbatim-copied package (`authl/ httpclient/ logger/
mailer/ mcp/ nanoid/ reqid/ scheduler/ telemetry/ worker/ internal/platform/`) needs a reason
  and tests first. Load `go-release` for exported API changes.

## Scalability

- Unbounded work: a query without a limit, a list without pagination, a fan-out without a cap,
  a goroutine per request with no owner or shutdown path.
- N+1: a store call inside a loop over rows. Batch it or join it.
- Per-tenant cost: a query on a tenant table without the `org_id` predicate scans every tenant.
- Hot-path allocation or locking: a global mutex, a cache with no bound or TTL.
- Anything that holds in memory what should live in Postgres or the queue.

## Future-proofing, without speculation

Future-proof means **the next change is cheap**, not that it is already built.

- Contracts are additive: error codes, scopes, event payloads, proto fields, config keys.
  Removing or renaming one is a breaking change.
- Migrations are new files; an applied migration is never edited.
- A `Config` field carries its `awareness` tag.

The counterweight — flag these just as hard:

- An interface with one implementation and no test that needs a fake.
- A generic helper with one caller. Extract on the second real caller, not in advance.
- Options, flags or hooks no requirement asks for.
- A layer that only forwards calls.

Simple and in one place beats clever and general.
