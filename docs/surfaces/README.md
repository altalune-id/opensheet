# Surfaces

The canonical rule set for the seven-surface taxonomy every domain module and every fork
follows.

Layer map: [`architecture`](../architecture/README.md) · Scope strings: [`scopes`](../scopes/README.md) ·
Tenant scope per surface: [`request scope`](../multitenancy/request-scope.md) ·
S7 in full: [`mcp`](../mcp/README.md)

Rules in full: [`plane rules`](plane-rules.md) — R1, R3, R4, R7, R8, R9 ·
[`dispatch and cli`](dispatch-and-cli.md) — R10, R11

**Adding a route?** Pick its surface below — that fixes the mount, the credential and the
middleware chain — then follow the recipe for it in [`howto/`](../howto/README.md), one per
surface. This file is the contract; the recipes are the procedure.

## Terms

A **surface** is one way the outside world reaches the app, or one way the app reaches the
outside world on a tenant's behalf. Seven, on two axes.

| #   | Term              | Package                         | Mount                | Direction  | Initiated by                             |
| --- | ----------------- | ------------------------------- | -------------------- | ---------- | ---------------------------------------- |
| S1  | **console**       | `internal/web/handlers/`        | `/`                  | ingress    | a person, in a browser                   |
| S2  | **control plane** | `internal/controlplane/`        | `/api/`              | ingress    | the CLI; integrator automation           |
| S3  | **data plane**    | `internal/dataplane/`           | `/api/v1/`           | ingress    | an integrator's running product          |
| S4  | **ingest**        | `internal/ingest/`              | `/hooks/{provider}/` | ingress    | a third party pushing to us              |
| S5  | **dispatch**      | `internal/webhook/` + outbox    | —                    | **egress** | us, calling the tenant                   |
| S6  | **cli**           | `internal/cli/`                 | —                    | dual-mode  | an operator, at a terminal               |
| S7  | **mcp**           | `internal/mcp/` + `mcp/` (root) | `/mcp`               | ingress    | an MCP host acting for a person or agent |

Every mount is prefixed by `http.basePath` when one is set. The one exception is the RFC 9728
metadata document, which the well-known URI anchors at the host root ([`mcp`](../mcp/README.md)).

The package is `internal/dataplane/`, never `internal/data/`: `data` names a layer, not a
domain, and reads as persistence next to `internal/platform/db`. Layer-named packages are
rejected everywhere else in this repo and this one is no exception.

### Middleware chains

One chain per surface, built in `boot.surfaceChains` (`internal/boot/http.go`) and carried as
`web.SurfaceChains{Console, Control, Data, Ingest, MCP, Probes}`. There is no global chain.

| Chain     | Serves                                                                   | On top of `edge`                                     |
| --------- | ------------------------------------------------------------------------ | ---------------------------------------------------- |
| `Console` | S1, and every unmatched path under `basePath`                            | CSP, `Recover`, `Session`, `Tenant`, i18n, SSR gates |
| `Control` | S2                                                                       | nothing — Connect owns its own error shape           |
| `Data`    | S3                                                                       | `RecoverJSON`                                        |
| `Ingest`  | S4                                                                       | `RecoverJSON`                                        |
| `MCP`     | S7 `/mcp`                                                                | `RecoverJSON`                                        |
| `Probes`  | `/healthz`, `/readyz`, `/robots.txt`, S7's metadata and challenge routes | `Recover`, no error template                         |

`edge` is `RequestID` + `RequestLog` + `OTel` and every chain starts with it. S5 and S6 have
no chain: neither is an inbound HTTP route.

### S6 is dual-mode, and that matters

The CLI reaches the domain two ways ([`cli`](../cli/README.md),
`internal/cli/root.go`):

- **In-process** via `ServerBootFn` — `serve`, `migrate`, local operator commands. Here the
  CLI is a genuine driving adapter, calling `Service` methods directly.
- **Out-of-process** via `ClientBootFn` — `internal/boot/client.go` builds a
  `controlplane.Client` that speaks Connect to a remote over the wire.

The second mode is why S2 must stay a first-class versioned contract: **its only in-tree
consumer today is your own CLI**, not browser JavaScript. `gen/` emits Go and OpenAPI only,
`package.json` carries neither `connect-es` nor `protobuf-es`, and the scripts vendored into
`internal/web/static/` — htmx, its CSP shim, the Markdown editor — are HTML over the console,
not Connect over `/api/`.

### The naming collision this prevents

