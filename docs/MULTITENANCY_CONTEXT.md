# Tenant scope in a request

Every tenant-scoped read and write derives its org from **the URL**, never from session state.
Get this wrong and the failure is quiet: zero rows, or an opaque RLS rejection, or
`tenant: missing context` from somewhere three layers down.

This page is the short version. [`MULTITENANCY.md`](MULTITENANCY.md) covers the database side —
RLS policies, the `current_org_id()` helper, and the `SECURITY DEFINER` wrappers.

## Terms

"Tenant", "context", "scope" and "RLS" get used loosely and live at different layers. They are not
synonyms, and confusing them is how a runtime bug gets mistaken for a database one.

| Term                     | Layer               | What it actually is                                                                                                                             |
| ------------------------ | ------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| **Tenant**               | domain              | One org. A row in `orgs`, and the unit of isolation. "Tenant" and "org" mean the same thing here.                                               |
| **Tenant context**       | Go runtime          | `tenant.Context{OrgID, ProjectID, UserID}` carried on a `context.Context`. What the _application_ believes it is acting as.                     |
| **RLS**                  | Postgres            | Row-level security. Policies on each tenant table compare `org_id` against `current_org_id()`. What the _database_ enforces regardless of code. |
| **Scope** / **scoped**   | both                | The state of being bound to one tenant: a usable tenant context in Go **and**, inside a transaction, the GUC set for RLS to read.               |
| **`app.current_org_id`** | Postgres            | The session variable RLS policies read. Set per transaction by `PgConn.BeginTenanted` via `set_config(..., true)`.                              |
| **BYPASSRLS**            | Postgres role attr. | Skips RLS entirely. The app role must never have it; the owner role does.                                                                       |
| **`SECURITY DEFINER`**   | Postgres            | A function that runs as its owner rather than its caller, so it inherits the owner's BYPASSRLS. How the few pre-scope reads work.               |

The two layers are independent on purpose. The tenant context is what the app _intends_; RLS is what
the database _permits_. A bug in the first shows up as wrong-but-permitted data; RLS is what stops
that becoming a cross-tenant leak. Never treat either as sufficient alone.

## The rule

> A handler resolves its org from the path, gating membership, and passes the resulting
> **request** down. Nothing reads `Principal.ActiveOrgID` except the bare root redirect.

```go
func (h *ThingHandler) GetList(w http.ResponseWriter, r *http.Request) {
    p, _, ok := h.LoadSession(r)
    if !ok {
        http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
        return
    }
    // r now carries the org scope; membership was checked, and a non-member gets the same 404 as a bad slug.
    o, r, ok := h.OrgScopeFor(w, r, p, r.PathValue("org"))
    if !ok {
        return
    }
    items, err := h.Things.List(r.Context(), o.ID)
    ...
}
```

Assigning back into `r` is deliberate: the scoped request replaces the unscoped one, so a later
`r.Context()` cannot accidentally be the wrong scope.

For a project-scoped route, chain the second helper:

```go
proj, r, ok := h.ProjectScopeFor(w, r, o.ID, r.PathValue("project"))
```

## Where scope comes from, by entry point

| Entry point       | Source of scope                                                           |
| ----------------- | ------------------------------------------------------------------------- |
| Web handler       | `Deps.OrgScopeFor` / `Deps.ProjectScopeFor`, from the path                |
| Connect-RPC       | `interceptor.Tenant`, from the session principal                          |
| CLI               | the resolved `--org` flag, via `tenant.Into`                              |
| Scheduler         | one fan-out per tenant, from `tenant.NewEnumerator` — see below           |
| Boot / onboarding | `tenant.WithOrg` on the org being created — there is no ambient scope yet |

## The scheduler

A background job has no request, no session and no URL, so it cannot inherit a scope — it has to
create one per tenant. `scheduler.Job.Scope` decides how:

- **`ScopeSystem`** — `Run` fires once per tick with an unscoped context. It must not touch a
  tenant-scoped store.
- **`ScopeTenant`** — `Run` fires **once per tenant per tick**, each with its own scoped context.

The fan-out is `tenant.Enumerator`, wired in `boot.buildScheduler`. Per tick it calls
`OrgIDs(ctx)`, then invokes the job once per org with `tenant.Into(ctx, tenant.Context{OrgID: id})`.
From the job's point of view the context is already scoped, so it calls its store the same way a
handler does.

Two consequences worth knowing:

- **The enumeration itself is a cross-tenant read**, so it cannot go through RLS. `OrgReader` calls
  the `<prefix>list_org_ids()` `SECURITY DEFINER` wrapper, which runs as the owner role. This is why
  the scheduler needs no separate maintenance login — the wrapper is the privileged surface, and
  migration `005` revokes `EXECUTE` on it from `PUBLIC`.
