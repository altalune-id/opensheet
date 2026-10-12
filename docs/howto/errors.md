# Raise an error

Use this when code in a domain or platform package needs to fail. Registering a brand-new code is
a separate recipe — [`error-code.md`](error-code.md); the registry itself is
[`error codes`](../errors/README.md).

## Steps

1. **Add one struct per failure mode** to the package's `errors.go`. Reference impl:
   `internal/blog/errors.go`. Fields carry the identifying detail, not a sentence.
2. **Name the helper `Is<FullTypeName>`** — `NotFoundError` → `IsNotFoundError`. Never `IsErr*`,
   never a shortened form.
3. **Add `ToAppError() *apperror.AppError`** to anything that can reach a wire.
   `apperror.AsAppError` walks the chain hop by hop looking for that method, so a struct without
   one falls through to a generic 500.
4. **Route unexpected failures through `apperror.UnexpectedFunc`** — the func-typed dependency a
   `Service` takes at construction (`internal/blog/service.go`). It logs the cause, ticks the
   incident counter and returns a `GEN900` envelope.
5. **Wrap with `%w` on the way up** — `fmt.Errorf("blog.postgres.Save: %w", err)`. Never swallow
   and never return a fresh error that drops the cause: the `Is…` helpers walk the chain.
6. **Never `panic` for an expected failure mode.**

```go
// IsNotFoundError reports whether err's tree contains a *NotFoundError.
func IsNotFoundError(err error) bool {
	_, ok := errors.AsType[*NotFoundError](err)
	return ok
}
```

`errors.AsType` is the form new code uses; `errors.As` still appears at older call sites.

## Where the code actually travels

Full table: [`error codes`](../errors/README.md#where-a-code-travels). The part contributors
get wrong:

| Surface                                       | Carries `<DOM><NNN>`? |
| --------------------------------------------- | --------------------- |
| console S1, control plane S2, CLI S6, MCP S7  | yes                   |
| data plane `/api/v1/` S3, ingest `/hooks/` S4 | **no**                |

S3 and S4 answer `{"code","message"}` where `code` is an opaque outcome word — `not_found`,
`unauthorized`, `bad_request`, `conflict`, `in_progress`, `precondition_failed`,
`precondition_required`, `method_not_allowed`, `payload_too_large`, `internal`. Each surface picks
it in its own `statusFor`: `internal/dataplane/errors.go` for S3, `internal/ingest/ingest.go` for
S4. A domain error reaches that body only if `statusFor` has a case for it — anything unrecognized
becomes `500 internal`. Exposing a new domain failure mode on S3 means adding a case there as well
as the typed struct.

## Safe to return, versus replace with a generic one

- **Validation and conflict outcomes are safe** — the caller supplied the input.
- **An unresolvable scope, a missing row and a denied authorization must answer identically.**
  `resolver.resolve` in `internal/dataplane/scope.go` returns the same `&NotFoundError{}` for all
  three, so nobody can enumerate org or project slugs by reading statuses. Report "absent", never
  "forbidden".
- **A draft post is a 404, not a 403** — confirming a slug exists is itself a disclosure. R9 in
  [`surfaces`](../surfaces/README.md).
- **Never put an infrastructure message on the wire.** `UnexpectedFunc` logs the cause and returns
  the generic envelope; outside development the cause is not echoed.
