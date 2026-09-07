# schema

Embedded goose migrations for Postgres and SQLite, the runtime migrator, the
RLS boot-time guard, and the tenant-table registry consumed by `tenant.PgConn`.

Migrations are `.sql` templates rendered with `{{.Schema}}`, `{{.TablePrefix}}`,
and `{{.Role}}` before goose applies them. Both dialects satisfy the same
domain interfaces — SQLite is dev/demo, Postgres is production.

## Layout

```
schema/
├── migrations/postgres/    # 001_init, 002_rls, 003_bootstrap, VERSION
├── migrations/sqlite/      # 001_init, 002_bootstrap, VERSION
├── migrator.go             # goose runner, template rendering, embed FS
├── migrator_templatefs.go  # per-boot template-rendered file system
├── rls_guard.go            # boot-time BYPASSRLS assertion
└── tenant_tables_gen.go    # generated from RLS migrations (make tenant-tables)
```

`TenantTableSuffixes` (in `tenant_tables_gen.go`) drives RLS policy
enforcement — every table in this list carries `org_id` and gets
`FORCE ROW LEVEL SECURITY` on Postgres. Regenerate with
`make tenant-tables` after adding a tenant-scoped table.

## Tables

```mermaid
erDiagram
    users ||--o{ memberships : "belongs to"
    users ||--o{ orgs : "created"
    users ||--o{ invites : "invited by"
    users ||--o{ todos : "assigned to"
    orgs ||--o{ memberships : "has"
    orgs ||--o{ projects : "owns"
    orgs ||--o{ invites : "pending"
    orgs ||--o{ todos : "scopes"
    projects ||--o{ todos : "contains"

    users {
        uuid id PK
        text email UK
        text idp_issuer
        text idp_subject
        text name
        text password_hash
        bool is_admin
        text locale
    }
    orgs {
        uuid id PK
        text slug UK
        text name
        bool system
        uuid created_by FK
    }
    memberships {
        uuid id PK
        uuid org_id FK
        uuid user_id FK
        text role "owner|admin|member"
        bool system
    }
    projects {
        uuid id PK
        uuid org_id FK
        text slug
        text name
        bool system
        uuid created_by FK
    }
    invites {
        uuid id PK
        uuid org_id FK
        text email
        text role
        text token_hash
        timestamptz expires_at
        timestamptz accepted_at
        uuid invited_by FK
    }
    todos {
        uuid id PK
        uuid org_id FK
        uuid project_id FK
        uuid user_id FK
        text title
        bool done
    }
```

`users` is global (no `org_id`); everything else is tenant-scoped and
appears in `TenantTableSuffixes`.

## Adding a migration

1. Add `NNN_<name>.sql` under both `migrations/postgres/` and
   `migrations/sqlite/`. Use goose `-- +goose Up` / `-- +goose Down`
   markers and `{{.Schema}}` / `{{.TablePrefix}}` template variables.
2. Bump `VERSION` in the affected dialect(s) to the highest `NNN` present.
3. If the new table carries `org_id`, add its RLS policy in the Postgres
   migration and run `make tenant-tables` to regenerate the registry.
4. If the migration must run as owner (DDL), prefix the block with
   `{{if .Role}}SET ROLE {{.Role}};{{end}}`.
