# Surfaces: what the recipes assume

Seven surfaces exist and no eighth may be invented. Names, mounts, credential classes,
middleware chains and R1–R11 are fixed by
[`surfaces`](../../../../docs/surfaces/README.md); the per-surface procedures are the recipes:

| Surface          | Recipe                                                            |
| ---------------- | ----------------------------------------------------------------- |
| S1 console       | [`howto/webpage.md`](../../../../docs/howto/webpage.md)           |
| S2 control plane | [`howto/internal-api.md`](../../../../docs/howto/internal-api.md) |
| S3 data plane    | [`howto/external-api.md`](../../../../docs/howto/external-api.md) |
| S4 ingest        | [`howto/webhook-in.md`](../../../../docs/howto/webhook-in.md)     |
| S5 dispatch      | [`howto/webhook-out.md`](../../../../docs/howto/webhook-out.md)   |
| S6 cli           | [`howto/cli-command.md`](../../../../docs/howto/cli-command.md)   |
| S7 mcp           | [`howto/mcp-tool.md`](../../../../docs/howto/mcp-tool.md)         |

A module needs only the surfaces it is actually used through; one reaching for all of them by
default becomes several times the size it needs to be. This file is what the recipes leave out.

## Console — things that stay green while broken

**Route probes are not feature tests.** `probeRoutes()` in `internal/boot/route_scope_test.go`
runs every route twice — as a user with no org, and as an org member — asserting `< 500` and no
tenant-scope error in the log. So a nonexistent `{id}` must 404, a caller with no org must 303,
and a refused delete must render the list with an error banner rather than a 500. But the probes
**stop at project resolution and never enter a handler body**. They prove a route is registered,
not that it works. Write a handler test that boots a server, seeds a project and exercises the
real flow.

**An HTMX fragment needs the full layout context.** Rendering a fragment with a bare base leaves
`ActiveOrg` nil, so `d.ProjectPath` collapses to `/orgs` and every action URL in the swapped
markup breaks. Type checking and route probes both stay green; it only shows up in a browser.

**Rendered HTML reaches a page through `@templ.Raw(...)`** (`legal.templ`, `post_form.templ`). A
plain templ expression escapes it and looks like a rendering bug.

`web.Mux` is an interface, not `*http.ServeMux` — the server passes a recording mux, so the
patterns a handler registers are also reported to the route test.

The nonce rule and the `//i18n:use` rule are in
[`howto/webpage.md`](../../../../docs/howto/webpage.md#gotchas).

## i18n

Every `d.Tr` key must exist in all five locale files
(`internal/i18n/locales/active.{ar-SA,en-US,id-ID,ja-JP,ms-MY}.yaml`) or `make i18n-check` and
the pre-commit hook fail.

**Do not run `go tool i18n-lint --fix`.** It yaml-marshals the whole map, re-sorting every key
and dropping header comments. Append by hand: real `en-US` values, empty stubs elsewhere, every
plural form present for a `TrN` key. An empty value satisfies `-check`; a missing key does not.

## Control plane — wire-shape decisions

The recipe covers the files. These are the API shapes worth copying, and they are choices the
recipe does not make for you.

- **Set by id, return nested.** Requests carry `category_id` and `repeated string tag_ids`;
  responses carry the nested messages. A consumer rendering the record should not need a second
  call.
- **Do not offer set-by-name** for a related aggregate — it lets one module's write silently
  create another module's vocabulary.
- **Lifecycle transitions get their own RPCs** (`PublishPost`, `UnpublishPost`) rather than a
  `status` field on update, so the transition stays visible on the wire.
- **Update is full replacement**, and say so. An absent `repeated` field clears the set — proto3
  has no field presence for repeated fields, so that is the only implementable reading.
- **A versioned aggregate returns its `version`** and its mutating RPCs accept `if_version`,
  passed straight through to the service. Omitted means `0`, an unconditional write.
- **Render server-side.** If the record has markdown, return the rendered HTML alongside the
  source so a consumer cannot reintroduce the escaping bug the renderer closes.

`controlplane.New(...)` is a positional constructor, so adding a service grows it and every call
site — `internal/boot/http.go` plus the package's test helpers.

CI runs `buf lint` and a proto-drift check, so generated output must be committed.

## Data plane, MCP and CLI

Fully covered by their recipes. The two rules a module author most often gets wrong:

- **R8 fixes the path shape.** A non-CRUD data-plane action creates a resource, never an
  RPC-shaped path; a state transition on an existing resource takes a verb sub-resource. Worked
  examples: [`plane rules` R8](../../../../docs/surfaces/plane-rules.md#r8--shaping-non-crud-actions).
- **The CLI owns no verbs (R11).** It is a client of the plane that owns the verb, so a
  conditional write is a `--if-version` flag forwarded to the data plane, not a precondition the
  CLI evaluates. [`dispatch and cli` R11](../../../../docs/surfaces/dispatch-and-cli.md#r11--the-cli-is-a-client-not-a-plane).

An MCP tool is an annotation on an **existing** control-plane RPC, never a new implementation —
so the RPC comes first. [`mcp`](../../../../docs/mcp/README.md) is the contract.
