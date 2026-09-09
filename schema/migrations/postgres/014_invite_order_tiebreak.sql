-- +goose Up
-- +goose StatementBegin

-- SECURITY: pg_temp must stay last in search_path — https://www.postgresql.org/docs/17/sql-createfunction.html#SQL-CREATEFUNCTION-SECURITY
CREATE OR REPLACE FUNCTION {{.Schema}}.{{.TablePrefix}}list_pending_invites_for_email(p_email text)
RETURNS TABLE (id uuid, org_id uuid, email text, role text, token_hash text, expires_at timestamptz, accepted_at timestamptz, created_at timestamptz)
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = {{.Schema}}, pg_catalog, pg_temp
AS $$
  SELECT i.id, i.org_id, i.email, i.role, i.token_hash, i.expires_at, i.accepted_at, i.created_at
  FROM {{.Schema}}.{{.TablePrefix}}invites i
  WHERE i.email = p_email AND i.accepted_at IS NULL
  ORDER BY i.created_at ASC, i.id ASC;
$$;

REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}list_pending_invites_for_email(text) FROM PUBLIC;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

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

REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}list_pending_invites_for_email(text) FROM PUBLIC;

-- +goose StatementEnd
