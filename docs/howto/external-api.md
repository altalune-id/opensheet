# Add a data-plane REST endpoint

Surface **S3 data plane** ([`surfaces`](../surfaces/README.md)) — REST under `/api/v1/orgs/{org}/projects/{project}/…`, called by an integrator's running product with an API key. A JWT is a 401 at the door. Your own CLI and integrator automation belong on [`internal-api.md`](internal-api.md) (S2); a browser page on [`webpage.md`](webpage.md) (S1).

Worked example: `internal/dataplane/blog.go`, ported through `internal/boot/shims.go`.

## Steps

1. Add the method to the driven port in `internal/dataplane/dataplane.go` (`Posts` is the shipped one). The surface never reaches a `Store`, and never imports the domain package.
2. Adapt the domain service to that port in `internal/boot/shims.go` (`blogServiceForDataplane`). The tenant scope on the context is authoritative there, so the ids the port passes are not re-supplied to the service.
3. Write the handler in `internal/dataplane/<module>.go`. A read starts with `h.begin(w, r)`; a write starts with `h.beginWrite(w, r)`, which refuses an uncredentialed request before the path is resolved.
4. Authorize before anything observable happens: `h.authorize(req, authn.ScopePostsWrite, resourceID)` for a resource-narrowed check, `h.authorizeProject(req, authn.ScopePostsRead)` for a project-wide one. Both map failure to `&NotFoundError{}`.
5. Reads set `ETag` from `etagFor(version)` and answer `If-None-Match` with a 304.
6. Mutations read `ifMatchVersion(r.Header.Get("If-Match"))`. A missing header is `PreconditionRequiredError` (428), an unparseable one `BadRequestError` (400), and a stale version surfaces as 412 through `blog.IsStaleVersionError` in `statusFor`.
7. Creates honour `Idempotency-Key`: `h.idem.reserve`, then `defer h.idem.release`, then `h.idem.store` once the write succeeded.
8. Route it in `NewHandler` and add the matching row to `VerbTable()` in `internal/dataplane/registry.go`.
9. If the CLI should walk it, extend `internal/dataplane/client.go` and `internal/cli/blog.go` — R11 says the CLI is a client of the plane, not a second owner of the verb.
10. `make check`.

## Tenancy

- **Path first, then the key must agree (the only shape here).** `resolver.resolve` in `internal/dataplane/scope.go` turns the two slugs into a `tenant.Context`, and `Authorize` / `AuthorizeProject` then refuse unless the key's own `OrgID` **and** `ProjectID` equal the resolved pair. A non-empty `ResourceIDs` narrows to named resources; `AuthorizeProject` rejects a resource-pinned key outright.
- **There is no unscoped route on this surface** — the mount itself carries `{org}` and `{project}`.
- **Uncredentialed reads** are the one variation. `req.raw == ""` means no `Authorize` call happens at all: the read requires `h.caps.PublicReads` **and** a published row, and is a 404 otherwise, never a 403. The context carries no `UserID`, so RLS is the only thing still constraining the query. Every write requires a credential regardless of the flag (`TestPublicFlagNeverOpensWrites`).

## Gotchas

- R8 fixes the path shape: a state transition is `POST …/{slug}/publish`; a thing with its own lifecycle is a created resource. Never a colon verb.
- Body-shape errors are decoded but held until after `authorize`, so a bad key cannot tell 400 from the masked 404.
- Idempotency is per-process, capped and TTL-bounded — not durable, not shared between replicas. A retry landing elsewhere is served as a new request.
- The JSON error vocabulary is outcome words (`not_found`, `precondition_required`, …), deliberately opaque. `GEN###` codes do not appear here.
- An unrouted path under `/api/v1/` answers in this surface's envelope, not the mux default, and a known path on the wrong method gets an `Allow` header plus a 405 (`TestUnroutedRequestsKeepTheR6Envelope`).
- `TestDataPlaneVerbTableMatchesRegisteredRoutes` fails on a route with no `VerbTable()` row and on a row with no route.
- `dataplane.enabled=false` makes `buildDataHandler` return nil and the mount disappears.

## Contracts

[`surfaces`](../surfaces/README.md) · [`scopes`](../scopes/README.md) · [`request scope`](../multitenancy/request-scope.md) · [`error codes`](../errors/README.md) · [`architecture`](../architecture/README.md)
