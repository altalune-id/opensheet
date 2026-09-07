## Summary

<!-- What changed and why. A few sentences is enough. -->

## Type

<!-- Pick one, delete the rest. Matches .gitmessage.txt. -->

feat | fix | chore | refactor | docs | test | perf | build | ci

## Verification

- [ ] `make check` passes
- [ ] `make test-integration` run if `postgres.go` or a migration was touched
- [ ] `make config-examples` run if a `Config` struct tag changed
- [ ] `make generate` run if a `.proto` or `.templ` file changed
- [ ] `make tenant-tables` run if a tenant-scoped table was added
- [ ] Screenshot(s) attached for UI-visible changes

## Breaking changes

<!-- Root packages (authl, logger, mailer, nanoid, reqid, telemetry, worker, internal/platform) affected? Migration path? Delete this section if none. -->

## Notes for the reviewer

<!-- Anything not obvious from the diff. Delete if none. -->
