# .husky

Git hooks managed by [husky](https://typicode.github.io/husky/). Installed
automatically on `pnpm install` (via the `prepare` script in `package.json`).

## What runs when

| Hook | Command | Purpose |
|---|---|---|
| `pre-commit` | `go tool i18n-lint --check` | fail if a `d.Tr` key has no translation |
| `pre-commit` | `pnpm exec lint-staged` | gofmt + go vet, hadolint, yamllint, buf lint, prettier on staged files only |
| `pre-push` | `git verify-commit` over outgoing range | fail fast if any outgoing commit is not GPG/SSH-signed |
| `pre-push` | `go tool gen-config-example --check` (env + yaml) | fail if `.env.example` / `config.example.yaml` drifted from struct tags |
| `pre-push` | `go tool i18n-lint --check` | i18n coverage across locales |
| `pre-push` | `go test -race -shuffle=on ./...` | full unit test suite |
| `pre-push` | `golangci-lint run --timeout=3m ./...` | static analysis |
| `pre-push` | `pnpm exec buf breaking --against origin/main` | protobuf breaking-change guard |
| `pre-push` | `goreleaser check` | release config sanity |

## Before you commit — the short list

- Run `make check` (fmt + vet + test) — same gate as pre-commit but without staged-only filter.
- If you added a `d.Tr("...")` in a template, add the key to every locale in `internal/web/i18n/`.
- If you changed a `config.Config` field, run `make config-examples` and commit the regenerated files.
- If you edited a `.proto`, commit the regenerated `gen/` alongside.

## Before you push — the short list

- All pre-commit rules, plus:
- Integration tests locally if you changed `postgres.go` or a migration: `make test-integration`.
- Breaking a proto? Bump the API major or add a new versioned service; don't rename fields in place.

## Signed commits

Every commit MUST be GPG- or SSH-signed. The `pre-push` hook verifies
every outgoing commit before it leaves your machine, but the durable
gate is server-side — GitHub → Settings → Branches → **Require signed
commits** on `main`. `--no-verify` bypasses the hook, not the branch
protection.

Set up once:

```bash
git config commit.gpgsign true
git config tag.gpgsign true
git config user.signingkey <keyid>
# or SSH signing:
git config gpg.format ssh
git config user.signingkey <ssh-key-path>
```

## Skipping (don't)

Never `git commit --no-verify` or `git push --no-verify`. If a hook fails,
fix the underlying issue — the hook is telling you CI would fail too.

## Uninstall / troubleshoot

- Hooks not firing? `pnpm install` re-installs them (`prepare` script).
- Broken permission? `chmod +x .husky/pre-commit .husky/pre-push`.
- Missing `pnpm` / `go tool <x>`? Run `make install-tools` to get pnpm devDeps
  and Go toolchain plugins.
