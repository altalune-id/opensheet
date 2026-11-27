-- +goose Up
-- +goose StatementBegin

-- SECURITY: an org or project key acts for its creator, so deleting the creator revokes it; the row stays, so rows it authored keep their reference.
CREATE TRIGGER {{.TablePrefix}}users_revoke_keys
BEFORE DELETE ON {{.TablePrefix}}users
FOR EACH ROW
BEGIN
  UPDATE {{.TablePrefix}}api_keys
     SET revoked_at = strftime('%Y-%m-%dT%H:%M:%S', 'now') || '.000000000Z'
   WHERE created_by = OLD.id
     AND kind IN ('org', 'project')
     AND revoked_at IS NULL;
END;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS {{.TablePrefix}}users_revoke_keys;
-- +goose StatementEnd
