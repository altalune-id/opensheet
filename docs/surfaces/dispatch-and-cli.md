# Dispatch and CLI

R10 and R11 in full — the egress surface (S5) and the CLI (S6), plus what a fork implementing
S4 or S5 must reuse. Taxonomy, mounts and the R1–R11 summary: [`surfaces`](README.md). Ingress
rules: [`plane rules`](plane-rules.md).

### R10 — dispatch, in full

S5 dials a **tenant-controlled URL from inside our network** — a confused-deputy vector before
it is a delivery mechanism, so the transport rule comes first. Procedure:
[`howto/webhook-out.md`](../howto/webhook-out.md).

**Transport.** Dispatch builds its client with `httpclient.New(...)` and never a bare
`http.Client`. The package installs `rejectPrivate` as the dialer's `ControlContext`
(`httpclient/httpclient.go:45`, `httpclient/safe.go:48`), which refuses loopback, link-local,
private and CGNAT ranges at **connect** time — so it also defeats DNS rebinding, because the
check runs on the resolved address rather than the hostname. `httpclient.New` follows
redirects, and the dial guard runs on every hop; the webhook deliverer refuses them anyway —
`webhook.NewDeliverer` sets `CheckRedirect` to `http.ErrUseLastResponse`, so a 3xx is a failed
delivery. (`httpclient.NewResty` refuses redirects unless `AllowRedirects`; S5 does not use it.)

**Private endpoints in self-hosted forks.** Self-hosted tenants do legitimately run receivers
on internal networks, and `httpclient.WithAllowPrivateHosts(true)` opens this up. The template
exposes it as a **code-level option only** — no config key here. A fork that needs it
operator-tunable adds one tagged `awareness:"mode:selfhosted"` and defaulting to **false**. It
is an **operator** decision, never a tenant one: a tenant able to widen it by supplying a URL
is exactly how SSRF works.

**Signature.** `HMAC-SHA256` over `timestamp || "." || raw_body`, where `timestamp` is decimal
Unix seconds — the same string sent in the header, so a verifier recomputes from what it
received. Signature is lowercase hex. Grammar, pinned:

```
X-Opensheet-Timestamp: 1758153600
X-Opensheet-Signature: v1=<hex>
X-Opensheet-Signature: v1=<hex-primary> v1=<hex-secondary>   # during rotation only
```

Space-separated, **primary first**; a verifier accepts the delivery if any listed signature
matches. Guidance published to tenants ([`webhooks`](../webhooks/README.md#signature)): compare
with `hmac.Equal`, never `==` or `bytes.Equal` on a decoded value; reject a timestamp outside a
**±5 minute** window.

**Rotation.** An endpoint holds up to two active secrets. Dispatch signs with the primary and
emits both during the rotation window so a tenant can roll without dropping deliveries;
retiring the old secret is an explicit action.

**Delivery goes through the durable outbox, never inline from a request handler.** That
primitive is `internal/platform/outbox/`: a tenant-scoped table holding
`{id, event_id, org_id, project_id, target, payload, attempt, next_attempt_at, status, last_error}`;
`outbox.Worker`, which claims due rows per tenant — safe against two replicas — delivers
through a `Deliverer` and records the outcome; and `outbox.Backoff`, a fixed step table with
±10% jitter and bounded by `outbox.MaxAttempts`, with terminal rows retained as the delivery log. Its tenant
fan-out is the same `tenant.Enumerator` the scheduler uses
([`request scope`](../multitenancy/request-scope.md)). The sender is `internal/webhook`: boot
always registers the worker with `webhook.NewDeliverer`. `UNIQUE (org_id, event_id, target)`
makes a repeat enqueue a no-op; tenants treat deliveries as idempotent and dedupe on the
delivery id (`X-Opensheet-Delivery-Id`). Receiver contract — envelope, headers, retry schedule:
[`webhooks`](../webhooks/README.md). Adding an event: [`howto/webhook-out.md`](../howto/webhook-out.md).

### R11 — the CLI is a client, not a plane

S6 owns no verbs of its own. When a CLI command performs an operation the data plane already
exposes, it calls S3 — it does not get a private RPC mirroring a public REST route. This keeps
R1 intact with no new exception, and it buys something else: the CLI walks the integrator's
exact path, so a break in the public contract breaks your own tooling first. Procedure:
[`howto/cli-command.md`](../howto/cli-command.md).

One client shape is shipped:

| Client                | Speaks      | Credential | Built by                  | Reference command  |
| --------------------- | ----------- | ---------- | ------------------------- | ------------------ |
| `controlplane.Client` | Connect, S2 | Bearer JWT | `internal/boot/client.go` | `opensheet todo …` |

opensheet's CLI has no data-plane caller: integrators call `/api/v1/` with an API key, as
[`opensheet`](opensheet.md) describes.

**A client, not a peer of the server.** Single-user, local-first CLIs own their protocol
session in the CLI process and keep state on the operator's disk. Borrow their **ergonomics** —
a saved default, a per-invocation override flag, a streaming mode for long reads — never their
architecture: here the session, the connection and the state live **server-side**, and the CLI
holds credentials and nothing else.

> **Rule:** a CLI must never open a long-lived protocol connection that the server also owns.
> Two processes holding one upstream registration corrupt it. A package implementing such a
> stateful adapter is imported by the server, never by `internal/cli`.

**Resource selection is tenant-scoped.** A flag naming a resource names it inside the active
project, so a saved default is a `{url, org, project, resource}` tuple. A bare slug must never
resolve without its project.

**Output, retargeting and credential binding are already contracts of this repo.** Output is
`--output=text|json|ndjson` through `internal/cli/render` — do not adopt another tool's output
flags, `ndjson` already covers the streaming case. Every fork is self-hostable, so the CLI is
retargetable without editing a config file: a root persistent `--url` / `OPENSHEET_URL` and named
profiles (`--profile` / `OPENSHEET_PROFILE`, `internal/cli/url.go`, `internal/cli/profile.go`), each
bound to the URL it signed in to, which is why the CLI answers `ProfileHostMismatchError` rather
than forwarding a profile's token to a server named on the command line. The envelopes and the exit codes are [`cli`](../cli/README.md);
the flags, the precedence, the `healthz` carve-out and that SECURITY note are
[`cli resolution`](../cli/resolution.md).

### What a fork implementing S4 or S5 must reuse

S4 is a seam; S5 ships with the blog events. A fork registers providers
([`howto/webhook-in.md`](../howto/webhook-in.md)) and adds events
([`howto/webhook-out.md`](../howto/webhook-out.md)); it does not re-decide the shape. **Delivery** goes through `internal/platform/outbox`, not a
second queue and never inline from a request handler; **transport** is `httpclient.New(...)`,
never a bare `http.Client`; **private endpoints** are an operator decision, never settable by a
tenant; **signature** is the pinned grammar — do not invent a header. All four are R10 above.

**Stateful adapters** — a long-lived provider socket or stream — are driven adapters
supervised by `worker/`, not surfaces. If such a package imports `net/http` for an inbound
route, the hexagon has broken ([`architecture`](../architecture/README.md)).
