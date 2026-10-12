# Add an MCP tool

Surface **S7 mcp** ([`mcp`](../mcp/README.md)) — an MCP host acting for a person or an agent, at `/mcp`. A tool and a control-plane RPC are **one verb reached two ways**; no tool has its own implementation. So the RPC comes first: [`internal-api.md`](internal-api.md) (S2).

Worked example: `blog_list` and `blog_publish` in `api/blog/v1/blog.proto`.

## Steps

1. Annotate the **unary** RPC in `api/<module>/v1/<module>.proto`:

   ```proto
   option (mcp.v1.tool) = {
     name: "blog_publish"
     description: "Publish a draft blog post. Requires the post's UUID."
     mutation: true
     required: ["postId"]
   };
   ```

2. `make generate` — `cmd/protoc-gen-mcp` emits `gen/go/<module>/v1/<module>v1mcp/`, carrying a `…ToolName` constant and a `Register<X>ServiceTools` function.
3. Declare the scope in `internal/mcp/scopes.go`: a `Tool<X>` constant bound to the generated `…ToolName`, plus its `ScopeTable()` row.
4. Wire the domain in `internal/boot/mcp_tools.go` — add `"<pkg>.v1"` to both `mcpToolDomains()` and `mcpToolManifest()`, calling `Register<X>ServiceTools(reg, apiSrv.XSvc, mcpinternal.ScopeFor)`.
5. `make check`, then `bash scripts/verify-mcp-smoke.sh`, which boots for real and asserts the wire shape.

## Tenancy

- **From the principal, never the path (the only shape here).** `/mcp` has no org segment, so `internal/mcp/auth.go` puts the authenticated principal on the context and the tool calls the same control-plane method an RPC would — landing in the same scoping code.
- **Project-scoped tool (default)** — the `projectId` argument reaches `scopeToProject`, which loads the project under the principal's `ActiveOrgID` and refuses when `proj.OrgID` differs. It narrows; it cannot re-target.
- **Project-scoped read with a default** — `blog_list` takes `projectId` as optional and falls back
  to the principal's `ActiveProjectID`, then runs the same `scopeToProject` check, so the fallback
  narrows and never re-targets. A principal carrying no active project fails with `PRJ005`, whose
  message tells the caller to run `project_list` first.
- **Org-scoped tool** (`member_list`) — the `projectId` argument and its check disappear. The tool acts on `ActiveOrgID` alone, so it takes an `org`-level scope and an org-wide read is the blast radius of one delegated token.
- **Not tenant-scoped** — nothing enters a scope, and a tenant-scoped store call fails with `tenant: missing context`.
- A JWT's org comes from the local user's membership, never from the token's `org_id` claim — the verifier puts it on `ClaimedOrgID` and `internal/user/authn.go` resolves the real one. Pinned by `TestMCP_JWTResolvesItsTenantFromMembership`.

## Gotchas

- **The annotation carries no scope.** A tool's scope lives once in `internal/mcp/scopes.go` and is resolved at registration, so the runtime check cannot drift. Never add an authorization field to the proto.
- A tool missing from that catalog resolves to the empty scope and is **denied**, not admitted.
- Boot fails closed in both directions: `assertMCPWiring` on a declared domain with no registrar, `assertMCPTools` on a catalog entry nobody registered.
- **A JWT is scope-checked here**, unlike on the control plane. An MCP token is a delegated grant, so its scopes are the limit of the delegation.
- The generator refuses a streaming RPC, a name outside `^[a-z_][a-z0-9_]{0,63}$`, a duplicate name, a missing description with no leading comment, a `required:` naming a field the input lacks, and a `ui:` with no `ui_prefix` (set to `ui://opensheet` in `buf.gen.yaml`).
- `ui:` needs `mcp.appsUI=true`; a `_meta.ui` naming an unpublished resource **panics** at boot. The bundle must be self-contained — re-vendor with `make mcp-ui-vendor`.
- A tool failure answers HTTP 200 with `result.isError = true`, never as a JSON-RPC error.

## Contracts

[`mcp`](../mcp/README.md) · [`scopes`](../scopes/README.md) · [`surfaces`](../surfaces/README.md) · [`request scope`](../multitenancy/request-scope.md) · [`error codes`](../errors/README.md)
