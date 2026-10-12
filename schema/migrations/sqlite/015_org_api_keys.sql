-- +goose Up
-- +goose StatementBegin

-- NOTE: SQLite has no ALTER COLUMN, so making project_id nullable means rebuilding the table; dropping the old one fires todos' ON DELETE SET NULL, so the key authorship is saved first and restored after.
CREATE TEMP TABLE {{.TablePrefix}}todo_key_authors AS
SELECT id, created_by_key_id FROM {{.TablePrefix}}todos WHERE created_by_key_id IS NOT NULL;

CREATE TABLE {{.TablePrefix}}api_keys_new (
  id                  TEXT PRIMARY KEY,
  org_id              TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          TEXT REFERENCES {{.TablePrefix}}projects(id) ON DELETE CASCADE,
  kind                TEXT NOT NULL DEFAULT 'project',
  all_projects        INTEGER NOT NULL DEFAULT 0,
  name                TEXT NOT NULL,
  secret_hash         BLOB NOT NULL,
  secret_hint         TEXT NOT NULL DEFAULT '',
  scopes              TEXT NOT NULL DEFAULT '[]',
  resource_ids        TEXT NOT NULL DEFAULT '[]',
  created_by          TEXT REFERENCES {{.TablePrefix}}users(id) ON DELETE SET NULL,
  created_at          TEXT NOT NULL,
  expires_at          TEXT,
  revoked_at          TEXT,
  last_used_at        TEXT,
  CONSTRAINT {{.TablePrefix}}api_keys_kind_shape CHECK (
    (kind = 'project' AND project_id IS NOT NULL AND all_projects = 0)
    OR (kind IN ('org', 'personal') AND project_id IS NULL)
  )
);

INSERT INTO {{.TablePrefix}}api_keys_new (id, org_id, project_id, kind, all_projects, name, secret_hash, secret_hint, scopes, resource_ids, created_by, created_at, expires_at, revoked_at, last_used_at)
SELECT id, org_id, project_id, 'project', 0, name, secret_hash, '', scopes, resource_ids, NULL, created_at, expires_at, revoked_at, last_used_at
FROM {{.TablePrefix}}api_keys;

DROP TABLE {{.TablePrefix}}api_keys;
ALTER TABLE {{.TablePrefix}}api_keys_new RENAME TO {{.TablePrefix}}api_keys;

UPDATE {{.TablePrefix}}todos
SET created_by_key_id = (SELECT a.created_by_key_id FROM {{.TablePrefix}}todo_key_authors a WHERE a.id = {{.TablePrefix}}todos.id)
WHERE id IN (SELECT id FROM {{.TablePrefix}}todo_key_authors);

DROP TABLE {{.TablePrefix}}todo_key_authors;

CREATE UNIQUE INDEX {{.TablePrefix}}api_keys_secret_hash_key
  ON {{.TablePrefix}}api_keys (secret_hash);

CREATE INDEX {{.TablePrefix}}api_keys_project_idx
  ON {{.TablePrefix}}api_keys (org_id, project_id);

CREATE INDEX {{.TablePrefix}}api_keys_org_kind_idx
  ON {{.TablePrefix}}api_keys (org_id, kind, created_at DESC);

CREATE INDEX {{.TablePrefix}}api_keys_personal_owner_idx
  ON {{.TablePrefix}}api_keys (org_id, created_by, created_at DESC)
  WHERE kind = 'personal';

CREATE UNIQUE INDEX {{.TablePrefix}}api_keys_org_id_id_key
  ON {{.TablePrefix}}api_keys (org_id, id);

CREATE UNIQUE INDEX {{.TablePrefix}}projects_org_id_id_key
  ON {{.TablePrefix}}projects (org_id, id);

-- SECURITY: SQLite has no RLS, so these composite keys are what stop a grant row naming another org's key or project.
CREATE TABLE {{.TablePrefix}}api_key_projects (
  org_id      TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  key_id      TEXT NOT NULL,
  project_id  TEXT NOT NULL,
  created_at  TEXT NOT NULL,
  PRIMARY KEY (key_id, project_id),
  FOREIGN KEY (org_id, key_id) REFERENCES {{.TablePrefix}}api_keys(org_id, id) ON DELETE CASCADE,
  FOREIGN KEY (org_id, project_id) REFERENCES {{.TablePrefix}}projects(org_id, id) ON DELETE CASCADE
);

CREATE INDEX {{.TablePrefix}}api_key_projects_org_project_idx
  ON {{.TablePrefix}}api_key_projects (org_id, project_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS {{.TablePrefix}}api_key_projects;
DROP INDEX IF EXISTS {{.TablePrefix}}projects_org_id_id_key;

CREATE TEMP TABLE {{.TablePrefix}}todo_key_authors AS
SELECT t.id, t.created_by_key_id FROM {{.TablePrefix}}todos t
JOIN {{.TablePrefix}}api_keys k ON k.id = t.created_by_key_id
WHERE k.kind = 'project';

-- NOTE: org keys and personal tokens cannot satisfy the restored NOT NULL project_id and are dropped.
CREATE TABLE {{.TablePrefix}}api_keys_old (
  id                  TEXT PRIMARY KEY,
  org_id              TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          TEXT NOT NULL REFERENCES {{.TablePrefix}}projects(id) ON DELETE CASCADE,
  name                TEXT NOT NULL,
  secret_hash         BLOB NOT NULL,
  scopes              TEXT NOT NULL DEFAULT '[]',
  resource_ids        TEXT NOT NULL DEFAULT '[]',
  created_at          TEXT NOT NULL,
  expires_at          TEXT,
  revoked_at          TEXT,
  last_used_at        TEXT
);

INSERT INTO {{.TablePrefix}}api_keys_old (id, org_id, project_id, name, secret_hash, scopes, resource_ids, created_at, expires_at, revoked_at, last_used_at)
SELECT id, org_id, project_id, name, secret_hash, scopes, resource_ids, created_at, expires_at, revoked_at, last_used_at
FROM {{.TablePrefix}}api_keys WHERE kind = 'project';

DROP TABLE {{.TablePrefix}}api_keys;
ALTER TABLE {{.TablePrefix}}api_keys_old RENAME TO {{.TablePrefix}}api_keys;

UPDATE {{.TablePrefix}}todos
SET created_by_key_id = (SELECT a.created_by_key_id FROM {{.TablePrefix}}todo_key_authors a WHERE a.id = {{.TablePrefix}}todos.id)
WHERE id IN (SELECT id FROM {{.TablePrefix}}todo_key_authors);

DROP TABLE {{.TablePrefix}}todo_key_authors;

CREATE UNIQUE INDEX {{.TablePrefix}}api_keys_secret_hash_key
  ON {{.TablePrefix}}api_keys (secret_hash);

CREATE INDEX {{.TablePrefix}}api_keys_project_idx
  ON {{.TablePrefix}}api_keys (org_id, project_id);

-- +goose StatementEnd
