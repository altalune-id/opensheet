# Extend a module with a subdomain

Use this when a bounded context grows a second aggregate. Reference impls: `internal/blog/category/`
and `internal/blog/tag/` under `internal/blog/`.

## Decide first

Add files to the existing package unless **all three** hold: the thing has its own aggregate root
with its own invariants, its own `Store`, and its own table
([`modules`](../modules/README.md) Section 8). More files in one package is not a
problem; a subpackage that shares the parent's table is.

## Steps

1. Create `internal/<name>/<sub>/` with the same file set as a module —
   [`modules`](../modules/README.md) Section 1. The package name is the domain term:
   `category`, not `blogcategory`.
2. Its table goes in the parent's migration or a new one, both dialects, with its own RLS block and
   `VERSION` bumps. `006_blog.sql` carries `blog_posts`, `blog_categories`, `blog_tags` and the
   `blog_post_tags` join table together. Then `make tenant-tables`.
3. Jet bindings per dialect:
   `internal/platform/db/entity/{postgres,sqlite}/<table>.go`.
4. Its own `errors.go` and its own error-code block — `CAT001`+ for categories, `TAG001`+ for tags,
   in `internal/apperror/codes.go` and [`error codes`](../errors/README.md).
5. Store and adapters: [`store-method.md`](store-method.md). Service methods:
   [`service-method.md`](service-method.md).
6. Extend the `domain-purity` globs in `.golangci.yaml`. The rule names subdomain files explicitly
   (`**/internal/blog/*/{category,tag,store,errors}.go`), so a new subdomain is invisible to the
   import check until it is listed.
7. Fake at `internal/testutil/fakes/<sub>.go`; construct the service and store in
   `internal/boot/services.go` with its own field on `Services` — a subdomain is its own service, not
   a method on the parent's.
8. Sibling-private helpers go under `internal/<name>/<sub>/internal/`, which only `<sub>/` and its
   children can import.
9. `make check`, then `make test-integration`.

## Cross-references

**By UUID, never by pointer.** `Post.CategoryID uuid.UUID`, not `*category.Category`. The parent's
`postgres.go` may not import the subdomain for persistence — `depguard`'s `domain-purity` rule
denies it, and the coupling would make each store responsible for the other's tenant scoping.

Aggregation across the boundary stays with whoever owns the rows being counted:
`blog.Store.CountByCategory` and `CountByTag` live in `internal/blog/`, keyed by id, and the handler
joins the two maps.

## Tenancy

A subdomain of a tenant-scoped module is tenant-scoped the same way — `org_id` and `project_id` on
the table, an RLS policy, `tenant.From(ctx)` in the service, an explicit `org_id` predicate in every
query. [`multitenancy`](../multitenancy/README.md).

A join table needs its **own** `org_id` column: a policy needs a column to scope on, and without one
the join is a cross-tenant read that RLS cannot see. `blog_post_tags` carries it, plus a composite
`FOREIGN KEY (post_id, org_id)` so a link cannot name a post in another org.

If the new table has no `org_id`, it does not belong under a tenant-scoped module. A global
aggregate is a module of its own at `internal/<name>/` — [`module.md`](module.md).

## Gotchas

- **Prefer `ON DELETE RESTRICT` over `CASCADE` for a reference the user owns.** Deleting a category
  that still has posts must fail loudly as a typed `InUseError` (`CAT004`), not silently delete the
  posts. `006_blog.sql` does this for `blog_posts.category_id`.
- A method taking a bare id still checks org **and** project after loading — the store filters by
  org only. `category.Service.ByID` is the shape.
- A subdomain's `Delete` that never reads the row first will destroy a sibling project's row.
- Splitting too early costs a package boundary that has to be undone. When in doubt, add files.
