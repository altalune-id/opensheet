-- +goose Up
-- +goose StatementBegin

-- NOTE: opensheet's osk_ keys hashed only the secret half and resolved by key_prefix; the template's api_keys that follow hash the whole plaintext, so legacy keys cannot be converted and must be re-minted.
DROP FUNCTION IF EXISTS {{.Schema}}.{{.TablePrefix}}apikey_by_prefix(text);
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}api_key_sheets;
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}api_keys;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE TABLE {{.Schema}}.{{.TablePrefix}}api_keys (
  id                  UUID PRIMARY KEY,
  org_id              UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}projects(id) ON DELETE CASCADE,
  name                TEXT NOT NULL,
  key_prefix          TEXT NOT NULL UNIQUE,
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
  org_id              UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  PRIMARY KEY (api_key_id, sheet_id)
);

CREATE INDEX {{.TablePrefix}}api_key_sheets_sheet_idx
  ON {{.Schema}}.{{.TablePrefix}}api_key_sheets (sheet_id);

CREATE INDEX {{.TablePrefix}}api_key_sheets_org_idx
  ON {{.Schema}}.{{.TablePrefix}}api_key_sheets (org_id);

{{if .RLSEnforce}}
ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}api_keys_tenant
  ON {{.Schema}}.{{.TablePrefix}}api_keys
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());

ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_key_sheets ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_key_sheets FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}api_key_sheets_tenant
  ON {{.Schema}}.{{.TablePrefix}}api_key_sheets
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());
{{end}}

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
