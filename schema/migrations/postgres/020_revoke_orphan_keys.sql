-- +goose Up
-- +goose StatementBegin

-- SECURITY: an org or project key acts for its creator, so deleting the creator revokes it across every org; the row stays, so rows it authored keep their reference.
CREATE OR REPLACE FUNCTION {{.Schema}}.{{.TablePrefix}}revoke_keys_of_deleted_user()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = {{.Schema}}, pg_catalog, pg_temp
AS $fn$
BEGIN
  UPDATE {{.Schema}}.{{.TablePrefix}}api_keys
     SET revoked_at = now()
   WHERE created_by = OLD.id
     AND kind IN ('org', 'project')
     AND revoked_at IS NULL;
  RETURN OLD;
END;
$fn$;

REVOKE EXECUTE ON FUNCTION {{.Schema}}.{{.TablePrefix}}revoke_keys_of_deleted_user() FROM PUBLIC;

CREATE TRIGGER {{.TablePrefix}}users_revoke_keys
  BEFORE DELETE ON {{.Schema}}.{{.TablePrefix}}users
  FOR EACH ROW EXECUTE FUNCTION {{.Schema}}.{{.TablePrefix}}revoke_keys_of_deleted_user();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS {{.TablePrefix}}users_revoke_keys ON {{.Schema}}.{{.TablePrefix}}users;
DROP FUNCTION IF EXISTS {{.Schema}}.{{.TablePrefix}}revoke_keys_of_deleted_user();
-- +goose StatementEnd
