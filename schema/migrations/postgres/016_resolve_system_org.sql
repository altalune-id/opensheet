-- +goose Up
-- +goose StatementBegin

-- SECURITY: resolves the singleton org on every selfhosted login, before any tenant scope exists.
CREATE OR REPLACE FUNCTION {{.Schema}}.{{.TablePrefix}}resolve_system_org()
RETURNS TABLE (id uuid, slug text, name text, created_by uuid, created_at timestamptz, system boolean)
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = {{.Schema}}, pg_catalog, pg_temp
AS $$
  SELECT o.id, o.slug, o.name, o.created_by, o.created_at, o.system
  FROM {{.Schema}}.{{.TablePrefix}}orgs o
  WHERE o.system
  ORDER BY o.created_at ASC, o.id ASC
  LIMIT 1;
$$;

REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}resolve_system_org() FROM PUBLIC;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP FUNCTION IF EXISTS {{.Schema}}.{{.TablePrefix}}resolve_system_org();

-- +goose StatementEnd
