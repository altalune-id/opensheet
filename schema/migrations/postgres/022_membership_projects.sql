-- +goose Up
-- +goose StatementBegin

ALTER TABLE {{.Schema}}.{{.TablePrefix}}memberships
  ADD COLUMN all_projects BOOLEAN NOT NULL DEFAULT true,
  ADD CONSTRAINT {{.TablePrefix}}memberships_org_id_id_key UNIQUE (org_id, id),
  ADD CONSTRAINT {{.TablePrefix}}memberships_owner_all_projects CHECK (role <> 'owner' OR all_projects);

ALTER TABLE {{.Schema}}.{{.TablePrefix}}invites
  ADD COLUMN all_projects BOOLEAN NOT NULL DEFAULT true,
  ADD CONSTRAINT {{.TablePrefix}}invites_org_id_id_key UNIQUE (org_id, id),
  ADD CONSTRAINT {{.TablePrefix}}invites_owner_all_projects CHECK (role <> 'owner' OR all_projects);

-- SECURITY: both composite keys carry org_id, so a grant row can name only a membership or invite and a project of its own org; RLS alone checks the row's org_id, never whether project_id belongs to it.
CREATE TABLE {{.Schema}}.{{.TablePrefix}}membership_projects (
  org_id         UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  membership_id  UUID NOT NULL,
  project_id     UUID NOT NULL,
  created_at     TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (membership_id, project_id),
  FOREIGN KEY (org_id, membership_id) REFERENCES {{.Schema}}.{{.TablePrefix}}memberships(org_id, id) ON DELETE CASCADE,
  FOREIGN KEY (org_id, project_id) REFERENCES {{.Schema}}.{{.TablePrefix}}projects(org_id, id) ON DELETE CASCADE
);
CREATE INDEX {{.TablePrefix}}membership_projects_org_project_idx
  ON {{.Schema}}.{{.TablePrefix}}membership_projects (org_id, project_id);

CREATE TABLE {{.Schema}}.{{.TablePrefix}}invite_projects (
  org_id      UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  invite_id   UUID NOT NULL,
  project_id  UUID NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (invite_id, project_id),
  FOREIGN KEY (org_id, invite_id) REFERENCES {{.Schema}}.{{.TablePrefix}}invites(org_id, id) ON DELETE CASCADE,
  FOREIGN KEY (org_id, project_id) REFERENCES {{.Schema}}.{{.TablePrefix}}projects(org_id, id) ON DELETE CASCADE
);
CREATE INDEX {{.TablePrefix}}invite_projects_org_project_idx
  ON {{.Schema}}.{{.TablePrefix}}invite_projects (org_id, project_id);

{{if .RLSEnforce}}
ALTER TABLE {{.Schema}}.{{.TablePrefix}}membership_projects ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}membership_projects FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}membership_projects_tenant
  ON {{.Schema}}.{{.TablePrefix}}membership_projects
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());

ALTER TABLE {{.Schema}}.{{.TablePrefix}}invite_projects ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}invite_projects FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}invite_projects_tenant
  ON {{.Schema}}.{{.TablePrefix}}invite_projects
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());
{{end}}

DROP FUNCTION {{.Schema}}.{{.TablePrefix}}resolve_invite_by_token_hash(text);
DROP FUNCTION {{.Schema}}.{{.TablePrefix}}list_pending_invites_for_email(text);

-- SECURITY: both resolve an invite and its project grant before any tenant scope exists — the row is what names the org.
CREATE FUNCTION {{.Schema}}.{{.TablePrefix}}resolve_invite_by_token_hash(p_hash text)
RETURNS TABLE (id uuid, org_id uuid, email text, role text, role_id uuid, all_projects boolean, project_ids text, token_hash text, expires_at timestamptz, accepted_at timestamptz, created_at timestamptz)
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = {{.Schema}}, pg_catalog, pg_temp
AS $fn$
  SELECT i.id, i.org_id, i.email, i.role, i.role_id, i.all_projects,
         COALESCE((SELECT array_agg(g.project_id ORDER BY g.created_at, g.project_id)
                   FROM {{.Schema}}.{{.TablePrefix}}invite_projects g
                   WHERE g.org_id = i.org_id AND g.invite_id = i.id), '{}')::text,
         i.token_hash, i.expires_at, i.accepted_at, i.created_at
  FROM {{.Schema}}.{{.TablePrefix}}invites i
  WHERE i.token_hash = p_hash
  LIMIT 1;
