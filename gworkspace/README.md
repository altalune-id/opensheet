# gworkspace

A shared client layer over `google.golang.org/api` for Google Workspace
services: one transport, one set of provider error types, and per-service
clients in subpackages. Sheets (`sheets/v4`), Docs (`docs/v1`), Slides
(`slides/v1`) and Drive (`drive/v2`, `drive/v3`) all ship inside that single
module, so adding a service here costs zero new dependencies.

Google Photos does **not**. Its Library API was split out of
`google.golang.org/api` — there is no `photoslibrary` package in
`google.golang.org/api@v0.297.0` — so it would be a new dependency with a
different auth story. That is this package's boundary: Workspace services that
`google.golang.org/api` still generates.

## Layout

```
gworkspace/
├── gworkspace.go   # Option, WithBaseURL, WithTimeout, ClientOptions — the shared transport
├── errors.go       # the five provider error types + Translate + Retry-After parsing
├── gsheet/
│   ├── gsheet.go   # Client, Row, Factory, New, Rows/Tabs/FirstTab/Title, header + range handling
│   └── errors.go   # TabNotFoundError only — a tab is a Sheets concept
└── *_test.go       # one test file per source file, in both packages
```

The split mirrors `google.golang.org/api` itself: a core of shared pieces
(`googleapi/` for errors, `option/` and `transport/` for dialing) plus one
package per service (`sheets/v4`, `docs/v1`). Here the root owns the transport
and every failure Google can return for any document, and a subpackage owns one
service's calls and the error types only that service can produce.

Exported surface:

```go
// package gworkspace
const DefaultTimeout, DefaultResponseBodyLimit
type Option func(*settings)
func WithBaseURL(u string) Option
func WithTimeout(d time.Duration) Option
func ClientOptions(ts oauth2.TokenSource, scope string, opts ...Option) ([]option.ClientOption, error)
func Translate(err error, fileID string) error
type NotFoundError struct{ FileID string }
type PermissionDeniedError struct{ FileID string }
type QuotaExceededError struct{ RetryAfter time.Duration }
type UnavailableError struct{ Cause error }
type AuthExpiredError struct{ Cause error }
func IsNotFoundError, IsPermissionDeniedError, IsQuotaExceededError, IsUnavailableError, IsAuthExpiredError (err error) bool

// package gworkspace/gsheet
const ScopeReadOnly = sheetsapi.SpreadsheetsReadonlyScope
type Row map[string]string
type Client struct{ /* unexported */ }
type Factory func(ctx context.Context, ts oauth2.TokenSource) (*Client, error)
func New(ctx context.Context, ts oauth2.TokenSource, opts ...gworkspace.Option) (*Client, error)
func (c *Client) Rows(ctx context.Context, fileID, tab string) ([]Row, []string, error)
func (c *Client) Tabs(ctx context.Context, fileID string) ([]string, error)
func (c *Client) FirstTab(ctx context.Context, fileID string) (string, error)
func (c *Client) Title(ctx context.Context, fileID string) (string, error)
type TabNotFoundError struct{ Tab string }
func IsTabNotFoundError(err error) bool
```

`ClientOptions` is the shared piece a service client is built from — it applies
the options, builds the safe HTTP transport with the token source riding it, and
returns the `option.ClientOption` slice the generated constructor wants. It
returns options rather than a bare `*http.Client` because `WithBaseURL` also has
to produce an `option.WithEndpoint`, which cannot ride on a client
(`gworkspace/gworkspace.go:46-59`).

## Quickstart

From an `oauth2.TokenSource` to rows:

```go
import (
	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/gwerr"
)

// credential.Service.TokenSourceFor returns an oauth2.TokenSource for one
// stored credential — see internal/credential/service.go:181.
c, err := gsheet.New(ctx, ts)
if err != nil {
	return err
}

tab, err := c.FirstTab(ctx, fileID) // leftmost tab; use it when the caller named none
if err != nil {
	return gwerr.AppError(err)
}

rows, warnings, err := c.Rows(ctx, fileID, tab)
if err != nil {
	return gwerr.AppError(err)
}
```

