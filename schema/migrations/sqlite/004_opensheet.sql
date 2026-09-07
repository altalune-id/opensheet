-- +goose Up
-- +goose StatementBegin

CREATE TABLE {{.TablePrefix}}credentials (
  id                    TEXT PRIMARY KEY,
  org_id                TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id            TEXT NOT NULL REFERENCES {{.TablePrefix}}projects(id) ON DELETE CASCADE,
  name                  TEXT NOT NULL,
  kind                  TEXT NOT NULL CHECK (kind IN ('service_account','google_oauth')),
  status                TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','reauth_needed')),
  authorized_by_user_id TEXT NOT NULL REFERENCES {{.TablePrefix}}users(id),
  google_account_email  TEXT NOT NULL DEFAULT '',
  -- SECURITY: ciphertext only, sealed with AAD org_id||project_id||id — a row moved to another tenant cannot be opened.
  sealed                BLOB NOT NULL,
  created_at            TEXT NOT NULL,
  updated_at            TEXT NOT NULL,
  UNIQUE (project_id, name)
);

CREATE INDEX {{.TablePrefix}}credentials_org_project_idx
  ON {{.TablePrefix}}credentials (org_id, project_id);

CREATE INDEX {{.TablePrefix}}credentials_authorized_by_idx
  ON {{.TablePrefix}}credentials (authorized_by_user_id);

CREATE TABLE {{.TablePrefix}}spreadsheets (
  id                  TEXT PRIMARY KEY,
  org_id              TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          TEXT NOT NULL REFERENCES {{.TablePrefix}}projects(id) ON DELETE CASCADE,
  -- RESTRICT, not CASCADE: deleting a credential that still reads documents must fail loudly as CRD005.
  credential_id       TEXT NOT NULL REFERENCES {{.TablePrefix}}credentials(id) ON DELETE RESTRICT,
  google_file_id      TEXT NOT NULL,
  title               TEXT NOT NULL DEFAULT '',
  created_at          TEXT NOT NULL,
  updated_at          TEXT NOT NULL,
  UNIQUE (project_id, google_file_id)
);

CREATE INDEX {{.TablePrefix}}spreadsheets_org_project_idx
  ON {{.TablePrefix}}spreadsheets (org_id, project_id);

CREATE INDEX {{.TablePrefix}}spreadsheets_credential_idx
  ON {{.TablePrefix}}spreadsheets (credential_id);

CREATE TABLE {{.TablePrefix}}sheets (
  id                  TEXT PRIMARY KEY,
  org_id              TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          TEXT NOT NULL REFERENCES {{.TablePrefix}}projects(id) ON DELETE CASCADE,
  spreadsheet_id      TEXT NOT NULL REFERENCES {{.TablePrefix}}spreadsheets(id) ON DELETE CASCADE,
  -- Empty tab means "the spreadsheet's first tab", resolved at read time.
  tab                 TEXT NOT NULL DEFAULT '',
  slug                TEXT NOT NULL,
  visibility          TEXT NOT NULL DEFAULT 'key' CHECK (visibility IN ('key','public')),
  -- 0 means "use cache.defaultTTL"; the aggregate otherwise bounds it to [1s, 24h].
  cache_ttl_secs      INTEGER NOT NULL DEFAULT 0 CHECK (cache_ttl_secs >= 0 AND cache_ttl_secs <= 86400),
  created_at          TEXT NOT NULL,
  updated_at          TEXT NOT NULL,
  UNIQUE (project_id, slug)
);

CREATE INDEX {{.TablePrefix}}sheets_org_project_idx
  ON {{.TablePrefix}}sheets (org_id, project_id);

CREATE INDEX {{.TablePrefix}}sheets_spreadsheet_idx
  ON {{.TablePrefix}}sheets (spreadsheet_id);