> **S5 dispatch sends to tenants** — durable, retried and tenant-scoped, on the outbox in
> `internal/platform/outbox/`. **`internal/platform/notify` sends to operators** —
> Slack/Discord/Google Chat alerts, fire-and-forget, bounded queue
> (`platform/notify/webhook.go`). Never the same code path: a tenant endpoint wired into the
> alert sink leaks operational data cross-tenant and silently drops on queue overflow.

### Browser callbacks are console, not ingest

An OAuth redirect (`/oauth/callback`) ends in a 302 to a page a person is looking at, so it is
**S1** ([`howto/webpage.md`](../howto/webpage.md)). Only credential-free machine pushes — provider
webhooks, Pub/Sub push — are **S4**, with their own mount and middleware chain
([`howto/webhook-in.md`](../howto/webhook-in.md)).

---

## Rules

Each linked number opens the rule in full.

|                                                                 | Rule                                                                                                                                               | Enforced by                                                                                |
| --------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------ |
| [R1](plane-rules.md#r1--what-verb-and-plane-mean)               | A verb lives on exactly one **machine plane** (S2 or S3).                                                                                          | `TestOneVerbOnePlane`, allowlist `{"blog"}`                                                |
| R2                                                              | Each surface declares its credential class and accepts no other.                                                                                   | table test per surface                                                                     |
| [R3](plane-rules.md#r3--one-primitive-per-surface)              | Tenant scope resolves through one shared primitive; surfaces differ only in how they address it.                                                   | guard test per surface                                                                     |
| [R4](plane-rules.md#r4--authorization-depth-per-surface)        | Authorization depth is declared per surface, not chosen per handler.                                                                               | `TestEveryRPCHasAScope`                                                                    |
| R5                                                              | Machine surfaces are never in the SSR middleware chain.                                                                                            | `TestMachineSurfacesSkipSSRChain`, `TestProbesSkipSSRChain`, `TestMCPSurfaceSkipsSSRChain` |
| R6                                                              | Error shape is fixed per surface.                                                                                                                  | `TestUnroutedRequestsKeepTheR6Envelope`, `internal/ingest/ingest_test.go`                  |
| [R7](plane-rules.md#r7--what-the-guard-test-actually-guards)    | Mount prefixes are reserved; nothing but the data plane mounts under `/api/v1/`.                                                                   | `TestMountPrefixesReserved`                                                                |
| [R8](plane-rules.md#r8--shaping-non-crud-actions)               | A non-CRUD data-plane action creates a resource, never an RPC-shaped path.                                                                         | review rule                                                                                |
| [R9](plane-rules.md#r9--public-reads)                           | Uncredentialed reads require a capability flag and explicitly published state; absence is 404, never 403.                                          | `TestPublicReadMatrix`, `TestUnresolvableScopeIsIndistinguishable`                         |
| [R10](dispatch-and-cli.md#r10--dispatch-in-full)                | Dispatch signs HMAC-SHA256 over the raw body with a timestamp, per-endpoint secret, at-least-once via `worker/`, dialed only through `httpclient`. | transport tested in `httpclient/`                                                          |
| [R11](dispatch-and-cli.md#r11--the-cli-is-a-client-not-a-plane) | The CLI owns no verbs. In remote mode it is a client of the plane that owns the verb.                                                              | review rule                                                                                |

---

## Status of each surface in this template

| Surface          | State                                                                                            |
| ---------------- | ------------------------------------------------------------------------------------------------ |
| S1 console       | shipped                                                                                          |
| S2 control plane | shipped — gated by `api.enabled`                                                                 |
| S3 data plane    | shipped — `internal/dataplane/`, blog example, gated by `dataplane.enabled`                      |
| S4 ingest        | seam shipped — mount, chain and `Verifier`; no provider registered, so every delivery is refused |
| S5 dispatch      | shipped — webhook endpoints, blog events; see [`webhooks`](../webhooks/README.md)                |
| S6 cli           | shipped                                                                                          |
| S7 mcp           | shipped — `internal/mcp/` + `mcp/`, gated by `mcp.enabled`; see [`mcp`](../mcp/README.md)        |

A fork that needs none of S4, S5 or S7 ignores them. A fork that needs one implements it
**here**, under this name and this mount, and escalates any rule change upstream rather than
diverging. What it must reuse rather than re-decide:
[`dispatch and cli`](dispatch-and-cli.md#what-a-fork-implementing-s4-or-s5-must-reuse).
