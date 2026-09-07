-- +goose Up
-- +goose StatementBegin

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

-- +goose StatementEnd
