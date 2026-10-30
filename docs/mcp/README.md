# MCP

Surface S7. Publishes control-plane RPCs as tools an MCP host can call. A tool and its RPC are
one verb reached two ways — no tool has its own implementation.

Rules: [`surfaces`](../surfaces/README.md) · Scope strings: [`scopes`](../scopes/README.md)

## Endpoints

| Path                                                  | Chain    | Credential |
| ----------------------------------------------------- | -------- | ---------- |
| `{basePath}/mcp`                                      | `MCP`    | bearer     |
| `/.well-known/oauth-protected-resource{basePath}/mcp` | `Probes` | none       |

The control plane's document, `/.well-known/oauth-protected-resource{basePath}/api`, adds a
`cli_client_id` member for `auth login --url`; the MCP document never does, as the CLI signs in for
`/api` only.

Streamable HTTP, stateless, JSON. `MCP` chain = `edge` + `RecoverJSON` — no session, no i18n,
no SSR gate (R5).

Behind a same-host proxy, a loopback connection may carry `http.baseURL`'s host as its `Host`
(`mcp.WithAllowedHosts`). Every other public `Host` on a loopback connection gets 403, as DNS
rebinding.

## Setup

```bash
OPENSHEET_TOKENS_ISSUER=https://othra.example.com/p/<handle>   # the Othra project issuer
OPENSHEET_HTTP_BASE_URL=https://app.example.com
OPENSHEET_MCP_ENABLED=true
OPENSHEET_MCP_APPS_UI=true                         # only if the host renders MCP Apps
```

Order is enforced — each step's omission is a boot error:

1. `tokens.issuer` — without it there is no verifier (`MCPIssuerRequiredError`).
2. `http.baseURL` — the audience must name this deployment (`MCPBaseURLRequiredError`).
3. `mcp.enabled=true` — the audience derives from 1–2.
4. In Othra, register a resource server whose identifier is `mcp.audience`. Othra verifies it
   by reading the RFC 9728 document above, so the app must be deployed first.
5. `mcp.introspection.clientID` / `clientSecret` — optional: the MCP resource server's public id
   and introspection secret, to accept `othra_pt_` personal tokens.
6. `mcp.appsUI` — optional.

Discovery is lazy: a wrong `tokens.issuer` fails the first verified call with a masked 401,
not boot.

| Key                              | Default                             | Meaning                                                  |
| -------------------------------- | ----------------------------------- | -------------------------------------------------------- |
| `mcp.enabled`                    | `false`                             | Master switch                                            |
| `mcp.audience`                   | `baseURL + basePath + /mcp`         | RFC 8707 resource id the verifier pins                   |
| `mcp.audienceOverride`           | `false`                             | Accept an audience ≠ the mounted URL                     |
| `mcp.appsUI`                     | `false`                             | Publish the UI resource, bind `_meta.ui`                 |
| `mcp.introspection.clientID`     | `""`                                | MCP resource server id, for personal tokens              |
| `mcp.introspection.clientSecret` | `""`                                | Its introspection secret (`othra_rs_…`)                  |
| `mcp.introspection.maxPerMinute` | `tokens.introspection.maxPerMinute` | Introspection calls per minute, all tokens; `0` inherits |

`mcp.audience` must be absolute, fragment-free, and equal the mounted URL unless
`mcp.audienceOverride=true`. The override is the only way to widen what the verifier accepts,
so it is explicit rather than inferred.
It must also differ from `tokens.audience`: a shared audience would let a token minted for MCP
authenticate on the control plane.

## Auth

```mermaid
sequenceDiagram
  participant H as MCP host
  participant S as /mcp
  participant A as Othra
  H->>S: tools/call (no token)
  S-->>H: 401 + WWW-Authenticate: resource_metadata="…"
  H->>S: GET /.well-known/oauth-protected-resource…
  S-->>H: { resource, authorization_servers, scopes_supported }
  H->>A: OAuth (audience = mcp.audience)
  A-->>H: access token
  H->>S: tools/call + Bearer
  S->>S: authn.Chain{key, token} → step-up check → dispatch
```

- Chain is `authn.Chain{KeyAuthn, tokenAuthenticator}` — the same API-key authenticator the other
  planes use, plus an `oauthrs` verifier on a **second** resource pinned to `mcp.audience`. It
  accepts RFC 9068 access tokens (`typ: at+jwt`, RS256, so an ID token never passes) and, when
  `mcp.introspection` is set, `othra_pt_` personal tokens through RFC 7662 introspection with no
  cache, so a revoked token stops at once. An API key never reaches the issuer.
  **SECURITY:** never the kernel's control-plane verifier — audience binding is what stops a
  token minted for `/api` being replayed at `/mcp`.
- Missing, malformed and rejected credentials all answer **one identical 401** body. Only the
  challenge differs: no bearer gets `Bearer resource_metadata="…"`, a refused one adds
  `error="invalid_token"`; why it was refused is logged, never returned.
