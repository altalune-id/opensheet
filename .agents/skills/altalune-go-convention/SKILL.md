---
name: altalune-go-convention
description: Write or review Go code in this multitenant template (opensheet) or a downstream fork, without breaking its conventions — the seven-surface route taxonomy, domain-module shape, tenant scoping and RLS, guarded upserts, optimistic concurrency, typed errors and error codes, migrations, config keys, platform primitives and workers, and boot wiring. Use this whenever adding or changing a route, endpoint, web page, RPC, MCP tool, CLI command, module, aggregate, subdomain, Store method, Service method, migration, config key, worker or error code — or when reviewing such a change. Also use it when the task mentions tenant isolation, org scoping, row level security, ON CONFLICT, If-Match, scopes, or "how do I add X here".
license: Proprietary
metadata:
  reference-impl: internal/blog (relations), internal/todo (flat)
---

# Working in opensheet

`docs/` holds the contracts. [`docs/howto/`](../../../docs/howto/README.md) holds the task
recipes. This skill routes you to the right one, fixes the order you do things in, and carries
the traps that live nowhere else.

## What are you changing?

Find the row, open the recipe, follow it. Do not reconstruct the procedure from memory.

| Change                             | Recipe                                                                     |
| ---------------------------------- | -------------------------------------------------------------------------- |
| a page a person opens (S1)         | [`howto/webpage.md`](../../../docs/howto/webpage.md)                       |
| a control-plane RPC (S2)           | [`howto/internal-api.md`](../../../docs/howto/internal-api.md)             |
| a data-plane REST endpoint (S3)    | [`howto/external-api.md`](../../../docs/howto/external-api.md)             |
| receiving a provider webhook (S4)  | [`howto/webhook-in.md`](../../../docs/howto/webhook-in.md)                 |
| sending an outbound event (S5)     | [`howto/webhook-out.md`](../../../docs/howto/webhook-out.md)               |
| a CLI command (S6)                 | [`howto/cli-command.md`](../../../docs/howto/cli-command.md)               |
| an MCP tool (S7)                   | [`howto/mcp-tool.md`](../../../docs/howto/mcp-tool.md)                     |
| a whole new bounded context        | [`howto/module.md`](../../../docs/howto/module.md)                         |
| a second aggregate inside a module | [`howto/subdomain.md`](../../../docs/howto/subdomain.md)                   |
| a `Service` method                 | [`howto/service-method.md`](../../../docs/howto/service-method.md)         |
| a `Store` method                   | [`howto/store-method.md`](../../../docs/howto/store-method.md)             |
| a cache, queue, limiter or loop    | [`howto/platform-primitive.md`](../../../docs/howto/platform-primitive.md) |
| a queue job or broadcast           | [`howto/queue-consumer.md`](../../../docs/howto/queue-consumer.md)         |
| a config key                       | [`howto/config-key.md`](../../../docs/howto/config-key.md)                 |
| failing from any layer             | [`howto/errors.md`](../../../docs/howto/errors.md)                         |
| a new `<DOM><NNN>` code            | [`howto/error-code.md`](../../../docs/howto/error-code.md)                 |
| reviewing any of the above         | the `altalune-go-review` skill                                             |

Deciding _which_ surface before anything else is mandatory — every route belongs to exactly
one and there is no eighth ([`surfaces`](../../../docs/surfaces/README.md)). The recipes
carry the details; the references in this skill carry what the recipes assume you already
know.

## Order of work for a new module

Each step's verification depends on the previous one, so the order is not a preference.
[`howto/module.md`](../../../docs/howto/module.md) is the numbered procedure; this is why it
is numbered that way.

