-- +goose Up
-- +goose StatementBegin

-- SECURITY: pg_temp must stay last in search_path — https://www.postgresql.org/docs/17/sql-createfunction.html#SQL-CREATEFUNCTION-SECURITY
CREATE OR REPLACE FUNCTION {{.Schema}}.{{.TablePrefix}}list_org_ids()
RETURNS TABLE (id uuid)
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = {{.Schema}}, pg_catalog, pg_temp
AS $$
  SELECT o.id FROM {{.Schema}}.{{.TablePrefix}}orgs o ORDER BY o.created_at ASC, o.id ASC;
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
  ORDER BY o.created_at ASC, o.id ASC;
$$;

REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}list_org_ids() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}list_orgs_for_user(uuid) FROM PUBLIC;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE OR REPLACE FUNCTION {{.Schema}}.{{.TablePrefix}}list_org_ids()
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

REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}list_org_ids() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}list_orgs_for_user(uuid) FROM PUBLIC;

-- +goose StatementEnd
