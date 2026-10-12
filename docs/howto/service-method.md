# Add a Service method

Use this when the behaviour belongs to a module that already exists and the aggregate already holds
— or nearly holds — the rule it needs.

Reference impls: `internal/blog/service.go` (versioned, relations) and `internal/todo/service.go`
(flat). The layer rules are [`architecture`](../architecture/README.md); the `Service` shape is
[`modules`](../modules/README.md) Section 2.

## Steps

1. If the method mutates state, name the verb on the aggregate first — `internal/blog/blog.go`,
   e.g. `Post.Publish()`. Invariants live there, never in the `Service`.
2. Write the failing test in `<module>/service_test.go` against the fake
   (`internal/testutil/fakes/<Name>`), before any store work. That is step 1 of the
   [`CONTRIBUTING.md`](../../CONTRIBUTING.md) loop, and it is what lets the method exist before the
   `Store` verb does.
3. Add the method to `service.go`. Open a span first:

   ```go
   ctx, span := tracer.Start(ctx, "blog.UpdateWithTags")
   defer span.End()
   ```

4. Read the scope: `tc, err := tenant.From(ctx)`. Return the error; never `panic`, never
   `MustFrom`.
5. A method taking a bare id **also checks project after loading** — the store filters by org only,
   so without it a sibling project's row is reachable. Shape: `category.Service.ByID` in
   `internal/blog/category/service.go`.
6. Expected failures return the typed error unchanged. Everything else goes through
   `s.unexpected(ctx, "<module>.<Method>: <situation>", err, "post_id", id)`.
7. Needs a new `Store` verb? [`store-method.md`](store-method.md).
8. The method is unreachable until a surface calls it. Pick one — [`surfaces`](../surfaces/README.md).
9. `make check`.

## Hard rules

**A conditional write is one write.** Load, mutate the aggregate, `Save(ctx, p, ifVersion)` once.
`blog.Service.UpdateWithTags` exists for exactly this: a conditional body write followed by an
unconditional tag write let two writers interleave — the loser's precondition failed while its tags
landed anyway. Pinned by `internal/blog/update_atomic_test.go`. Never add a second method that
writes an aggregate's satellites separately.

**`ifVersion` is a request field.** It threads from the surface to the store untouched; `0` writes
unconditionally and there is no separate unconditional method. A mismatch is `*StaleVersionError`.

**A new failure mode is a new error struct**, plus `Is<FullTypeName>Error`, plus a constant in
`internal/apperror/codes.go` and a row in [`error codes`](../errors/README.md). Never `IsErr*`.

**3+ dependencies beyond `store/log/unexpected`** means the method becomes its own struct in its own
file — [`modules`](../modules/README.md) Section 7.

## Tenancy

Tenant-scoped is the default. A creating method takes `tc.OrgID` / `tc.ProjectID` from the context
and never from a parameter — a parameter scopes _within_ a tenant, it does not establish one. How a
request acquires its scope: [`request scope`](../multitenancy/request-scope.md).

On an unscoped module (`user`, `org`) the `tenant.From(ctx)` call disappears, and with it the
org/project comparison after a load. The id alone becomes the authority, so any caller that reaches
the service reaches every row in the table. Nothing downstream replaces that check.

## Gotchas

- A span with no error recorded hides the failure from traces. `span.RecordError(err)` on every
  branch that returns one.
- `s.unexpected` on an expected error buries a user-visible code under an internal one. Branch on
  the `Is*` helpers first — see `Service.persist` in `internal/blog/service.go`.
- Naming another module's aggregate by pointer breaks `application-purity` in `.golangci.yaml`.
  Cross-reference by UUID.
- Type checking is not evidence. Run the surface.
