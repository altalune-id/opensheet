# How do I…

Task recipes. Each one is a short, ordered procedure for a change people make often.

**These are procedures. [`docs/`](../) holds the contracts.** A recipe tells you the steps and
links to the contract that governs them; it never restates one. When the two disagree, the
contract wins and the recipe is a bug.

## Adding something that handles a request

Pick the surface first — it decides the mount, the credential and the middleware chain.
[`surfaces`](../surfaces/README.md) is the contract; this table is the shortcut.

| I want to                                      | Surface          | Recipe                          |
| ---------------------------------------------- | ---------------- | ------------------------------- |
| show a page to a signed-in human               | S1 console       | [webpage](webpage.md)           |
| add an RPC the console or our own clients call | S2 control plane | [internal-api](internal-api.md) |
| expose a REST endpoint to a customer's script  | S3 data plane    | [external-api](external-api.md) |
| receive a webhook from a third party           | S4 ingest        | [webhook-in](webhook-in.md)     |
| add an event sent to a tenant's endpoint       | S5 dispatch      | [webhook-out](webhook-out.md)   |
| add a terminal command                         | S6 cli           | [cli-command](cli-command.md)   |
| let an MCP host call a verb                    | S7 mcp           | [mcp-tool](mcp-tool.md)         |

"Internal" and "external" are about the **credential**, not the network: S2 takes a session or
an api key and is ours to change; S3 takes an api key, is versioned under `/api/v1/`, and is a
promise to someone outside.

## Changing domain code

| I want to                               | Recipe                              |
| --------------------------------------- | ----------------------------------- |
| add a whole new bounded context         | [module](module.md)                 |
| add a method to an existing Service     | [service-method](service-method.md) |
| read or write something new from the DB | [store-method](store-method.md)     |
| grow a module a sub-entity of its own   | [subdomain](subdomain.md)           |

## Platform and configuration

| I want to                                             | Recipe                                      |
| ----------------------------------------------------- | ------------------------------------------- |
| add a cache, queue, rate limiter or background loop   | [platform-primitive](platform-primitive.md) |
| run work on the queue, or broadcast to every instance | [queue-consumer](queue-consumer.md)         |
| add a config key                                      | [config-key](config-key.md)                 |

## Errors

| I want to                     | Recipe                      |
| ----------------------------- | --------------------------- |
| fail correctly from any layer | [errors](errors.md)         |
| add a new `<DOM><NNN>` code   | [error-code](error-code.md) |

## Tenancy

Every recipe shows the **tenant-scoped path as the default** and states what changes when the
thing is not tenant-scoped. Read that part even when you are sure you do not need it — the
unscoped case removes a guard rather than adding one, and nothing fails loudly when you get it
wrong. [`multitenancy`](../multitenancy/README.md) is the model;
[`request scope`](../multitenancy/request-scope.md) is how a request acquires its scope.

## The contracts these link to

| Doc                                         | Owns                                       |
| ------------------------------------------- | ------------------------------------------ |
| [`architecture`](../architecture/README.md) | the layer map                              |
| [`surfaces`](../surfaces/README.md)         | surfaces, mounts, credentials, R1–R11      |
| [`scopes`](../scopes/README.md)             | the scope catalog                          |
| [`mcp`](../mcp/README.md)                   | the MCP surface                            |
| [`webhooks`](../webhooks/README.md)         | the webhook receiver contract              |
| [`queue`](../queue/README.md)               | jobs, broadcasts, retries and the DLQ      |
| [`modules`](../modules/README.md)           | the domain-module shape                    |
| [`platform`](../platform/README.md)         | the platform-primitive shape               |
| [`error codes`](../errors/README.md)        | the code registry and where each travels   |
| [`config`](../config/README.md)             | keys, precedence, modes                    |
| [`cli`](../cli/README.md)                   | command tree, exit codes, output envelopes |
