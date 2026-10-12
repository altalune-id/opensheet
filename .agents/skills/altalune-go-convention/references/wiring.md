# Wiring: fakes, boot, config, depguard

Boot wiring per surface is in that surface's recipe
([`howto/`](../../../../docs/howto/README.md)); the two-file service wiring is
[`howto/module.md`](../../../../docs/howto/module.md#steps) step 8; the one-instance rule is
[`modules`](../../../../docs/modules/README.md#5-surfaces). This file is what none of
them say.

## Fake store

`internal/testutil/fakes/<name>.go` — a hand-written in-memory `Store`, mutex-guarded, with
`var _ <name>.Store = (*<Name>)(nil)` so it cannot drift from the interface.

No `sqlmock`, no `gomock`, no `testify/mock`. `testify/require` and `assert` are fine and used
throughout — the rule ([`CONTRIBUTING.md`](../../../../CONTRIBUTING.md)) is no mocks, not no
testify.

Expose function fields (`DeleteFn func(...) error`, as in `fakes/category.go`) for the one or two
behaviours a test needs to override, rather than building a configurable fake.

**A fake must not enforce what the test is proving.** A fake that filters by project makes every
project-scope test pass regardless of the production guard — the real stores filter by org only,
and the project check belongs to the `Service`. Assert the non-filtering in the fixture: a raw
`store.ByID` that must return the row.

**The mirror half: a versioned `Store`'s fake _must_ honour `ifVersion`**, or every concurrency
test is vacuous. It mirrors the real write contract — insert with the caller's version, on
conflict `SET version = version + 1`, a nonzero `ifVersion` that misses returns
`*StaleVersionError`. Both halves are pinned by
[`howto/store-method.md`](../../../../docs/howto/store-method.md#the-fake-is-not-free-to-differ).

## Boot

Everything HTTP is assembled in `internal/boot/http.go` — `buildWebHandler` (console, via the
`AppHandlers` slice), `buildAPIHandler` (control plane), `buildDataHandler` (data plane) — and
MCP in `internal/boot/mcp_tools.go`. Each surface's recipe names the exact edits.

**Adding a module touches no middleware chain.** Chains are per surface, built in
`boot.surfaceChains` and carried as `web.SurfaceChains`
([`surfaces`](../../../../docs/surfaces/README.md#middleware-chains)). There is no global chain — if
you think a module needs one changed, the route is on the wrong surface.

**Do not register a scheduler provider unless the module has periodic work.**
`internal/boot/schedulers.go` holds a `schedulerDomains` manifest and `assertSchedulerWiring`
fails the boot when the provider list and the domain list disagree. Adding one half breaks
startup; adding neither is correct for most modules. Shape:
[`modules`](../../../../docs/modules/README.md#6-periodic-work).

Boot also fails closed on a half-wired MCP tool and on a data-plane port with no shim; both are
in their recipes.

## Config

The full procedure, the env-binding walk and the awareness tags are
[`howto/config-key.md`](../../../../docs/howto/config-key.md). One thing it does not say:

**Add a new cross-field validator last in its chain.** `validateInvariants` runs its checks in
order, so putting a new one first changes which error an existing misconfiguration reports and
breaks tests asserting the old message.

## depguard

`domain-purity` in `.golangci.yaml` restricts imports in aggregate files. Its `files` globs are
path-shaped and `*` does not cross a `/`, so a subpackage needs its own entry:

```yaml
- "**/internal/*/<name>.go"
- "**/internal/<name>/*/{sub1,sub2,store,errors}.go"
```

Prefer a narrow alternation over `**/internal/*/*/errors.go`, which would also capture
`internal/platform/*/errors.go` and fail lint later for an unrelated-looking reason. A surface
package that collides with a domain filename (`internal/dataplane/*.go`, `internal/mcp/auth.go`)
is excluded with a `!` line, not by widening the allow list.

**Prove the glob bites.** Add a genuinely disallowed third-party import — the allow list includes
`$gostd`, so a stdlib one proves nothing — and confirm `make lint` fails.
