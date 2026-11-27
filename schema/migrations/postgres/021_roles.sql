-- +goose Up
-- +goose StatementBegin

CREATE TABLE {{.Schema}}.{{.TablePrefix}}roles (
  id           UUID PRIMARY KEY,
  org_id       UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  description  TEXT NOT NULL DEFAULT '',
  permissions  TEXT[] NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL,
  updated_at   TIMESTAMPTZ NOT NULL,
  version      BIGINT NOT NULL DEFAULT 1,
  UNIQUE (org_id, id)
);
CREATE UNIQUE INDEX {{.TablePrefix}}roles_org_name_idx
  ON {{.Schema}}.{{.TablePrefix}}roles (org_id, lower(name));

-- NOTE: NO ACTION, not RESTRICT, so an org delete can cascade through roles and their holders in one statement.
ALTER TABLE {{.Schema}}.{{.TablePrefix}}memberships
  DROP CONSTRAINT {{.TablePrefix}}memberships_role_check,
  ADD COLUMN role_id UUID,
  ADD CONSTRAINT {{.TablePrefix}}memberships_role_check CHECK (role IN ('owner','admin','member','custom')),
  ADD CONSTRAINT {{.TablePrefix}}memberships_role_custom CHECK ((role = 'custom') = (role_id IS NOT NULL)),
  ADD CONSTRAINT {{.TablePrefix}}memberships_role_fk FOREIGN KEY (org_id, role_id)
    REFERENCES {{.Schema}}.{{.TablePrefix}}roles (org_id, id);

ALTER TABLE {{.Schema}}.{{.TablePrefix}}invites
  DROP CONSTRAINT {{.TablePrefix}}invites_role_check,
  ADD COLUMN role_id UUID,
  ADD CONSTRAINT {{.TablePrefix}}invites_role_check CHECK (role IN ('owner','admin','member','custom')),
  ADD CONSTRAINT {{.TablePrefix}}invites_role_custom CHECK ((role = 'custom') = (role_id IS NOT NULL)),
  ADD CONSTRAINT {{.TablePrefix}}invites_role_fk FOREIGN KEY (org_id, role_id)
    REFERENCES {{.Schema}}.{{.TablePrefix}}roles (org_id, id);

CREATE INDEX {{.TablePrefix}}memberships_role_idx
  ON {{.Schema}}.{{.TablePrefix}}memberships (org_id, role_id) WHERE role_id IS NOT NULL;
CREATE INDEX {{.TablePrefix}}invites_role_idx
  ON {{.Schema}}.{{.TablePrefix}}invites (org_id, role_id) WHERE role_id IS NOT NULL;

DROP FUNCTION {{.Schema}}.{{.TablePrefix}}resolve_invite_by_token_hash(text);
DROP FUNCTION {{.Schema}}.{{.TablePrefix}}list_pending_invites_for_email(text);

-- SECURITY: both resolve an invite before any tenant scope exists — the row is what names the org.
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

{{if .RLSEnforce}}
ALTER TABLE {{.Schema}}.{{.TablePrefix}}roles ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}roles FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}roles_tenant
  ON {{.Schema}}.{{.TablePrefix}}roles
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());
{{end}}

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP FUNCTION {{.Schema}}.{{.TablePrefix}}resolve_invite_by_token_hash(text);
DROP FUNCTION {{.Schema}}.{{.TablePrefix}}list_pending_invites_for_email(text);

CREATE FUNCTION {{.Schema}}.{{.TablePrefix}}resolve_invite_by_token_hash(p_hash text)
RETURNS TABLE (id uuid, org_id uuid, email text, role text, token_hash text, expires_at timestamptz, accepted_at timestamptz, created_at timestamptz)
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = {{.Schema}}, pg_catalog, pg_temp
AS $fn$
  SELECT i.id, i.org_id, i.email, i.role, i.token_hash, i.expires_at, i.accepted_at, i.created_at
  FROM {{.Schema}}.{{.TablePrefix}}invites i
  WHERE i.token_hash = p_hash
  LIMIT 1;
$fn$;

CREATE FUNCTION {{.Schema}}.{{.TablePrefix}}list_pending_invites_for_email(p_email text)
RETURNS TABLE (id uuid, org_id uuid, email text, role text, token_hash text, expires_at timestamptz, accepted_at timestamptz, created_at timestamptz)
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = {{.Schema}}, pg_catalog, pg_temp
AS $fn$
  SELECT i.id, i.org_id, i.email, i.role, i.token_hash, i.expires_at, i.accepted_at, i.created_at
  FROM {{.Schema}}.{{.TablePrefix}}invites i
  WHERE i.email = p_email AND i.accepted_at IS NULL
  ORDER BY i.created_at ASC, i.id ASC;
$fn$;

REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}resolve_invite_by_token_hash(text) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}list_pending_invites_for_email(text) FROM PUBLIC;

-- NOTE: custom-role members and invites cannot satisfy the restored check and are dropped.
DELETE FROM {{.Schema}}.{{.TablePrefix}}memberships WHERE role = 'custom';
DELETE FROM {{.Schema}}.{{.TablePrefix}}invites WHERE role = 'custom';

DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}invites_role_idx;
DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}memberships_role_idx;

ALTER TABLE {{.Schema}}.{{.TablePrefix}}invites
  DROP CONSTRAINT {{.TablePrefix}}invites_role_fk,
  DROP CONSTRAINT {{.TablePrefix}}invites_role_custom,
  DROP CONSTRAINT {{.TablePrefix}}invites_role_check,
  DROP COLUMN role_id,
  ADD CONSTRAINT {{.TablePrefix}}invites_role_check CHECK (role IN ('owner','admin','member'));

ALTER TABLE {{.Schema}}.{{.TablePrefix}}memberships
  DROP CONSTRAINT {{.TablePrefix}}memberships_role_fk,
  DROP CONSTRAINT {{.TablePrefix}}memberships_role_custom,
  DROP CONSTRAINT {{.TablePrefix}}memberships_role_check,
  DROP COLUMN role_id,
  ADD CONSTRAINT {{.TablePrefix}}memberships_role_check CHECK (role IN ('owner','admin','member'));

DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}roles;

-- +goose StatementEnd