1. **Schema** — migrations + RLS + jet bindings → [`references/schema.md`](references/schema.md)
2. **Error codes** — before any code constructs one → [`references/errors.md`](references/errors.md)
3. **Domain** — aggregate, `Store`, typed errors, service → [`references/domain.md`](references/domain.md)
4. **Adapters** — `postgres.go` / `sqlite.go` → [`references/persistence.md`](references/persistence.md)
5. **Wiring** — fake, boot, depguard → [`references/wiring.md`](references/wiring.md)
6. **Surfaces** — one recipe per surface the module needs → [`references/surfaces.md`](references/surfaces.md)
7. **Verify** — `scripts/verify.sh`

Within a step the loop is test-first ([`CONTRIBUTING.md`](../../../CONTRIBUTING.md)): the
service test against the fake exists before the `Store` verb does.

`scripts/scaffold.sh <name>` copies `internal/todo/` and renames the package, type and
identifiers. It produces a compiling-but-wrong module — todo's invariants, columns and error
codes come along and must be replaced.

## Rules that are expensive to get wrong

Each of these caused a real defect here. The explanation lives in the doc; the rule is stated
so you cannot miss it.

- **Every query carries an explicit `org_id` predicate from `tenant.From(ctx)`.** RLS is the
  backstop, not the guard — [`multitenancy`](../../../docs/multitenancy/README.md#two-guards-not-one).
- **An upsert's conflict clause carries the tenant predicate**, plus a `RowsAffected() == 0`
  branch returning `&NotFoundError{}` —
  [`howto/store-method.md`](../../../docs/howto/store-method.md#gotchas).
- **A service method taking a bare id checks org _and_ project.** The store filters by org
  only — [`howto/service-method.md`](../../../docs/howto/service-method.md).
- **A conditional write is one write.** Compose on the aggregate and `Save` once;
  `ifVersion 0` means unconditional and there is no second method —
  [`modules`](../../../docs/modules/README.md#4-optimistic-concurrency).
- **Typed errors only**, one struct per failure mode, helper `Is<FullTypeName>` — never
  `IsErr*`. Codes are append-only —
  [`error codes`](../../../docs/errors/README.md).
- **Never wrap jet's `NULL` singleton.** Use the `Null*` helpers in
  `internal/platform/db/entity/{postgres,sqlite}`, matched to the column's declared type.

## Tests that cannot fail

This is the failure class that survives a plan, a spec review and a green suite. Nothing in
`docs/` will catch it for you; ask these questions yourself.

**A security guard's test must run where the guard is the only protection.** A hijack test on
an RLS-enforcing Postgres fixture cannot detect its own guard's removal — RLS refuses the
write either way. Put those on SQLite or a superuser fixture, and prove each one by reverting
the guard and watching it fail.

**A fake must not enforce what the test is proving.** A fake that filters by project makes
every project-scope test pass regardless of the production guard. The mirror half: a versioned
`Store`'s fake _must_ honour `ifVersion`, or every concurrency test is vacuous. Both halves
matter. Detail and the pinning tests:
[`references/wiring.md`](references/wiring.md#fake-store).

**A guard with no coverage looks exactly like protection.** A `WHERE` clause or a depguard
glob that matches nothing passes silently. Break it on purpose and confirm the failure.

**A route probe is not a feature test.** `probeRoutes()` stops at project resolution and never
enters a handler body. More of these in the `altalune-go-review` skill, `references/convention.md`.

## Verifying

`scripts/verify.sh` runs the gates in dependency order and explains each failure. It takes
`--integration` to add the Postgres suite, and `--check` to run read-only (it regenerates and
formats files otherwise). Run it before claiming a module is done.

Integration tests need Postgres. Set `TEST_PG_DSN` at a throwaway database and use `-p 1` —
without it, packages race on `CREATE/DROP ROLE` and cleanup fails. Without `TEST_PG_DSN` each
`pgtest.New` starts its own container and the suite takes ~25 minutes instead of ~2.

What `verify.sh` cannot check, and you still must do: open the page, run
`bash scripts/verify-serve-smoke.sh` (and `verify-mcp-smoke.sh` for S7), and revert each
security guard to confirm a test fails. Type checking passing is not evidence a surface works.
