# schema

Embedded goose migrations for Postgres and SQLite, the runtime migrator, the
RLS boot-time guard, and the tenant-table registry consumed by `tenant.PgConn`.

Migrations are `.sql` templates rendered with `{{.Schema}}`, `{{.TablePrefix}}`,
and `{{.Role}}` before goose applies them. Both dialects satisfy the same
domain interfaces — SQLite is dev/demo, Postgres is production.

## Layout

```
schema/
├── migrations/postgres/    # 001_init … 020_org_api_keys, VERSION
├── migrations/sqlite/      # 001_init … 017_org_api_keys, VERSION
├── migrator.go             # goose runner, template rendering, embed FS
├── migrator_templatefs.go  # per-boot template-rendered file system
├── rls_guard.go            # boot-time BYPASSRLS assertion
├── table_guard.go          # boot-time check for tables with no org_id
└── tenant_tables_gen.go    # generated from RLS migrations (make tenant-tables)
```

The two dialects number independently — SQLite has no RLS or definer
migrations — so each `VERSION` pins its own highest `NNN`.

`TenantTableSuffixes` (in `tenant_tables_gen.go`) drives RLS policy
enforcement — every table in this list carries `org_id` and gets
`FORCE ROW LEVEL SECURITY` on Postgres. Regenerate with
`make tenant-tables` after adding a tenant-scoped table.

## Tables

`users` and `sessions` are global (no `org_id`); every other table is
tenant-scoped and appears in `TenantTableSuffixes`.

```mermaid
erDiagram
    users ||--o{ memberships : "belongs to"
    orgs ||--o{ memberships : "has"
    orgs ||--o{ projects : "owns"
    orgs ||--o{ invites : "pending"
    projects ||--o{ todos : "contains"
    projects ||--o{ blog_posts : "contains"
    blog_posts ||--o{ blog_post_tags : "tagged"
    projects ||--o{ api_keys : "project key"
    orgs ||--o{ api_keys : "org key"
    api_keys ||--o{ api_key_projects : "grants"
    projects ||--o{ api_key_projects : "granted"
    projects ||--o{ webhook_endpoints : "sends to"
    webhook_endpoints ||--o{ webhook_deliveries : "attempts"

    memberships {
        uuid org_id FK
        uuid user_id FK
        text role "owner|admin|member"
    }
    projects {
        uuid id PK
        uuid org_id FK
        text slug "unique per org"
    }
    api_keys {
        uuid id PK
        uuid org_id FK
        uuid project_id FK "null for org and personal keys"
        text kind "project|org|personal"
        bool all_projects
        bytea secret_hash UK
        text secret_hint "last 4 chars"
        uuid created_by FK
    }
    api_key_projects {
        uuid org_id FK
        uuid key_id FK
        uuid project_id FK
    }
```

Also tenant-scoped: `blog_categories`, `blog_tags`, `outbox_entries`, `bootstrap`.

## API keys

- **Three kinds, one table.** A `project` key has a `project_id` for life. An
  `org` key and a `personal` token have none and reach either every project
  (`all_projects`) or the rows in `api_key_projects`; a personal token's
  `created_by` is its owner. `api_keys_kind_shape` refuses any other mix.
- **A grant row cannot cross orgs.** Both foreign keys on `api_key_projects`
  are composite, `(org_id, key_id)` and `(org_id, project_id)`. RLS only
  checks the row's own `org_id`, and SQLite has no RLS at all.
- **Grants only widen.** The domain has no verb that removes a project; moving to
  all projects deletes the named rows in the same write.
- **New keys expire within a year** (`apikey.MaxLifetime`). `expires_at` may be
  null on existing rows.

## SECURITY DEFINER functions (Postgres)

Each one lifts RLS for a lookup that runs before any tenant scope exists.
All are owned by the migration role, with `EXECUTE` revoked from `PUBLIC`,
pinned by `TestMigrateUp_DefinerFunctionsAreOwnedByTheMigrationRole`.

| Function                                | Resolves                                |
| --------------------------------------- | --------------------------------------- |
| `resolve_api_key_by_secret_hash(bytea)` | a key and its project grant, by hash    |
| `resolve_org_by_slug(text)`             | an org from a path slug                 |
| `resolve_invite_by_token_hash(text)`    | an invite from its link                 |
| `list_pending_invites_for_email(text)`  | invites waiting for a signing-up user   |
| `list_orgs_for_user(uuid)`              | a user's orgs, to resolve their tenant  |
| `list_org_ids()`                        | every org id, for cross-tenant jobs     |
| `resolve_system_org()`                  | the singleton org on a selfhosted login |

## Adding a migration

1. Add `NNN_<name>.sql` under `migrations/postgres/` and
   `migrations/sqlite/`, each at its own next number. Use goose
   `-- +goose Up` / `-- +goose Down` markers and `{{.Schema}}` /
   `{{.TablePrefix}}` template variables.
