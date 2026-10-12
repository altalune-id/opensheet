# Plane rules

R1, R3, R4, R7, R8 and R9 in full — the ingress rules. Taxonomy, mounts and the R1–R11 summary:
[`surfaces`](README.md). Egress and CLI: [`dispatch and cli`](dispatch-and-cli.md).

### R1 — what "verb" and "plane" mean

Left undefined, R1 is untestable and wrong. Three clarifications:

- **"Plane" means S2 and S3 only.** The console is excluded by design: a human UI over the
  same domain is not duplication, it is the entire point of having a console. `todo`, `org`,
  `project` and `invite` already span S1 + S2 + S6 today, and they are not violations. The CLI
  is excluded too, by R11 — it owns no verbs to duplicate.
- **"Verb" means a domain operation on an aggregate**, identified by
  `surfaces.Verb{Module, Aggregate, Operation}` (`internal/platform/surfaces/`) — not an HTTP
  method and not a function name. `DeletePost` on S2 and `DELETE /posts/{slug}` on S3 are one
  verb.
- **Detection is a hand-maintained registry, not an AST scan.** Each plane declares its verbs
  (`controlplane.VerbTable()`, `dataplane.VerbTable()`) and `TestOneVerbOnePlane` asserts no
  triple appears on both outside the allowlist. `TestDataPlaneVerbTableMatchesRegisteredRoutes`
  and `TestControlPlaneVerbTableMatchesScopeTable` hold each registry against what is actually
  mounted, so a stale table cannot make R1 pass vacuously.

**Why `blog` is allowlisted.** `TestOneVerbOnePlane` (`internal/dataplane/surfaces_test.go`)
allows exactly one module on both planes, and that module is `blog` — the worked example of
each machine plane, the same verbs as Connect RPCs and as REST so a fork can read both shapes
side by side. A demonstration, not a precedent: a fork's own module goes on one plane, and a
second name in that allowlist means writing the reason here first.

### R3 — one primitive per surface

Every surface resolves its tenant through the primitive that surface owns, and never
hand-builds a `tenant.Context`. Console routes call `Deps.RequireProject` / `Deps.RequireOrg`
(`internal/web/handlers/scope.go`), never their own `OrgScopeFor` → `ProjectScopeFor` chain;
`TestTenantGateIsNotCopiedIntoHandlers` fails the build on a handler that rebuilds it. Which
primitive each surface uses, what narrows it, and the tests that hold it together are
[`request scope`](../multitenancy/request-scope.md).

### R4 — authorization depth per surface

| Surface          | Credential                                                     | Authenticated by               | Authorization depth                        |
| ---------------- | -------------------------------------------------------------- | ------------------------------ | ------------------------------------------ |
| S1 console       | `sid` cookie, HMAC                                             | `webmw.Session`                | org membership + role                      |
| S2 control plane | Bearer JWT **or** an api key (`api.keyPrefix`, default `key_`) | `authn.Interceptor(Chain)`     | **method→scope table, fail closed**        |
| S3 data plane    | Bearer or `X-API-Key`, key only — a JWT is **401**             | per-route `Authorize`          | scope **+** `ResourceIDs`                  |
| S4 ingest        | provider signature                                             | per-provider `ingest.Verifier` | none — provider identity _is_ the authz    |
| S5 dispatch      | n/a, we sign                                                   | n/a                            | n/a                                        |
| S6 cli           | in-process: none; remote: the credential of the plane it calls | —                              | inherits the plane it calls                |
| S7 mcp           | Bearer JWT (MCP audience) **or** an api key                    | `authn.Chain` + per-tool scope | **every tool scope-checked, JWT included** |

Fail-closed is the whole point of the S2 row: a key principal reaching a Connect method absent
from the scope table is **denied**, and `TestEveryRPCHasAScope` asserts every mounted method has
an entry. The S7 row deliberately breaks S2's pattern, so do not generalize from it. Both
asymmetries and the enforcement points behind them are [`scopes`](../scopes/README.md); the S7 mount,
its config keys and its tool contract are [`mcp`](../mcp/README.md).

### R7 — what the guard test actually guards

Not proto versioning: Connect paths are `/api/<proto.package>.<Service>/<Method>`, so a `v1`
proto package yields the segment `v1.Foo` and never enters the `/api/v1/` subtree. The real risk
is ordinary — someone mounts a second handler under `/api/v1/`, or moves the data plane and
leaves the RPC mount to swallow the subtree. `TestMountPrefixesReserved` routes a request at
each reserved prefix and asserts it lands on exactly the handler that owns it: `/api/` to S2,
`/api/v1/` to S3, `/hooks/` to S4, `/mcp` to S7.
`TestMCPMountDoesNotSwallowNeighbouringPaths` guards the exact-plus-subtree pair `/mcp` registers.

Console routes get a second guard: `TestRoutes_ListMatchesTheMux` compares the patterns the
handlers registered on `web.recordingMux` (returned by `web.NewServerWithRoutes`) against
`probeRoutes()` in both directions, so a route with no probe and a probe with no route both
fail the build.

### R8 — shaping non-CRUD actions

A command that produces something with its own lifecycle — dispatching a notification,
triggering an export, enqueuing a job — creates a resource.

```
POST .../things/{thing}/dispatches    # correct — the thing dispatched has its own lifecycle
POST .../things/{thing}/dispatch      # forbidden — a dispatch IS a resource
POST .../things/{thing}:dispatch      # forbidden — colon spelling, always
POST .../posts/{slug}/publish         # correct — state transition, verb sub-resource
POST .../posts/{slug}/unpublish       # correct
POST .../posts/{slug}/publications    # wrong — nothing to hang on it
```

Full paths are `/api/v1/orgs/{org}/projects/{proj}/…`. The created resource carries an id, a
status and a URL, which gives idempotency (`Idempotency-Key`), retry semantics and a place to
hang delivery state — none of which the verb-path spelling has anywhere to put. That rationale
is the test, not the spelling: a state transition on an existing resource creates nothing,
because the resource already carries the id, the status and the URL, and `If-Match` already
supplies the concurrency control. Wiring either shape:
[`howto/external-api.md`](../howto/external-api.md).

### R9 — public reads

Published-content reads are the most common data-plane need and the easiest to leak drafts
through. Gated by `blog.publicReads`, surfaced as `caps.PublicReads`.

```
GET /api/v1/.../posts/{slug}
  no credential + published + caps.PublicReads            => 200
  no credential + published + flag off                   => 404
  no credential + draft                                  => 404   (never 403)
  valid key with posts:read + draft                      => 200
```

404 rather than 403 is deliberate: on an unauthenticated surface, confirming a resource exists
is itself a disclosure, so a draft slug must be indistinguishable from a slug never used.

**An anonymous request still gets a tenant scope, pre-scope and path-derived.**
`Deps.RequireProject` assumes a session, so it cannot serve this path; `internal/dataplane/scope.go`
resolves the slugs through a `SECURITY DEFINER` lookup and enters a `tenant.Context` with **no**
`UserID`, leaving RLS to constrain every read ([`request scope`](../multitenancy/request-scope.md)).
A bad org slug, a project in another org, and a real project with no matching post are one
indistinguishable **404** — `TestUnresolvableScopeIsIndistinguishable` asserts all three answer
alike, because a body or timing difference is the whole attack.

An anonymous principal must never reach a write path: the capability flag gates reads only,
and every write route requires a key regardless of it (`TestPublicFlagNeverOpensWrites`).
