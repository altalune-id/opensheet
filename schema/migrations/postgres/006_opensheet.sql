-- +goose Up
-- +goose StatementBegin

-- SECURITY: pg_temp must stay last in search_path — https://www.postgresql.org/docs/17/sql-createfunction.html#SQL-CREATEFUNCTION-SECURITY
CREATE OR REPLACE FUNCTION {{.Schema}}.{{.TablePrefix}}list_orgs_for_user(p_user_id uuid)
RETURNS TABLE (id uuid, slug text, name text, created_by uuid, created_at timestamptz, system boolean)
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = {{.Schema}}, pg_catalog, pg_temp
AS $$
  SELECT o.id, o.slug, o.name, o.created_by, o.created_at, o.system
  FROM {{.Schema}}.{{.TablePrefix}}orgs o
  INNER JOIN {{.Schema}}.{{.TablePrefix}}memberships m ON m.org_id = o.id
  WHERE m.user_id = p_user_id
  ORDER BY o.created_at ASC, o.id ASC;
$$;

CREATE OR REPLACE FUNCTION {{.Schema}}.{{.TablePrefix}}list_pending_invites_for_email(p_email text)
RETURNS TABLE (id uuid, org_id uuid, email text, role text, token_hash text, expires_at timestamptz, accepted_at timestamptz, created_at timestamptz)
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = {{.Schema}}, pg_catalog, pg_temp
AS $$
  SELECT i.id, i.org_id, i.email, i.role, i.token_hash, i.expires_at, i.accepted_at, i.created_at
  FROM {{.Schema}}.{{.TablePrefix}}invites i
  WHERE i.email = p_email AND i.accepted_at IS NULL
  ORDER BY i.created_at ASC, i.id ASC;
$$;

REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}list_orgs_for_user(uuid) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}list_pending_invites_for_email(text) FROM PUBLIC;

DROP FUNCTION {{.Schema}}.{{.TablePrefix}}list_org_ids();

CREATE FUNCTION {{.Schema}}.{{.TablePrefix}}list_org_ids()
RETURNS TABLE (id uuid, created_at timestamptz)
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = {{.Schema}}, pg_catalog, pg_temp
AS $$
  SELECT o.id, o.created_at FROM {{.Schema}}.{{.TablePrefix}}orgs o ORDER BY o.created_at ASC, o.id ASC;
$$;

REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}list_org_ids() FROM PUBLIC;

CREATE TABLE {{.Schema}}.{{.TablePrefix}}credentials (
  id                    UUID PRIMARY KEY,
  org_id                UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id            UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}projects(id) ON DELETE CASCADE,
  name                  TEXT NOT NULL,
  kind                  TEXT NOT NULL CHECK (kind IN ('service_account','google_oauth')),
  status                TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','reauth_needed')),
  authorized_by_user_id UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}users(id),
  google_account_email  TEXT NOT NULL DEFAULT '',
  -- SECURITY: ciphertext only, sealed with AAD org_id||project_id||id — a row moved to another tenant cannot be opened.
  sealed                BYTEA NOT NULL,
  created_at            TIMESTAMPTZ NOT NULL,
  updated_at            TIMESTAMPTZ NOT NULL,
  UNIQUE (project_id, name)
);

CREATE INDEX {{.TablePrefix}}credentials_org_project_idx
  ON {{.Schema}}.{{.TablePrefix}}credentials (org_id, project_id);

CREATE INDEX {{.TablePrefix}}credentials_authorized_by_idx
  ON {{.Schema}}.{{.TablePrefix}}credentials (authorized_by_user_id);

CREATE TABLE {{.Schema}}.{{.TablePrefix}}spreadsheets (
  id                  UUID PRIMARY KEY,
  org_id              UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}projects(id) ON DELETE CASCADE,
  -- RESTRICT, not CASCADE: deleting a credential that still reads documents must fail loudly as CRD005.
  credential_id       UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}credentials(id) ON DELETE RESTRICT,
  google_file_id      TEXT NOT NULL,
  title               TEXT NOT NULL DEFAULT '',
  created_at          TIMESTAMPTZ NOT NULL,
  updated_at          TIMESTAMPTZ NOT NULL,
  -- SECURITY: spreadsheets:write is already mintable but enforced nowhere, so keys carrying it exist; defaulting to false stops the tab-creation route from granting them write access retroactively when it deploys.
  writable            BOOLEAN NOT NULL DEFAULT false,
  UNIQUE (project_id, google_file_id)
);

