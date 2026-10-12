# Documentation

Two layers, and they never restate each other:

- **Contracts** — what must be true. The rest of this page.
- **Procedures** — how to do a task. [`howto`](howto/README.md), 16 recipes.

When the two disagree, the contract wins and the recipe is a bug.

## Start here

| Doc                                      | Owns                                                             |
| ---------------------------------------- | ---------------------------------------------------------------- |
| [`architecture`](architecture/README.md) | the layer map — surfaces, domain, data, and what each may not do |
| [`surfaces`](surfaces/README.md)         | the seven surfaces, mounts, credential classes, rules R1–R11     |
| [`howto`](howto/README.md)               | "I want to add X" → the recipe that covers it                    |

## Building

| Doc                                              | Owns                                                      |
| ------------------------------------------------ | --------------------------------------------------------- |
| [`modules`](modules/README.md)                   | the domain-module shape. Reference impl: `internal/todo/` |
| [`platform`](platform/README.md)                 | cross-cutting primitives, workers, the `Kernel`           |
| [`queue`](queue/README.md)                       | jobs and broadcasts over NATS, retries, the DLQ           |
| [`multitenancy`](multitenancy/README.md)         | tenants, the two guards, RLS, Postgres roles              |
| [`request scope`](multitenancy/request-scope.md) | how a request acquires its tenant scope                   |
| [`error codes`](errors/README.md)                | the code registry and where each code travels             |
| [`scopes`](scopes/README.md)                     | the scope catalog. Wire contract; additive only           |

## Interfaces

| Doc                                   | Owns                                                          |
| ------------------------------------- | ------------------------------------------------------------- |
| [`mcp`](mcp/README.md)                | the MCP surface — mount, auth, tools, the Apps UI             |
| [`webhooks`](webhooks/README.md)      | the receiver contract — envelope, headers, signature, retries |
| [`cli`](cli/README.md)                | command tree, exit codes, output envelopes                    |
| [`cli commands`](cli/commands.md)     | per-command reference and payload fields                      |
| [`cli resolution`](cli/resolution.md) | global flags, credential and URL precedence                   |

## Running it

| Doc                                  | Owns                                          |
| ------------------------------------ | --------------------------------------------- |
| [`config`](config/README.md)         | keys, precedence, modes, awareness tags       |
| [`deployment`](deployment/README.md) | docker, Postgres roles, probes, observability |
| [`workers`](deployment/workers.md)   | scheduler and queue across replicas, NATS     |

## Conventions

- Every file here is **200 lines or fewer**. A topic that outgrows that splits into its folder.
- Each folder's `README.md` is its entry point and links to its parts.
- Reference implementations: `internal/todo/` (flat module), `internal/blog/` (relations, subdomains).
- Repo-wide rules for agents: [`AGENTS.md`](../AGENTS.md). Terminology: [`GLOSSARY.md`](../GLOSSARY.md).
