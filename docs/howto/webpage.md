# Add a console page

Surface **S1 console** ([`surfaces`](../surfaces/README.md)) — a page a signed-in person opens in a browser, including an OAuth redirect that ends in a 302. A machine caller belongs on [`internal-api.md`](internal-api.md) (S2) or [`external-api.md`](external-api.md) (S3); a credential-free provider push belongs on [`webhook-in.md`](webhook-in.md) (S4).

Worked example: `internal/web/handlers/blog.go` + `internal/web/templates/categories.templ`.

## Steps

1. Add a handler method to `internal/web/handlers/<module>.go`. Its first statement resolves scope:

   ```go
   sc, ok := h.RequireProject(w, r)
   if !ok {
       return
   }
   ```

2. Use `sc.req` for everything downstream — `sc.req.Context()`, `sc.org`, `sc.project`. The original `r` is the unscoped request.
3. Render through `Render(w, sc.req, templates.X(h.LayoutForProject(sc.req, title, sc.org.Slug, sc.project, "<navKey>"), v))`. Org-only pages use `LayoutForOrg`.
4. Write the `.templ` under `internal/web/templates/`, then `make generate`.
5. Register the route on the handler's `Register(mux web.Mux)` method, under `/orgs/{org}/projects/{project}/…`. A bare path is for personal pages only (see Tenancy).
6. Wire the handler in `internal/boot/http.go` — construct it in `buildWebHandler` and add it to the `AppHandlers` slice.
7. Add the nav entry in `internal/web/templates/layout.templ` using `d.ProjectPath(slug, "/x")`, `d.Tr("nav.x")` and an `ActiveNav.ProjectKey` match. Add the key to every file in `internal/i18n/locales/active.*.yaml`.
8. Add the route to `probeRoutes()` in `internal/boot/route_scope_test.go`.
9. `make check`, then `make dev` and open the page. Type-checking passing is not the feature working.

## Tenancy

- **Project-scoped (default)** — `RequireProject` gates org membership, then looks the project slug up inside that org. Membership is org-level, so it admits any project in an org the caller belongs to.
- **Org-scoped** — `RequireOrg` instead; you get `sc.org` and `sc.req` but no `sc.project`, and the route drops the `/projects/{project}` segment.
- **Personal** (`/settings/…`) — `RequireSignedIn` for the caller alone; `MemberOrgScopes` for one scoped request per org they belong to; `RequireOrgFrom` when a form or query field names the org. The last runs the same membership gate as `RequireOrg`.
- **Not tenant-scoped** (`/login`, `/terms`, `/welcome`) — neither call appears, and the route carries no `{org}` segment. Nothing checks membership, and the request context enters no tenant scope, so any tenant-scoped store method reached from there fails with `tenant: missing context`. RLS stops being a second line of defence because there is no scope for it to narrow.

## Gotchas

- Passing `r` instead of `sc.req` silently runs the work in the unscoped context.
- Never call `OrgScopeFor` / `ProjectScopeFor` yourself. `TestTenantGateIsNotCopiedIntoHandlers` fails the build on a handler that rebuilds the gate.
- Every `<script>` carries `nonce={ d.Nonce }`, and every htmx attribute carries `hx-nonce={ d.Nonce }`. Under an enforcing CSP the shim strips unnonced htmx attributes off swapped-in fragments, so the fragment renders but does nothing (`TestBase_CSPEnforced`).
- A translation key the handler picks at runtime needs a standalone `//i18n:use <key>` or `//i18n:use <prefix>.*` comment, or `make i18n-check` reads it as dead.
- A route with no `probeRoutes()` entry and a probe with no route both fail `TestRoutes_ListMatchesTheMux`.

## Contracts

[`surfaces`](../surfaces/README.md) · [`request scope`](../multitenancy/request-scope.md) · [`architecture`](../architecture/README.md) · [`modules`](../modules/README.md)
