-- +goose Up
-- +goose StatementBegin

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_roles
    WHERE rolname = current_user AND (rolsuper OR rolbypassrls)
  ) THEN
    RAISE EXCEPTION
      'migration role % lacks BYPASSRLS — SECURITY DEFINER wrappers would silently return zero rows under FORCE row level security',
      current_user
      USING HINT = 'grant it via scripts/db/provision.sh, or: ALTER ROLE ' || quote_ident(current_user) || ' BYPASSRLS';
  END IF;
END $$;

-- SECURITY: pg_temp must stay last in search_path — https://www.postgresql.org/docs/17/sql-createfunction.html#SQL-CREATEFUNCTION-SECURITY
CREATE OR REPLACE FUNCTION {{.Schema}}.{{.TablePrefix}}list_org_ids()
RETURNS TABLE (id uuid)
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = {{.Schema}}, pg_catalog, pg_temp
AS $$
  SELECT o.id FROM {{.Schema}}.{{.TablePrefix}}orgs o ORDER BY o.created_at;
$$;

CREATE OR REPLACE FUNCTION {{.Schema}}.{{.TablePrefix}}resolve_org_by_slug(p_slug text)
RETURNS TABLE (id uuid, slug text, name text, created_by uuid, created_at timestamptz, system boolean)
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = {{.Schema}}, pg_catalog, pg_temp
AS $$
  SELECT o.id, o.slug, o.name, o.created_by, o.created_at, o.system
  FROM {{.Schema}}.{{.TablePrefix}}orgs o
  WHERE o.slug = p_slug
  LIMIT 1;
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

REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}list_org_ids() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}resolve_org_by_slug(text) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}list_orgs_for_user(uuid) FROM PUBLIC;

-- SECURITY: both resolve an invite before any tenant scope exists — the row is what names the org.
CREATE OR REPLACE FUNCTION {{.Schema}}.{{.TablePrefix}}resolve_invite_by_token_hash(p_hash text)
RETURNS TABLE (id uuid, org_id uuid, email text, role text, token_hash text, expires_at timestamptz, accepted_at timestamptz, created_at timestamptz)
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = {{.Schema}}, pg_catalog, pg_temp
AS $$
  SELECT i.id, i.org_id, i.email, i.role, i.token_hash, i.expires_at, i.accepted_at, i.created_at
  FROM {{.Schema}}.{{.TablePrefix}}invites i
  WHERE i.token_hash = p_hash
  LIMIT 1;
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

REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}resolve_invite_by_token_hash(text) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}list_pending_invites_for_email(text) FROM PUBLIC;


-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP FUNCTION IF EXISTS {{.Schema}}.{{.TablePrefix}}list_pending_invites_for_email(text);
DROP FUNCTION IF EXISTS {{.Schema}}.{{.TablePrefix}}resolve_invite_by_token_hash(text);
DROP FUNCTION IF EXISTS {{.Schema}}.{{.TablePrefix}}list_orgs_for_user(uuid);
DROP FUNCTION IF EXISTS {{.Schema}}.{{.TablePrefix}}resolve_org_by_slug(text);
DROP FUNCTION IF EXISTS {{.Schema}}.{{.TablePrefix}}list_org_ids();

-- +goose StatementEnd