CREATE INDEX {{.TablePrefix}}spreadsheets_org_project_idx
  ON {{.Schema}}.{{.TablePrefix}}spreadsheets (org_id, project_id);

CREATE INDEX {{.TablePrefix}}spreadsheets_credential_idx
  ON {{.Schema}}.{{.TablePrefix}}spreadsheets (credential_id);

CREATE TABLE {{.Schema}}.{{.TablePrefix}}sheets (
  id                  UUID PRIMARY KEY,
  org_id              UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}projects(id) ON DELETE CASCADE,
  spreadsheet_id      UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}spreadsheets(id) ON DELETE CASCADE,
  -- Empty tab means "the spreadsheet's first tab", resolved at read time.
  tab                 TEXT NOT NULL DEFAULT '',
  slug                TEXT NOT NULL,
  visibility          TEXT NOT NULL DEFAULT 'key' CHECK (visibility IN ('key','public')),
  -- 0 means "use cache.defaultTTL"; the aggregate otherwise bounds it to [1s, 24h].
  cache_ttl_secs      INTEGER NOT NULL DEFAULT 0 CHECK (cache_ttl_secs >= 0 AND cache_ttl_secs <= 86400),
  created_at          TIMESTAMPTZ NOT NULL,
  updated_at          TIMESTAMPTZ NOT NULL,
  -- SECURITY: defaults to false, so no sheet published before this migration becomes writable by deploying it.
  writable            BOOLEAN NOT NULL DEFAULT false,
  UNIQUE (project_id, slug)
);

CREATE INDEX {{.TablePrefix}}sheets_org_project_idx
  ON {{.Schema}}.{{.TablePrefix}}sheets (org_id, project_id);

CREATE INDEX {{.TablePrefix}}sheets_spreadsheet_idx
  ON {{.Schema}}.{{.TablePrefix}}sheets (spreadsheet_id);

CREATE TABLE {{.Schema}}.{{.TablePrefix}}api_keys (
  id                  UUID PRIMARY KEY,
  org_id              UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}projects(id) ON DELETE CASCADE,
  name                TEXT NOT NULL,
  key_prefix          TEXT NOT NULL UNIQUE,
  -- SECURITY: SHA-256 of the 256-bit secret half. The plaintext key is returned once, at mint, and never stored.
  secret_hash         BYTEA NOT NULL,
  scopes              TEXT[] NOT NULL DEFAULT '{}',
  expires_at          TIMESTAMPTZ,
  last_used_at        TIMESTAMPTZ,
  revoked_at          TIMESTAMPTZ,
  created_at          TIMESTAMPTZ NOT NULL
);

CREATE INDEX {{.TablePrefix}}api_keys_org_project_idx
  ON {{.Schema}}.{{.TablePrefix}}api_keys (org_id, project_id);

CREATE TABLE {{.Schema}}.{{.TablePrefix}}api_key_sheets (
  api_key_id          UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}api_keys(id) ON DELETE CASCADE,
  sheet_id            UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}sheets(id) ON DELETE CASCADE,
  -- A join table still needs its own RLS policy, which needs its own org_id.
  org_id              UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  PRIMARY KEY (api_key_id, sheet_id)
);

CREATE INDEX {{.TablePrefix}}api_key_sheets_sheet_idx
  ON {{.Schema}}.{{.TablePrefix}}api_key_sheets (sheet_id);

CREATE INDEX {{.TablePrefix}}api_key_sheets_org_idx
  ON {{.Schema}}.{{.TablePrefix}}api_key_sheets (org_id);

CREATE TABLE {{.Schema}}.{{.TablePrefix}}sheet_snapshots (
  sheet_id            UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}sheets(id) ON DELETE CASCADE,
  -- The RESOLVED tab name, never '', so renaming the first tab cannot keep serving the old one.
  tab                 TEXT NOT NULL,
  etag                TEXT NOT NULL,
  -- BYTEA, not JSONB: jsonb reorders keys and normalizes numbers, so it would not round-trip the exact bytes the etag hashes.
  payload             BYTEA NOT NULL,
  fetched_at          TIMESTAMPTZ NOT NULL,
  expires_at          TIMESTAMPTZ NOT NULL,
  org_id              UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}projects(id) ON DELETE CASCADE,
  PRIMARY KEY (sheet_id, tab)
);

CREATE INDEX {{.TablePrefix}}sheet_snapshots_org_project_idx
  ON {{.Schema}}.{{.TablePrefix}}sheet_snapshots (org_id, project_id);

