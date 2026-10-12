# Receive a webhook

Surface **S4 ingest** ([`surfaces`](../surfaces/README.md)) — a credential-free machine push from a third party, mounted under `/hooks/{provider}/` and authenticated by the provider's signature. An OAuth redirect that ends in a 302 to a page a person is looking at is **not** ingest; that is [`webpage.md`](webpage.md) (S1). Calling a tenant's endpoint is the opposite direction — [`webhook-out.md`](webhook-out.md) (S5).

This surface is a **seam**: `internal/ingest/` ships the mount, the chain and the `Verifier` interface, and no provider. `buildIngestHandler` in `internal/boot/http.go` passes only `BasePath` and `Log`, so today every delivery answers 404.

## Steps

1. Implement `ingest.Verifier` — `Verify(r *http.Request, body []byte) error` — in your provider's own package. Compare digests with `hmac.Equal`, never `==`, and reject a timestamp outside the provider's replay window.
2. Write the `http.Handler` that verified deliveries reach.
3. Register the pair in `buildIngestHandler`:

   ```go
   Providers: map[string]ingest.Provider{"acme": {Verifier: v, Handler: h}},
   ```

4. In that handler, derive the org from the **verified** payload and enter it explicitly with `tenant.Into` before touching any store.
5. Set `MaxBodyBytes` in `HandlerParams` if the 1 MiB `DefaultMaxBodyBytes` is wrong for the provider.
6. Answer through the surface's own vocabulary. New outcomes get a typed struct plus an `Is<TypeName>Error` helper in `internal/ingest/errors.go` and a row in `statusFor`.
7. `make check` — `internal/ingest/ingest_test.go` holds the fail-closed matrix.

## Tenancy

- **No ambient scope, and that is the whole point.** There is no `RequireProject`, no path slug resolver, no key carrying an org. Every call that would have narrowed the request disappears.
- **What replaces it:** your handler reads the org out of the verified payload and calls `tenant.Into` itself. Nothing else will.
- **What stops protecting you:** provider identity _is_ the authorization here (R4), so nothing gates which org a delivery may name before your handler runs. A weak `Verify` plus an org id taken from the body is a cross-tenant write. RLS only constrains reads and writes _after_ you enter a scope — entering the wrong one is permitted.
- A tenant-scoped store reached before `tenant.Into` fails with `tenant: missing context`.

## Gotchas

- `/hooks/` is mounted unconditionally, even with no providers, so the prefix stays reserved rather than falling through to the console chain (`TestMountPrefixesReserved`).
- The body is read once, bounded, and passed to `Verify`; the handler receives it replayed, so do not re-read `r.Body` expecting the original stream semantics.
- A registered provider with a nil `Verifier` is refused as unverified; one with a nil `Handler` is a 500. Both are deliberate.
- Verification failures are logged with the provider name and answered as one flat 401 (`TestVerificationFailureIsLoggedNotReturned`). Do not leak the reason into the body.
- Ingest does not emit `GEN###` codes — its JSON body is the same opaque outcome vocabulary the data plane uses.

## Contracts

[`surfaces`](../surfaces/README.md) · [`request scope`](../multitenancy/request-scope.md) · [`error codes`](../errors/README.md) · [`architecture`](../architecture/README.md)
