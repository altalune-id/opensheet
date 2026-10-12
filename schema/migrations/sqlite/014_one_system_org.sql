-- +goose Up
-- +goose StatementBegin

-- SECURITY: selfhosted logins resolve the singleton by orgs.system, so a second system org would lock its admin out.
-- NOTE: keep the row SystemOrg already resolves (earliest created_at, then id) and demote the rest, so the index can build.
UPDATE {{.TablePrefix}}orgs SET system = 0
WHERE system = 1 AND id <> (
  SELECT o.id FROM {{.TablePrefix}}orgs o WHERE o.system = 1 ORDER BY o.created_at, o.id LIMIT 1
);

UPDATE {{.TablePrefix}}projects SET system = 0
WHERE system = 1 AND id <> (
  SELECT p.id FROM {{.TablePrefix}}projects p
  WHERE p.system = 1 AND p.org_id = {{.TablePrefix}}projects.org_id
  ORDER BY p.created_at, p.id LIMIT 1
);

CREATE UNIQUE INDEX {{.TablePrefix}}orgs_one_system
  ON {{.TablePrefix}}orgs (system)
  WHERE system = 1;

CREATE UNIQUE INDEX {{.TablePrefix}}projects_one_system
  ON {{.TablePrefix}}projects (org_id)
  WHERE system = 1;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS {{.TablePrefix}}projects_one_system;
DROP INDEX IF EXISTS {{.TablePrefix}}orgs_one_system;

-- +goose StatementEnd