CREATE INDEX {{.TablePrefix}}sheet_snapshots_expires_idx
  ON {{.Schema}}.{{.TablePrefix}}sheet_snapshots (expires_at);

CREATE TABLE {{.Schema}}.{{.TablePrefix}}sessions (
  sid         TEXT PRIMARY KEY,
  user_id     UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}users(id) ON DELETE CASCADE,
  -- SECURITY: sealed Principal, AAD-bound to sid — it carries a live IdP ID token.
  payload     BYTEA NOT NULL,
  expires_at  TIMESTAMPTZ NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL
);

CREATE INDEX {{.TablePrefix}}sessions_expires_idx
  ON {{.Schema}}.{{.TablePrefix}}sessions (expires_at);

CREATE INDEX {{.TablePrefix}}sessions_user_idx
  ON {{.Schema}}.{{.TablePrefix}}sessions (user_id);

CREATE TABLE {{.Schema}}.{{.TablePrefix}}sheet_write_attempts (
  sheet_id            UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}sheets(id) ON DELETE CASCADE,
  tab                 TEXT NOT NULL,
  -- NOTE: idem_key, not key: `ON CONFLICT (sheet_id, tab, key)` beside `SET key = key` reads as a trap.
  idem_key            TEXT NOT NULL,
  body_hash           TEXT NOT NULL,
  -- NOTE: nullable, because the no-op DO UPDATE returns the freshly inserted row on the claimed path, where no payload exists yet.
  payload             BYTEA,
  done                BOOLEAN NOT NULL,
  -- NOTE: read back through RETURNING to tell an insert from a conflict; created_at cannot serve, because timestamptz truncates sub-microsecond nanoseconds.
  claim_token         UUID NOT NULL,
  created_at          TIMESTAMPTZ NOT NULL,
  expires_at          TIMESTAMPTZ NOT NULL,
  org_id              UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}projects(id) ON DELETE CASCADE,
  PRIMARY KEY (sheet_id, tab, idem_key)
);

CREATE INDEX {{.TablePrefix}}sheet_write_attempts_org_project_idx
  ON {{.Schema}}.{{.TablePrefix}}sheet_write_attempts (org_id, project_id);

CREATE INDEX {{.TablePrefix}}sheet_write_attempts_expires_idx
  ON {{.Schema}}.{{.TablePrefix}}sheet_write_attempts (expires_at);

{{if .RLSEnforce}}
ALTER TABLE {{.Schema}}.{{.TablePrefix}}credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}credentials FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}credentials_tenant
  ON {{.Schema}}.{{.TablePrefix}}credentials
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());

ALTER TABLE {{.Schema}}.{{.TablePrefix}}spreadsheets ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}spreadsheets FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}spreadsheets_tenant
  ON {{.Schema}}.{{.TablePrefix}}spreadsheets
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());

ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheets ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheets FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}sheets_tenant
  ON {{.Schema}}.{{.TablePrefix}}sheets
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());

ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}api_keys_tenant
  ON {{.Schema}}.{{.TablePrefix}}api_keys
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());

-- A join table still needs its own policy; without one it would be readable across tenants.
ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_key_sheets ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_key_sheets FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}api_key_sheets_tenant
  ON {{.Schema}}.{{.TablePrefix}}api_key_sheets
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());

ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheet_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheet_snapshots FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}sheet_snapshots_tenant
  ON {{.Schema}}.{{.TablePrefix}}sheet_snapshots
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());

ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheet_write_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheet_write_attempts FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}sheet_write_attempts_tenant
  ON {{.Schema}}.{{.TablePrefix}}sheet_write_attempts
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());
{{end}}

-- SECURITY: the one intentional RLS bypass on the API-key path — a presented key must resolve
-- its own org before any tenant scope exists. Everything else about an api_key is read
-- through the tenanted store.
-- SECURITY: pg_temp must stay last in search_path — https://www.postgresql.org/docs/17/sql-createfunction.html#SQL-CREATEFUNCTION-SECURITY
CREATE OR REPLACE FUNCTION {{.Schema}}.{{.TablePrefix}}apikey_by_prefix(p_prefix text)
RETURNS SETOF {{.Schema}}.{{.TablePrefix}}api_keys
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = {{.Schema}}, pg_catalog, pg_temp
AS $$
  SELECT k.*
  FROM {{.Schema}}.{{.TablePrefix}}api_keys k
  WHERE k.key_prefix = p_prefix
  LIMIT 1;
