# Add a CLI command

Surface **S6 cli** ([`surfaces`](../surfaces/README.md)) — an operator at a terminal. S6 owns no verbs (R11): if the operation already exists on a plane, the command calls that plane rather than getting a private mirror of it. Adding the verb itself is [`internal-api.md`](internal-api.md) (S2) or [`external-api.md`](external-api.md) (S3).

Worked examples: `internal/cli/todo.go` (remote, Connect, bearer token) and `internal/cli/org.go` (the in-process shape). No command calls the data plane.

## Steps

1. Pick the mode. In-process through `ServerBootFn` for local operator work (`serve`, `migrate`, `org`); a client of S2 via `ClientBootFn` for anything the control plane already owns.
2. Write a factory in `internal/cli/<name>.go`:

   ```go
   func newXCmd(bootClient ClientBootFn) *cobra.Command
   ```

   Never a package-level `var`, so every test gets a fresh isolated tree.

3. Add it to the `root.AddCommand(...)` list in `internal/cli/root.go` with a `GroupID` of `runtime`, `auth`, `tenancy`, `domain` or `meta`.
4. Keep the business logic outside `cmd/`. `cmd/opensheet` builds the tree, runs it, and maps the error to an exit code — it knows nothing about Cobra internals or Viper.
5. Resolve the caller. Control-plane commands use `connFromCmd(cmd, bootClient)`, which resolves the URL and the credential and refreshes a profile's token. In-process commands use `withPrincipal(cmd)` (the local profile) and are wrapped in `markOperator` in `root.go`, so they refuse `--url`, `--token` and `--token-file`.
6. Print through `internal/cli/render`: `render.Detect(cmd)`, then `render.Table` or `render.JSON`.
7. Map failures to `apperror.New(code, message, grpcCode)` so `ExitCodeFor` in `internal/cli/exit.go` yields the documented exit code.
8. Test against a fresh `NewRootCmd` in memory, with `cmd.SetOut` / `SetArgs`.
9. Update [`cli`](../cli/README.md). Command names, flag names, exit codes and the field names under `data` are the contract.

## Tenancy

- **In-process (default)** — build the scope from the resolved principal and enter it explicitly:

  ```go
  ctx := tenant.Into(cmd.Context(), tenant.Context{OrgID: p.OrgID, ProjectID: p.ProjectID, UserID: p.UserID})
  ```

  This is the one surface where hand-building a `tenant.Context` is correct, because there is no request to gate.

- **Remote** — the scope is not yours to set. A control-plane command sends a token and the server derives the org from the principal.
- **Not tenant-scoped** (`version`, `healthz`, `completion`) — no principal resolution and no `tenant.Into` at all; they only call `config.Load`. Nothing verifies a credential, so such a command must not reach a tenant-scoped store.

## Gotchas

- `--org` and `--project` are **not** global tenant overrides. Nothing reads them; every tenant-scoped command takes its org from the principal and ignores both.
- A profile's token is withheld when `--url` names another server (`ProfileHostMismatchError`). An explicit `--token` is unambiguous intent and is sent to whatever `--url` names.
- A remote command never touches the session file itself: `connFromCmd` refreshes under the session file lock, and every write goes through `profileStore.edit`.
- Every command treats `ndjson` as `json` and emits the `{"data": …}` envelope.
- `--no-interactive`, `--log-level` and `--log-format` are declared but nothing reads the flags; the `OPENSHEET_*` env vars do work for the last two.
- `healthz` never adopts a profile or `http.baseURL` — it must reach the listener beside it.

## Contracts

[`cli`](../cli/README.md) · [`surfaces`](../surfaces/README.md) (R11) · [`request scope`](../multitenancy/request-scope.md) · [`error codes`](../errors/README.md)
