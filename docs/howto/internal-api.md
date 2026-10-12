# Add a control-plane RPC

Surface **S2 control plane** ([`surfaces`](../surfaces/README.md)) — Connect-RPC under `/api/`, called by your own CLI or an integrator's automation holding a bearer JWT or an api key. If the caller is an integrator's running product, it belongs on [`external-api.md`](external-api.md) (S3); if it is a browser, on [`webpage.md`](webpage.md) (S1). Publishing an existing RPC to an MCP host is [`mcp-tool.md`](mcp-tool.md).

Worked example: `internal/controlplane/blog_service.go` + `api/blog/v1/blog.proto`.

## Steps

1. Add the `rpc` and its request/response messages to `api/<module>/v1/<module>.proto`.
2. `make generate` — regenerates `gen/go/…` and `gen/openapi/…`.
3. Implement the method on the service in `internal/controlplane/<module>_service.go`. Resolve scope first:

   ```go
   tctx, err := s.scopeToProject(ctx, req.Msg.GetProjectId())
   ```

4. Add the procedure-to-scope row in `internal/controlplane/scopes.go` (`ScopeTable`), keyed by the generated `…Procedure` constant.
5. Add the verb row in `internal/controlplane/registry.go` (`VerbTable`), as `{Module, Aggregate, Operation}`.
6. A brand-new service additionally needs, in `internal/controlplane/server.go`: a field plus its constructor call in `New`, a `New<X>ServiceHandler` mount in `Handler`, a `serviceProcedures(...)` entry in `MountedProcedures`, and an interface assertion in the `var (...)` block.
7. `make check`.

## Tenancy

- **Project-scoped (default)** — `scopeToProject` reads the principal, loads the project under `p.ActiveOrgID`, and returns forbidden when `proj.OrgID` differs. A `projectId` argument narrows; it never re-targets.
- **Org-scoped** — drop the `scopeToProject` call. `interceptor.Tenant` has already put `ActiveOrgID`, `ActiveProjectID` and `UserID` on the context from the principal, so the method runs org-wide.
- **Not tenant-scoped** (`AuthService.Whoami`) — no scope call at all. Nothing narrows the context, so the method must not touch a tenant-scoped store; one that does fails with `tenant: missing context`, and any that succeeds is unfiltered by RLS.
- An api key's org and project are fixed when it is minted. No request argument can widen them.

## Gotchas

- A procedure missing from `ScopeTable()` is **denied** to key principals, not admitted. `TestEveryRPCHasAScope` fails the build on any mounted procedure with no entry.
- A JWT principal is scope-exempt on this surface. Do not generalize that to S7, where every tool is scope-checked.
- `VerbTable()` is hand-maintained. `TestControlPlaneVerbTableMatchesScopeTable` fails on a stale row, and `TestOneVerbOnePlane` fails if the same `{Module, Aggregate, Operation}` is already registered on S3 — only `blog` is allowlisted for both.
- The error code travels in `ErrorDetail.code` inside the Connect error; a new code needs a constant in `internal/apperror/codes.go` and a row in [`error codes`](../errors/README.md), which `TestCodes_EveryRefIsDocumented` checks in both directions.
- `api.enabled=false` leaves the whole surface unmounted.

## Contracts

[`surfaces`](../surfaces/README.md) · [`scopes`](../scopes/README.md) · [`request scope`](../multitenancy/request-scope.md) · [`error codes`](../errors/README.md) · [`modules`](../modules/README.md)
