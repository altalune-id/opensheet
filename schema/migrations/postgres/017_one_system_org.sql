-- +goose Up
-- +goose StatementBegin

-- SECURITY: selfhosted logins resolve the singleton by orgs.system, so a second system org would lock its admin out.
-- NOTE: keep the row SystemOrg already resolves (earliest created_at, then id) and demote the rest, so the index can build.
UPDATE {{.Schema}}.{{.TablePrefix}}orgs SET system = false
WHERE system AND id <> (
  SELECT o.id FROM {{.Schema}}.{{.TablePrefix}}orgs o WHERE o.system ORDER BY o.created_at, o.id LIMIT 1
);

UPDATE {{.Schema}}.{{.TablePrefix}}projects SET system = false
WHERE system AND id <> (
  SELECT p.id FROM {{.Schema}}.{{.TablePrefix}}projects p
  WHERE p.system AND p.org_id = {{.TablePrefix}}projects.org_id
  ORDER BY p.created_at, p.id LIMIT 1
);

CREATE UNIQUE INDEX {{.TablePrefix}}orgs_one_system
  ON {{.Schema}}.{{.TablePrefix}}orgs (system)
  WHERE system;

CREATE UNIQUE INDEX {{.TablePrefix}}projects_one_system
  ON {{.Schema}}.{{.TablePrefix}}projects (org_id)
  WHERE system;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}projects_one_system;
DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}orgs_one_system;

-- +goose StatementEnd