- **Every credential is scope-checked — no exemption.** An MCP token is a _delegated_ grant an
  agent holds for someone, so its scopes are the whole limit of that delegation.
- **Step-up.** An OAuth token calling `tools/call` for a tool whose scope it lacks gets HTTP 403
  before dispatch, with `WWW-Authenticate: Bearer resource_metadata="…",
error="insufficient_scope", scope="<tool scope>"` (RFC 6750 Section 3.1), so the host can ask
  for the scope. An API key cannot step up, so it keeps the in-result denial.
- The token's email and name are never read; the user is found by `(iss, sub)` and the profile
  comes from the local row.
- A tool declaring **no** scope is denied, not admitted.
- Tenant comes from the principal, not the path — `/mcp` has no org segment.
  See [`request scope`](../multitenancy/request-scope.md).

## Errors

| Where            | Shape                                                     |
| ---------------- | --------------------------------------------------------- |
| Transport 401    | HTTP 401, `ErrorPayload`, code `MCP001`                   |
| Transport 503    | HTTP 503, `ErrorPayload`, code `TKN002`, no challenge     |
| Step-up 403      | HTTP 403, `ErrorPayload`, code `GEN003` (OAuth tokens)    |
| Tool failure     | HTTP 200, `result.isError = true`                         |
| Unmapped failure | Same, code `GEN900` — the cause is logged, never returned |

```json
{
  "code": "GEN003",
  "message": "credential lacks the required scope",
  "meta": { "tool": "project_list", "scope": "sheets:read" },
  "request_id": "…",
  "trace_id": "…"
}
```

**SECURITY:** a tool failure answers _in the result_, never as a JSON-RPC error — a bare error
yields a wire error with code zero, which is invalid. Codes: [`error codes`](../errors/README.md).

Arguments that do not decode into the tool's input message answer `GEN004` with
`meta: { "tool", "field" }` (`field` only when the decoder names it). The generated handler
returns `mcp.InvalidArgumentsError`; it is a client error and is never logged as an incident.

## Adding a tool

Steps, in order: [`howto/mcp-tool.md`](../howto/mcp-tool.md) — the annotation on a **unary** RPC,
`make generate`, the scope in `internal/mcp/scopes.go`, the two rows in
`internal/boot/mcp_tools.go`, and the smoke run. What the annotation may say, and what the generator
and boot refuse, is below.

| Field         | Effect                                                  |
| ------------- | ------------------------------------------------------- |
| `name`        | Wire name; defaults to the snake-cased method           |
| `title`       | Overrides the derived title, which mangles acronyms     |
| `description` | Falls back to the RPC's leading comment                 |
| `required`    | JSON field names marked required in the input schema    |
| `mutation`    | Clears `readOnlyHint`                                   |
| `destructive` | Sets `destructiveHint` — always sent, absent means true |
| `ui`          | Joined to `ui_prefix` to form the `_meta.ui` link       |

### The generator refuses

| Refusal                                    | Why                          |
| ------------------------------------------ | ---------------------------- |
| streaming RPC                              | a tool must be unary         |
| name ∉ `^[a-z_][a-z0-9_]{0,63}$`           | hosts key on the name        |
| duplicate name or `…ToolName` constant     | unresolvable                 |
| no description and no leading comment      | renders unlabelled           |
| `required:` naming a field the input lacks | error lists the real fields  |
| `ui:` ∉ `^[a-z][a-z0-9-]*$`                | it becomes a URI segment     |
| `ui:` with no `ui_prefix`                  | the link resolves to nothing |

Boot adds more: a duplicate or handler-less spec, a registration after the server was built,
and a `_meta.ui` naming an unpublished resource — the last panics, because it renders as a
blank panel in every host.

### UI link

`mcp.appsUI=true` publishes one self-contained bundle at `ui://opensheet/app`, media type
`text/html;profile=mcp-app`, and binds every tool carrying a `ui:`:

```json
"_meta": { "ui": { "resourceUri": "ui://opensheet/app" } }
```

- **Exactly one key. No flat `ui/resourceUri` sibling.** Both meta schemas are
  `additionalProperties:false`, so emitting the deprecated sibling alongside the nested key
  makes a strict host reject the pair and render nothing.
- **Must be self-contained** — the host frames it with no network, so any `src=`, `href=` or
  `@import` is a dead reference. Re-vendor with `make mcp-ui-vendor`.

## Testing

| Command                                | Covers                           |
| -------------------------------------- | -------------------------------- |
| `bash scripts/verify-mcp-smoke.sh`     | the whole wire shape, real boot  |
| `go test ./mcp/... ./internal/mcp/...` | transport, registry, scope, meta |
| `make check`                           | everything but the smoke script  |

The smoke script boots on ephemeral SQLite against a stub issuer and asserts capabilities, tool
names and schemas, `_meta.ui` carrying one key, the bundle's media type and self-containment,
the 401 challenge, an in-result scope denial, and the metadata document. Needs `go`, `curl`,
`jq`, `python3`. CI runs it as its own job.
