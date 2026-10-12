# Domain layer: aggregate, Store, service

The file set, the depguard purity rules, the `Store` verb list and the `Service` field order are
[`modules`](../../../../docs/modules/README.md#1-files) and
[Section 2](../../../../docs/modules/README.md#2-inside-a-module). Adding a method is
[`howto/service-method.md`](../../../../docs/howto/service-method.md); splitting out a subdomain
is [`howto/subdomain.md`](../../../../docs/howto/subdomain.md).

This file is the design judgement those leave open.

## Aggregate

`New(...)` enforces creation invariants and returns typed domain errors — never a bare
`fmt.Errorf`. Mutations are methods on the aggregate, not logic in the service.

**Prefer idempotent lifecycle methods that return nothing** over ones that error on a repeat.
POST-and-redirect means a double submit is normal:

```go
func (p *Post) Publish()   // sets FirstPublishedAt only when nil
func (p *Post) Unpublish() // returns to draft, retains FirstPublishedAt
```

**Name a field for what it means across every state.** `FirstPublishedAt` survives an unpublish;
`PublishedAt` would invite a consumer to read non-nil as "currently published". Keep one field as
the source of truth for the question people actually ask.

**A setter taking a collection normalises it** — de-duplicate, preserve order (`Post.SetTags`) —
so the store never has to defend against bad input.

An aggregate a second writer can race carries `Version int`, set to `1` by `New(...)`. The
aggregate never bumps it; the adapter's UPDATE does
([`persistence.md`](persistence.md#optimistic-concurrency)).

## Store shapes worth copying

- **`ListOpts`** is a struct of optional filters whose zero value means "everything in scope".
- **Batch lookups return a map keyed by id** —
  `ByIDs(ctx, orgID, projectID, ids) (map[uuid.UUID]*T, error)`, as in
  `internal/blog/category/store.go`. An unresolvable id is simply absent, ordering is the
  caller's business, and it avoids N+1 in list views. Take `orgID, projectID` explicitly: without
  the project the store scopes only by org, which leaks across projects inside one org.
- A versioned aggregate's mutating verbs take the precondition **last** —
  `Save(ctx, p, ifVersion int)`, `Delete(ctx, id, ifVersion int)`. No `*int`, and no second
  "unconditional" method.

## Keep the service pure

A `Service` does not take another module's `Store`. Resolving a related aggregate's display name
is **composition, and composition happens at the edges** — the web handler and the RPC service
already hold every service they need. Aggregation stays with whoever owns the rows being counted
(`blog.Store.CountByCategory` lives in `internal/blog/`, keyed by id, and the handler joins the
two maps).

**Duplicating a small helper across sibling packages beats a shared import.** A slug function is
duplicated in `internal/blog/blog.go`, `internal/blog/category/category.go` and
`internal/blog/tag/tag.go` on purpose: the `domain-purity` allow-list would reject the import.