`Rows` returns `([]Row, []string, error)`. `Row` is `map[string]string` keyed by
the normalized header (see [Header handling](#header-handling)); the second
value is one human-readable warning per renamed header, and consumers are
expected to render them. An empty tab, or a tab holding only a header row,
returns zero rows and no error — not a failure.

Every method takes the Drive `fileID`, so one `Client` serves any spreadsheet
the credential can read. `DefaultTimeout` (30s) bounds a single call;
`WithTimeout` narrows it.

## Errors

Every Google failure comes back as one of six typed errors, each with an
`Is<TypeName>` predicate that walks the chain via `errors.AsType`:

| Type                               | Predicate                 | Raised when                                       |
| ---------------------------------- | ------------------------- | ------------------------------------------------- |
| `gworkspace.NotFoundError`         | `IsNotFoundError`         | HTTP 404 — no document with that file id          |
| `gworkspace.PermissionDeniedError` | `IsPermissionDeniedError` | HTTP 403 — the credential may not read it         |
| `gworkspace.QuotaExceededError`    | `IsQuotaExceededError`    | HTTP 429, carrying a parsed `Retry-After`         |
| `gworkspace.UnavailableError`      | `IsUnavailableError`      | any other status, and every transport failure     |
| `gworkspace.AuthExpiredError`      | `IsAuthExpiredError`      | HTTP 401 — the credential needs reauthorization   |
| `gsheet.TabNotFoundError`          | `IsTabNotFoundError`      | HTTP 400 on a values read, or a blank/missing tab |

`gworkspace.Translate(err, fileID)` does the mapping
(`gworkspace/errors.go:92`); `gsheet` layers the one Sheets-specific case on top
of it (`gworkspace/gsheet/errors.go:26`).

These types deliberately carry **no `ToAppError()`**. This is a root package, so
it cannot import `internal/apperror` — Go forbids it, which is also why the code
used to live under `internal/`. The consequence is that a consumer must adapt
them:

```go
// internal/sheet/read.go:215
values, warnings, err := client.Rows(ctx, src.GoogleFileID, key.Tab)
if err != nil {
	return Rows{}, gwerr.AppError(err)
}
```

`internal/gwerr.AppError` is the adapter (`internal/gwerr/gwerr.go:18`). It
attaches this app's envelope and keeps the original reachable through `Unwrap`,
so `gworkspace.Is*` / `gsheet.Is*` still match and `Error()` still reads as the
provider message.

Skipping it is not a cosmetic loss. `internal/sheet/read.go:265` and
`internal/spreadsheet/service.go:262` both branch on
`apperror.AsAppError(err)` to decide whether a failure is an expected outcome or
an incident. Without the wrap, "Google document not found" fails that check,
falls through to `unexpected`, and gets reported as an incident with a 500 —
instead of the `GSH*`/`SHT*` code the surfaces render.

## Header handling

This is wire contract, not an implementation detail — the keys below are what
API consumers see. `normalizeHeaders`
(`gworkspace/gsheet/gsheet.go:132`) walks the header row left to right:

- A **blank** header cell becomes `col_<N>`, where `N` is the 1-based column
  index. Warning: `column 2 has a blank header; exposed as "col_2"`.
- A **duplicate** header gets `_2`, `_3`, … appended in column order, so
  `name, name, name` becomes `name, name_2, name_3`. Warning:
  `duplicate header "name" in column 2; exposed as "name_2"`.
- Headers are trimmed of surrounding whitespace before either rule applies.

Cells beyond the last header are dropped; a row shorter than the header simply
omits those keys. Values are coerced to strings — `true`, `1.5`, and `""` for a
null cell.

SECURITY: a tab title reaches the API inside an A1 range, so `quoteRange`
single-quotes it and doubles embedded quotes
(`gworkspace/gsheet/gsheet.go:152-155`). Without that, a tab named `A'!A1:Z`
would break out of the range expression.

## OAuth notes

The consent flow lives in `internal/credential/connect.go`, not here — but three
of its properties were expensive to discover and must not be re-derived.

1. **`prompt=consent` is required alongside `access_type=offline`**
   (`internal/credential/connect.go:127-131`). With `offline` alone, Google
   returns a refresh token on the _first_ authorization only; every
   **reconnect** then yields a credential with no refresh token, which fails at
   `NoRefreshTokenError` rather than at read time.

2. **The scope set is pinned on the authorize URL** via
   `oauth2.SetAuthURLParam("scope", …)` (`internal/credential/connect.go:130`).
   `AuthCodeURL` writes `c.Scopes` into the query first and applies its options
   afterwards, so the pinned value wins. A wider scope set on the injected
   `*oauth2.Config` therefore cannot leak into the consent screen. The set
   itself is a construction parameter — `credential.Scopes()` is the default and
   `credential.WithScopes` replaces it (`internal/credential/connect.go:34`,
   `:52`).

3. **The account email comes from the `id_token`** in the token-endpoint
   response, which the `email` scope triggers
   (`internal/credential/connect.go:283`). Reading the claim avoids a
   `userinfo` round trip; the response arrives over TLS, so the channel
   authenticates it, and the claim is display-only — never an authorization
   input.

The signed-state codec stays in the consuming app on purpose
(`internal/credential/state.go`): its payload is that app's tenancy model
(org, project, user, return-to) and verifying it needs an HMAC key a root
package has no business holding.

## Adding a service

This recipe is the whole justification for the core/subpackage split.

1. Create `gworkspace/gdocs/` with `gdocs.go` and, only if the service has
   failure modes the root does not model, `errors.go`.
2. Declare the scope constant from the generated package, e.g.
   `const ScopeReadOnly = docsapi.DocumentsReadonlyScope`.
3. Build the client with the shared transport:

   ```go
   func New(ctx context.Context, ts oauth2.TokenSource, opts ...gworkspace.Option) (*Client, error) {
   	apiOpts, err := gworkspace.ClientOptions(ts, ScopeReadOnly, opts...)
   	if err != nil {
   		return nil, err
   	}
   	svc, err := docsapi.NewService(ctx, apiOpts...)
   	if err != nil {
   		return nil, fmt.Errorf("gdocs: build service: %w", err)
   	}
   	return &Client{svc: svc}, nil
   }
   ```

   `ClientOptions` already returns `option.WithHTTPClient(...)`,
   `option.WithScopes(...)` and the endpoint override, so do not assemble them
   again.

4. Translate every call failure through `gworkspace.Translate(err, fileID)`.
   Add a local error type only for something genuinely service-specific, the way
   `gsheet.TabNotFoundError` exists because a tab is a Sheets concept.
5. Map any new local error type to a wire code in `internal/gwerr`, and add its
   code to `internal/apperror` and `docs/ERROR_CODES.md`. Reusing the root's
   five types needs no change there.
6. Widen the connect scope set by passing `credential.WithScopes(...)` at
   construction. Keep `credential.Scopes()` as the default.

Caveat: a new Google **service** on the same account does not need a new connect
flow — it needs a wider scope set on the same credential, which is why step 6 is
one call and not a second OAuth handler. Only a new **provider** (Photos,
Dropbox, Microsoft 365) would need its own flow, its own token storage, and its
own error translation.

## Stability

`gworkspace` is a root package, so per the repo [`README.md`](../README.md) it is
importable by other altalune apps. Pre-1.0.0 minor releases may break; pin exact
versions. `internal/gwerr` is not importable from outside this module —
downstream consumers write their own adapter, or handle the typed errors
directly.

The root must never import `internal/`. Two things enforce it: the
`platform-boundary-root` depguard rule in `.golangci.yaml`, and

```bash
go list -deps ./gworkspace/... | grep 'altalune.id/opensheet/internal'   # must print nothing
```

## Testing

Every test runs against `net/http/httptest` through
`gworkspace.WithBaseURL(srv.URL)`. No test makes a live Google call, and none
needs a credential.

```bash
go test ./gworkspace/... -cover          # gworkspace 100%, gsheet 98.8%
go test ./internal/gwerr/ -cover         # the app-side adapter
```

SECURITY: `WithBaseURL` relaxes the private-host filter that otherwise blocks
requests to loopback and link-local addresses
(`gworkspace/gworkspace.go:31-35`). It exists for tests and for a code-supplied
endpoint. It must never be wired from config, a request parameter, or any other
value an attacker can influence — that turns a spreadsheet read into an SSRF
primitive.