$fn$;

CREATE FUNCTION {{.Schema}}.{{.TablePrefix}}list_pending_invites_for_email(p_email text)
RETURNS TABLE (id uuid, org_id uuid, email text, role text, role_id uuid, all_projects boolean, project_ids text, token_hash text, expires_at timestamptz, accepted_at timestamptz, created_at timestamptz)
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = {{.Schema}}, pg_catalog, pg_temp
AS $fn$
  SELECT i.id, i.org_id, i.email, i.role, i.role_id, i.all_projects,
         COALESCE((SELECT array_agg(g.project_id ORDER BY g.created_at, g.project_id)
                   FROM {{.Schema}}.{{.TablePrefix}}invite_projects g
                   WHERE g.org_id = i.org_id AND g.invite_id = i.id), '{}')::text,
         i.token_hash, i.expires_at, i.accepted_at, i.created_at
  FROM {{.Schema}}.{{.TablePrefix}}invites i
  WHERE i.email = p_email AND i.accepted_at IS NULL
  ORDER BY i.created_at ASC, i.id ASC;
$fn$;

REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}resolve_invite_by_token_hash(text) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}list_pending_invites_for_email(text) FROM PUBLIC;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP FUNCTION {{.Schema}}.{{.TablePrefix}}resolve_invite_by_token_hash(text);
DROP FUNCTION {{.Schema}}.{{.TablePrefix}}list_pending_invites_for_email(text);

CREATE FUNCTION {{.Schema}}.{{.TablePrefix}}resolve_invite_by_token_hash(p_hash text)
RETURNS TABLE (id uuid, org_id uuid, email text, role text, role_id uuid, token_hash text, expires_at timestamptz, accepted_at timestamptz, created_at timestamptz)
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = {{.Schema}}, pg_catalog, pg_temp
AS $fn$
  SELECT i.id, i.org_id, i.email, i.role, i.role_id, i.token_hash, i.expires_at, i.accepted_at, i.created_at
  FROM {{.Schema}}.{{.TablePrefix}}invites i
  WHERE i.token_hash = p_hash
  LIMIT 1;
$fn$;

CREATE FUNCTION {{.Schema}}.{{.TablePrefix}}list_pending_invites_for_email(p_email text)
RETURNS TABLE (id uuid, org_id uuid, email text, role text, role_id uuid, token_hash text, expires_at timestamptz, accepted_at timestamptz, created_at timestamptz)
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = {{.Schema}}, pg_catalog, pg_temp
AS $fn$
  SELECT i.id, i.org_id, i.email, i.role, i.role_id, i.token_hash, i.expires_at, i.accepted_at, i.created_at
  FROM {{.Schema}}.{{.TablePrefix}}invites i
  WHERE i.email = p_email AND i.accepted_at IS NULL
  ORDER BY i.created_at ASC, i.id ASC;
$fn$;

REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}resolve_invite_by_token_hash(text) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}list_pending_invites_for_email(text) FROM PUBLIC;

-- NOTE: dropping the grant makes every restricted member and invite reach every project again.
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}invite_projects;
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}membership_projects;

ALTER TABLE {{.Schema}}.{{.TablePrefix}}invites
  DROP CONSTRAINT {{.TablePrefix}}invites_owner_all_projects,
  DROP CONSTRAINT {{.TablePrefix}}invites_org_id_id_key,
  DROP COLUMN all_projects;

ALTER TABLE {{.Schema}}.{{.TablePrefix}}memberships
  DROP CONSTRAINT {{.TablePrefix}}memberships_owner_all_projects,
  DROP CONSTRAINT {{.TablePrefix}}memberships_org_id_id_key,
  DROP COLUMN all_projects;

-- +goose StatementEnd
