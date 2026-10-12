-- +goose Up
-- +goose StatementBegin

ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_keys
  ADD COLUMN kind          TEXT NOT NULL DEFAULT 'project',
  ADD COLUMN all_projects  BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN secret_hint   TEXT NOT NULL DEFAULT '',
  ADD COLUMN created_by    UUID REFERENCES {{.Schema}}.{{.TablePrefix}}users(id) ON DELETE SET NULL,
  ALTER COLUMN project_id DROP NOT NULL,
  ADD CONSTRAINT {{.TablePrefix}}api_keys_kind_shape CHECK (
    (kind = 'project' AND project_id IS NOT NULL AND NOT all_projects)
    OR (kind IN ('org', 'personal') AND project_id IS NULL)
  ),
  ADD CONSTRAINT {{.TablePrefix}}api_keys_org_id_id_key UNIQUE (org_id, id);

CREATE INDEX {{.TablePrefix}}api_keys_org_kind_idx
  ON {{.Schema}}.{{.TablePrefix}}api_keys (org_id, kind, created_at DESC);

CREATE INDEX {{.TablePrefix}}api_keys_personal_owner_idx
  ON {{.Schema}}.{{.TablePrefix}}api_keys (org_id, created_by, created_at DESC)
  WHERE kind = 'personal';

ALTER TABLE {{.Schema}}.{{.TablePrefix}}projects
  ADD CONSTRAINT {{.TablePrefix}}projects_org_id_id_key UNIQUE (org_id, id);

-- SECURITY: both composite keys carry org_id, so a grant row can name only a key and a project of its own org; RLS alone checks the row's org_id, never whether project_id belongs to it.
CREATE TABLE {{.Schema}}.{{.TablePrefix}}api_key_projects (
  org_id      UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  key_id      UUID NOT NULL,
  project_id  UUID NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (key_id, project_id),
  FOREIGN KEY (org_id, key_id) REFERENCES {{.Schema}}.{{.TablePrefix}}api_keys(org_id, id) ON DELETE CASCADE,
  FOREIGN KEY (org_id, project_id) REFERENCES {{.Schema}}.{{.TablePrefix}}projects(org_id, id) ON DELETE CASCADE
);

CREATE INDEX {{.TablePrefix}}api_key_projects_org_project_idx
  ON {{.Schema}}.{{.TablePrefix}}api_key_projects (org_id, project_id);

{{if .RLSEnforce}}
ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_key_projects ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_key_projects FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}api_key_projects_tenant
  ON {{.Schema}}.{{.TablePrefix}}api_key_projects
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());
{{end}}

DROP FUNCTION IF EXISTS {{.Schema}}.{{.TablePrefix}}resolve_api_key_by_secret_hash(bytea);

-- SECURITY: resolves a key and its project grant by secret hash before any tenant scope exists — the row is what names the org. The SECURITY DEFINER wrapper lifts RLS; the hash is unguessable.
CREATE FUNCTION {{.Schema}}.{{.TablePrefix}}resolve_api_key_by_secret_hash(p_hash bytea)
RETURNS TABLE (id uuid, org_id uuid, project_id uuid, kind text, all_projects boolean, project_ids text, name text, secret_hash bytea, secret_hint text, scopes text, resource_ids text, created_by uuid, created_at timestamptz, expires_at timestamptz, revoked_at timestamptz, last_used_at timestamptz)
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = {{.Schema}}, pg_catalog, pg_temp
AS $$
  SELECT k.id, k.org_id, k.project_id, k.kind, k.all_projects,
         COALESCE((SELECT array_agg(g.project_id ORDER BY g.created_at, g.project_id)
                   FROM {{.Schema}}.{{.TablePrefix}}api_key_projects g
                   WHERE g.org_id = k.org_id AND g.key_id = k.id), '{}')::text,
         k.name, k.secret_hash, k.secret_hint, k.scopes::text, k.resource_ids::text,
         k.created_by, k.created_at, k.expires_at, k.revoked_at, k.last_used_at
  FROM {{.Schema}}.{{.TablePrefix}}api_keys k
  WHERE k.secret_hash = p_hash
  LIMIT 1;
$$;

REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}resolve_api_key_by_secret_hash(bytea) FROM PUBLIC;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP FUNCTION IF EXISTS {{.Schema}}.{{.TablePrefix}}resolve_api_key_by_secret_hash(bytea);

CREATE FUNCTION {{.Schema}}.{{.TablePrefix}}resolve_api_key_by_secret_hash(p_hash bytea)
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

DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}api_key_projects;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}projects DROP CONSTRAINT IF EXISTS {{.TablePrefix}}projects_org_id_id_key;
DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}api_keys_personal_owner_idx;
DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}api_keys_org_kind_idx;

-- NOTE: org keys and personal tokens cannot satisfy the restored NOT NULL project_id and are dropped.
DELETE FROM {{.Schema}}.{{.TablePrefix}}api_keys WHERE kind <> 'project';
ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_keys
  DROP CONSTRAINT IF EXISTS {{.TablePrefix}}api_keys_org_id_id_key,
  DROP CONSTRAINT IF EXISTS {{.TablePrefix}}api_keys_kind_shape,
  ALTER COLUMN project_id SET NOT NULL,
  DROP COLUMN created_by,
  DROP COLUMN secret_hint,
  DROP COLUMN all_projects,
  DROP COLUMN kind;

-- +goose StatementEnd
