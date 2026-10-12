-- +goose Up
-- +goose StatementBegin

CREATE TABLE {{.Schema}}.{{.TablePrefix}}api_keys (
  id                  UUID PRIMARY KEY,
  org_id              UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}projects(id) ON DELETE CASCADE,
  name                TEXT NOT NULL,
  secret_hash         BYTEA NOT NULL,
  scopes              TEXT[] NOT NULL DEFAULT '{}',
  resource_ids        UUID[] NOT NULL DEFAULT '{}',
  created_at          TIMESTAMPTZ NOT NULL,
  expires_at          TIMESTAMPTZ,
  revoked_at          TIMESTAMPTZ,
  last_used_at        TIMESTAMPTZ
);

CREATE UNIQUE INDEX {{.TablePrefix}}api_keys_secret_hash_key
  ON {{.Schema}}.{{.TablePrefix}}api_keys (secret_hash);

CREATE INDEX {{.TablePrefix}}api_keys_project_idx
  ON {{.Schema}}.{{.TablePrefix}}api_keys (org_id, project_id);

{{if .RLSEnforce}}
ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}api_keys_tenant
  ON {{.Schema}}.{{.TablePrefix}}api_keys
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());
{{end}}

-- SECURITY: resolves a key by its secret hash before any tenant scope exists — the row is what names the org. The SECURITY DEFINER wrapper lifts RLS; the hash is unguessable.
CREATE OR REPLACE FUNCTION {{.Schema}}.{{.TablePrefix}}resolve_api_key_by_secret_hash(p_hash bytea)
RETURNS TABLE (id uuid, org_id uuid, project_id uuid, name text, secret_hash bytea, scopes text, resource_ids text, created_at timestamptz, expires_at timestamptz, revoked_at timestamptz, last_used_at timestamptz)
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = {{.Schema}}, pg_catalog, pg_temp
AS $$
  SELECT k.id, k.org_id, k.project_id, k.name, k.secret_hash, k.scopes::text, k.resource_ids::text,
         k.created_at, k.expires_at, k.revoked_at, k.last_used_at
  FROM {{.Schema}}.{{.TablePrefix}}api_keys k
  WHERE k.secret_hash = p_hash
  LIMIT 1;
$$;

REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}resolve_api_key_by_secret_hash(bytea) FROM PUBLIC;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP FUNCTION IF EXISTS {{.Schema}}.{{.TablePrefix}}resolve_api_key_by_secret_hash(bytea);
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}api_keys;

-- +goose StatementEnd