- **The scope carries no `UserID`.** A job acts as the system, not as a person, so anything that
  attributes an action to a user must take the actor explicitly rather than reading it off the
  context.

Registering a `ScopeTenant` job without `Options.Tenants` fails at startup rather than silently
running unscoped. `db.NewLocker` keeps one replica from running the same job concurrently, and
`Job.Timeout` caps a single invocation — per tenant, not per tick.

## Why the URL and not the session

`projects` is `UNIQUE (org_id, slug)`, so a project slug is unique **within an org** only. A path
like `/projects/default/todos` therefore does not identify a project; only ambient state could
disambiguate it. Keeping the org in the path means:

- a link means the same thing to everyone who opens it
- two browser tabs can sit in different orgs without fighting
- back and forward do what they look like they do
- the tenant scope is a pure function of the request

## Adding a tenant-scoped route

1. Register it under `/orgs/{org}/...` (and `/orgs/{org}/projects/{project}/...` when it belongs
   to a project). Never mount a tenant-scoped page at a bare path.
2. Resolve scope with `OrgScopeFor`, then `ProjectScopeFor`. Do not build a `tenant.Context` by hand.
3. Build links with `LayoutData.OrgPath` / `LayoutData.ProjectPath`. Hand-written `/orgs/...`
   strings in templates drift the moment a route moves.
4. Pass the org to the layout: `LayoutForOrg(r, title, o.Slug, navKey)` or
   `LayoutForProject(r, title, o.Slug, proj, navKey)`. Both switchers key off it — pass nothing and
   they fall back to the session, which is how an org once appeared under both Current and Switch.
5. Add the route to the table in `internal/boot/route_scope_test.go`. It is asserted complete
   against the handler sources, so a new route without a probe fails the build.

## Writes scope themselves

A write whose tenant _is_ the row cannot trust the caller's scope. `orgs` is scoped by its own id,
so `org.Create`, `org.Rename` and `org.RemoveMember` re-point the context with `tenant.WithOrg`
before touching the store. Authorization for those is the membership check in the handler, not RLS.

If you add a service method that takes an `orgID` parameter, scope to that parameter. Do not
assume the caller passed a context naming the same org.

## Failure modes and what they mean

| Symptom                                      | Cause                                                                             |
| -------------------------------------------- | --------------------------------------------------------------------------------- |
| `tenant: missing context`                    | reached a scope-requiring store method with no scope at all                       |
| `tenant: context names no org`               | scope present but `OrgID` is the zero uuid — usually a session with no active org |
| Zero rows, no error                          | scope names the wrong org; RLS filtered everything out                            |
| `new row violates row-level security policy` | inserting a row whose `org_id` differs from the scope                             |

`tenant.From` rejects a zero `OrgID` on purpose. Before that it returned an unusable scope with a
nil error, which turned every one of these into a separate debugging session.

## Store methods that require scope

Derived from the `txAcquire` callers in each domain's `pgreader.go` / `pgwriter.go`. Everything here
opens a tenant-scoped transaction and will fail without a usable scope:

```
invite:  ByID  Delete  ListPending  Save
org:     ByID  ListMemberProfiles  ListMembers  MembershipOf  RemoveMember  Save  SaveMembership
project: ByID  BySlug  List  Save
todo:    ByID  ClearDone  Delete  List  Save
```

`org.BySlug`, `org.List`, `invite.ByTokenHash` and `invite.FindPendingForEmail` are deliberately
**not** in that list: they run through `SECURITY DEFINER` wrappers because they are consulted
before a tenant scope exists — resolving a slug, or an invite, is what establishes the scope.

## Tests that hold this together

| Test                                                  | What it prevents                                           |
| ----------------------------------------------------- | ---------------------------------------------------------- |
| `internal/boot/route_scope_test.go`                   | a route reachable without scope, or a route with no probe  |
| `TestRoutes_NonMemberCannotReachAnotherOrg`           | reading another org by guessing its slug                   |
| `TestOrgSwitcher_PinnedOrgIsNotAlsoOfferedToSwitchTo` | the switcher disagreeing with the page                     |
| `internal/org/definer_integration_test.go`            | writes that only pass because the test role bypasses RLS   |
| `schema/tenant_policy_guard_test.go`                  | a migration inlining the GUC instead of calling the helper |

The route walk runs on SQLite, whose org store scopes by explicit argument rather than by context,
so it cannot see org-domain scope bugs. Those are covered by the Postgres integration tests running
as a **non-BYPASSRLS** role. A test that connects as a superuser proves nothing about RLS.