CREATE TABLE {{.TablePrefix}}api_keys (
  id                  TEXT PRIMARY KEY,
  org_id              TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          TEXT NOT NULL REFERENCES {{.TablePrefix}}projects(id) ON DELETE CASCADE,
  name                TEXT NOT NULL,
  key_prefix          TEXT NOT NULL UNIQUE,
  -- SECURITY: SHA-256 of the 256-bit secret half. The plaintext key is returned once, at mint, and never stored.
  secret_hash         BLOB NOT NULL,
  -- JSON array of scope strings; SQLite has no array type.
  scopes              TEXT NOT NULL DEFAULT '[]',
  expires_at          TEXT,
  last_used_at        TEXT,
  revoked_at          TEXT,
  created_at          TEXT NOT NULL
);

CREATE INDEX {{.TablePrefix}}api_keys_org_project_idx
  ON {{.TablePrefix}}api_keys (org_id, project_id);

CREATE TABLE {{.TablePrefix}}api_key_sheets (
  api_key_id          TEXT NOT NULL REFERENCES {{.TablePrefix}}api_keys(id) ON DELETE CASCADE,
  sheet_id            TEXT NOT NULL REFERENCES {{.TablePrefix}}sheets(id) ON DELETE CASCADE,
  -- Mirrors the Postgres table, whose org_id exists so the join table can carry its own RLS policy.
  org_id              TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  PRIMARY KEY (api_key_id, sheet_id)
);

CREATE INDEX {{.TablePrefix}}api_key_sheets_sheet_idx
  ON {{.TablePrefix}}api_key_sheets (sheet_id);

CREATE INDEX {{.TablePrefix}}api_key_sheets_org_idx
  ON {{.TablePrefix}}api_key_sheets (org_id);

CREATE TABLE {{.TablePrefix}}sheet_snapshots (
  sheet_id            TEXT NOT NULL REFERENCES {{.TablePrefix}}sheets(id) ON DELETE CASCADE,
  -- The RESOLVED tab name, never '', so renaming the first tab cannot keep serving the old one.
  tab                 TEXT NOT NULL,
  etag                TEXT NOT NULL,
  -- BLOB, not TEXT: the etag hashes these exact bytes, so they must round-trip unchanged.
  payload             BLOB NOT NULL,
  fetched_at          TEXT NOT NULL,
  expires_at          TEXT NOT NULL,
  org_id              TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          TEXT NOT NULL REFERENCES {{.TablePrefix}}projects(id) ON DELETE CASCADE,
  PRIMARY KEY (sheet_id, tab)
);

CREATE INDEX {{.TablePrefix}}sheet_snapshots_org_project_idx
  ON {{.TablePrefix}}sheet_snapshots (org_id, project_id);

CREATE INDEX {{.TablePrefix}}sheet_snapshots_expires_idx
  ON {{.TablePrefix}}sheet_snapshots (expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS {{.TablePrefix}}sheet_snapshots_expires_idx;
DROP INDEX IF EXISTS {{.TablePrefix}}sheet_snapshots_org_project_idx;
DROP TABLE IF EXISTS {{.TablePrefix}}sheet_snapshots;

DROP INDEX IF EXISTS {{.TablePrefix}}api_key_sheets_org_idx;
DROP INDEX IF EXISTS {{.TablePrefix}}api_key_sheets_sheet_idx;
DROP TABLE IF EXISTS {{.TablePrefix}}api_key_sheets;

DROP INDEX IF EXISTS {{.TablePrefix}}api_keys_org_project_idx;
DROP TABLE IF EXISTS {{.TablePrefix}}api_keys;

DROP INDEX IF EXISTS {{.TablePrefix}}sheets_spreadsheet_idx;
DROP INDEX IF EXISTS {{.TablePrefix}}sheets_org_project_idx;
DROP TABLE IF EXISTS {{.TablePrefix}}sheets;

DROP INDEX IF EXISTS {{.TablePrefix}}spreadsheets_credential_idx;
DROP INDEX IF EXISTS {{.TablePrefix}}spreadsheets_org_project_idx;
DROP TABLE IF EXISTS {{.TablePrefix}}spreadsheets;

DROP INDEX IF EXISTS {{.TablePrefix}}credentials_authorized_by_idx;
DROP INDEX IF EXISTS {{.TablePrefix}}credentials_org_project_idx;
DROP TABLE IF EXISTS {{.TablePrefix}}credentials;

-- +goose StatementEnd