2. Bump `VERSION` in each dialect to its highest `NNN`.
3. If the new table carries `org_id`, add its RLS policy in the Postgres
   migration and run `make tenant-tables` to regenerate the registry.
4. A join table between tenant rows takes composite `(org_id, …)` foreign
   keys, so a row cannot pair one org's parent with another org's child.
5. If the migration must run as owner (DDL), prefix the block with
   `{{if .Role}}SET ROLE {{.Role}};{{end}}`.
6. SQLite cannot alter a column, so a change there rebuilds the table
   (`<t>_new`, copy, drop, rename). **Dropping the old table fires every
   child's `ON DELETE` action**. Save and restore those links, as
   `017_org_api_keys.sql` does for `todos.created_by_key_id`, and cover it
   with a migration test that runs with `foreign_keys(1)`.

## opensheet

### Numbering

Postgres `001`–`005` and `012`–`020`, and SQLite `001`–`003` and `010`–`017`, are the template's,
content byte-identical to it. They are **never edited, renamed, renumbered or deleted**.
opensheet's own are Postgres `006`–`011` and SQLite `004`–`009`. A new template migration lands
at opensheet's next free number; a new opensheet migration takes the next free number too.

The template's later edits to Postgres `001_init` and `005_definer_functions` and SQLite
`001_init` are left out of every sync: opensheet `006_opensheet` (SQLite `004_opensheet`)
already creates `sessions` and refines the definer functions the same way. A fix opensheet needs
to a template object goes in a migration opensheet owns, never an edit to the template file.

`011_drop_legacy_api_keys` (SQLite `009`) drops opensheet's first `api_keys`, `api_key_sheets`
and `apikey_by_prefix` so the template's `api_keys` can follow. Those keys hashed only their
secret half, so they cannot be converted and must be re-minted.

NOTE: step 5 of "Adding a migration" does not hold. `templateVars` has no `Role` and rendering
uses `missingkey=error`, so `{{.Role}}` fails the migration. Migrations never `SET ROLE`; the
migrator connection does, from `db.migrator.role`.

### Sheet tables

All are tenant-scoped and carry `org_id` and `project_id`.

```mermaid
erDiagram
    users ||--o{ credentials : "authorized"
    projects ||--o{ credentials : "holds"
    projects ||--o{ spreadsheets : "registers"
    projects ||--o{ sheets : "publishes"
    credentials ||--o{ spreadsheets : "grants access"
    spreadsheets ||--o{ sheets : "has tabs"
    sheets ||--o{ sheet_snapshots : "cached as"
    sheets ||--o{ sheet_rows : "projected as"
    sheets ||--o{ sheet_write_attempts : "idempotency"

    credentials {
        uuid id PK
        text name "unique per project"
        text kind "service_account|google_oauth"
        text status "active|reauth_needed"
        uuid authorized_by_user_id FK
        text google_account_email
        bytea sealed "AES-256-GCM, AAD-bound"
    }
    spreadsheets {
        uuid id PK
        uuid credential_id FK "RESTRICT"
        text google_file_id "unique per project"
        text title
    }
    sheets {
        uuid id PK
        uuid spreadsheet_id FK "CASCADE"
        text tab "empty means first tab"
        text slug "unique per project"
        text visibility "key|public"
        int cache_ttl_secs "0 means default, max 86400"
        bool writable
        bigint generation "staleness guard and write lock"
        bool contract_ok
        text contract_reason
    }
    sheet_snapshots {
        uuid sheet_id PK
        text tab PK
        text etag
        bytea payload "exact bytes served"
        timestamptz expires_at
    }
    sheet_rows {
        uuid sheet_id PK
        text tab PK "resolved name, never empty"
        text row_id PK "the tab's id column"
        int row_index "position in the fetched tab"
        jsonb data "deleted_at stripped"
        timestamptz deleted_at "tombstone"
    }
    sheet_write_attempts {
        uuid sheet_id PK
        text tab PK
        text idem_key PK
        text body_hash
        bytea payload
        bool done
        timestamptz expires_at
    }
```

- `sheet_snapshots.payload` is `bytea`, not `jsonb`, because `etag` hashes those exact bytes and
  `jsonb` would reorder keys and normalise numbers.
- `sheet_rows` is the queryable projection of a published tab, keyed on the tab's `id` column.
  `row_index` counts an interior blank row, so the spreadsheet row is `row_index + 2`. Its `jsonb`
  keys come back in Postgres' order, so the served body is rebuilt with `encoding/json`.
- `sheets.generation` guards the projection: a refresh captures it before fetching and discards
  its write if it moved, and a `PATCH` holds it as a per-sheet write lock.

SECURITY: `current_org_id` (`002`) is not a definer function. Every RLS policy calls it in `USING`
as the querying role, so revoking `EXECUTE` on it breaks every tenant-scoped read, and only under
`db.allowBypassRLS=false`. Never redefine it.