$$;

REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}apikey_by_prefix(text) FROM PUBLIC;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP FUNCTION IF EXISTS {{.Schema}}.{{.TablePrefix}}apikey_by_prefix(text);

{{if .RLSEnforce}}
DROP POLICY IF EXISTS {{.TablePrefix}}sheet_write_attempts_tenant ON {{.Schema}}.{{.TablePrefix}}sheet_write_attempts;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheet_write_attempts NO FORCE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheet_write_attempts DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS {{.TablePrefix}}sheet_snapshots_tenant ON {{.Schema}}.{{.TablePrefix}}sheet_snapshots;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheet_snapshots NO FORCE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheet_snapshots DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS {{.TablePrefix}}api_key_sheets_tenant ON {{.Schema}}.{{.TablePrefix}}api_key_sheets;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_key_sheets NO FORCE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_key_sheets DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS {{.TablePrefix}}api_keys_tenant ON {{.Schema}}.{{.TablePrefix}}api_keys;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_keys NO FORCE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_keys DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS {{.TablePrefix}}sheets_tenant ON {{.Schema}}.{{.TablePrefix}}sheets;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheets NO FORCE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheets DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS {{.TablePrefix}}spreadsheets_tenant ON {{.Schema}}.{{.TablePrefix}}spreadsheets;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}spreadsheets NO FORCE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}spreadsheets DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS {{.TablePrefix}}credentials_tenant ON {{.Schema}}.{{.TablePrefix}}credentials;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}credentials NO FORCE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}credentials DISABLE ROW LEVEL SECURITY;
{{end}}

DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}sheet_write_attempts_expires_idx;
DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}sheet_write_attempts_org_project_idx;
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}sheet_write_attempts;

DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}sessions_user_idx;
DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}sessions_expires_idx;
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}sessions;

DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}sheet_snapshots_expires_idx;
DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}sheet_snapshots_org_project_idx;
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}sheet_snapshots;

DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}api_key_sheets_org_idx;
DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}api_key_sheets_sheet_idx;
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}api_key_sheets;

DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}api_keys_org_project_idx;
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}api_keys;

DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}sheets_spreadsheet_idx;
DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}sheets_org_project_idx;
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}sheets;

DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}spreadsheets_credential_idx;
DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}spreadsheets_org_project_idx;
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}spreadsheets;

DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}credentials_authorized_by_idx;
DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}credentials_org_project_idx;
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}credentials;

DROP FUNCTION IF EXISTS {{.Schema}}.{{.TablePrefix}}list_org_ids();

CREATE FUNCTION {{.Schema}}.{{.TablePrefix}}list_org_ids()
RETURNS TABLE (id uuid)
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = {{.Schema}}, pg_catalog, pg_temp
AS $$
  SELECT o.id FROM {{.Schema}}.{{.TablePrefix}}orgs o ORDER BY o.created_at;
$$;

CREATE OR REPLACE FUNCTION {{.Schema}}.{{.TablePrefix}}list_orgs_for_user(p_user_id uuid)
RETURNS TABLE (id uuid, slug text, name text, created_by uuid, created_at timestamptz, system boolean)
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = {{.Schema}}, pg_catalog, pg_temp
AS $$
  SELECT o.id, o.slug, o.name, o.created_by, o.created_at, o.system
  FROM {{.Schema}}.{{.TablePrefix}}orgs o
  INNER JOIN {{.Schema}}.{{.TablePrefix}}memberships m ON m.org_id = o.id
  WHERE m.user_id = p_user_id
  ORDER BY o.created_at ASC;
$$;

CREATE OR REPLACE FUNCTION {{.Schema}}.{{.TablePrefix}}list_pending_invites_for_email(p_email text)
RETURNS TABLE (id uuid, org_id uuid, email text, role text, token_hash text, expires_at timestamptz, accepted_at timestamptz, created_at timestamptz)
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = {{.Schema}}, pg_catalog, pg_temp
AS $$
  SELECT i.id, i.org_id, i.email, i.role, i.token_hash, i.expires_at, i.accepted_at, i.created_at
  FROM {{.Schema}}.{{.TablePrefix}}invites i
  WHERE i.email = p_email AND i.accepted_at IS NULL
  ORDER BY i.created_at ASC;
$$;

REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}list_org_ids() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}list_orgs_for_user(uuid) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}list_pending_invites_for_email(text) FROM PUBLIC;

-- +goose StatementEnd
