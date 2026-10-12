# Add a config key

Use this when an operator needs a new knob. Precedence, the modes, and what each awareness value
means are contracts — [`config`](../config/README.md). This is the procedure.

## Steps

1. **Add the field** to the owning struct in `internal/platform/config/config.go`, or to the
   package's own `Config` when a platform package owns it (`internal/platform/db/config.go`,
   `internal/platform/tokens/config.go`) — `config.Config` embeds those.
2. **Tag it.** `mapstructure` is load-bearing: both the env walker and viper's decoder read it.
   `yaml` mirrors it by convention and is not what the loader keys on.
3. **Add an `awareness:"..."` tag** — `required`, `bootstrap`, `secret`, `mode:cloud`,
   `mode:selfhosted`, or `-` to drop what the enclosing struct contributed.
4. **Add a default** in `setDefaults` in `internal/platform/config/loader.go`, if it has one.
5. **Validate it** when the rule is non-trivial: a per-field rule goes in the `validate:"..."`
   tag, a cross-field invariant goes in `validateInvariants` — both reached from `Config.Validate()`.
6. **Run `make config-examples`.** `make config-examples-check` fails CI on drift.
7. **Document it** in the matching table in [`config`](../config/README.md) when its
   behavior does not follow from the name.

```go
Tenant TenantConfig `yaml:"tenant" mapstructure:"tenant" awareness:"bootstrap"`
```

## Env binding

Automatic. `bindEnv` in `internal/platform/config/env.go` walks the struct and derives the variable
from the dotted path: prefix `ALT`, `.` and `-` become `_`, upper-cased. `http.basePath` becomes
`OPENSHEET_HTTP_BASE_PATH`. Nothing is registered by hand, and a name that does not fall out of that walk
is not reachable from the environment at all.

## Gotchas

- **Add an awareness tag even though nothing forces you to.** Most leaf keys in the tree carry
  none, and no test requires one — `genesis_awareness_test.go` and `mcp_config_test.go` pin a
  handful of specific fields and nothing else. An untagged neighbour is not evidence the tag is
  optional; it means nobody has gone back to fill it in.
- **Tags inherit down, and `-` clears them.** `mergeAwareness` in
  `internal/platform/config/envwalk.go` merges the enclosing struct's set with the field's own.
  `awareness:"-,secret"` drops the inherited set and keeps `secret`.
- **A `secret` field emits no value** in `.env.example`, even when a default exists internally.
- **An untagged nested struct still becomes a path segment.** The walker falls back to the Go field
  name, so the env var picks up the exported identifier verbatim. `mapstructure:"-"` is the only
  way to keep a field out of the key space.
- **Never add a knob for a job cadence.** A cadence is a package constant in the owning module's
  `scheduler.go`; changing one changes domain behavior and belongs in review, not in a deploy-time
  env var.
