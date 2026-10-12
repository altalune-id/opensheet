-- +goose Up
-- +goose StatementBegin

-- NOTE: SQLite has no ALTER COLUMN, so making user_id nullable means rebuilding the table.
CREATE TABLE {{.TablePrefix}}todos_new (
  id                  TEXT PRIMARY KEY,
  org_id              TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          TEXT NOT NULL REFERENCES {{.TablePrefix}}projects(id) ON DELETE CASCADE,
  user_id             TEXT REFERENCES {{.TablePrefix}}users(id),
  created_by_key_id   TEXT REFERENCES {{.TablePrefix}}api_keys(id) ON DELETE SET NULL,
  title               TEXT NOT NULL,
  done                INTEGER NOT NULL DEFAULT 0,
  created_at          TEXT NOT NULL,
  updated_at          TEXT NOT NULL,
  CONSTRAINT {{.TablePrefix}}todos_author_not_both CHECK (user_id IS NULL OR created_by_key_id IS NULL)
);

INSERT INTO {{.TablePrefix}}todos_new (id, org_id, project_id, user_id, created_by_key_id, title, done, created_at, updated_at)
SELECT id, org_id, project_id, user_id, NULL, title, done, created_at, updated_at
FROM {{.TablePrefix}}todos;

DROP TABLE {{.TablePrefix}}todos;
ALTER TABLE {{.TablePrefix}}todos_new RENAME TO {{.TablePrefix}}todos;

CREATE INDEX {{.TablePrefix}}todos_org_project_created_idx
  ON {{.TablePrefix}}todos (org_id, project_id, created_at DESC);

CREATE INDEX {{.TablePrefix}}todos_org_stale_idx
  ON {{.TablePrefix}}todos (org_id, created_at)
  WHERE done = 0;

CREATE INDEX {{.TablePrefix}}todos_created_by_key_idx
  ON {{.TablePrefix}}todos (created_by_key_id)
  WHERE created_by_key_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- NOTE: key-authored rows (user_id NULL) cannot satisfy the restored NOT NULL and are dropped.
CREATE TABLE {{.TablePrefix}}todos_old (
  id                  TEXT PRIMARY KEY,
  org_id              TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          TEXT NOT NULL REFERENCES {{.TablePrefix}}projects(id) ON DELETE CASCADE,
  user_id             TEXT NOT NULL REFERENCES {{.TablePrefix}}users(id),
  title               TEXT NOT NULL,
  done                INTEGER NOT NULL DEFAULT 0,
  created_at          TEXT NOT NULL,
  updated_at          TEXT NOT NULL
);

INSERT INTO {{.TablePrefix}}todos_old (id, org_id, project_id, user_id, title, done, created_at, updated_at)
SELECT id, org_id, project_id, user_id, title, done, created_at, updated_at
FROM {{.TablePrefix}}todos
WHERE user_id IS NOT NULL;

DROP TABLE {{.TablePrefix}}todos;
ALTER TABLE {{.TablePrefix}}todos_old RENAME TO {{.TablePrefix}}todos;

CREATE INDEX {{.TablePrefix}}todos_org_project_created_idx
  ON {{.TablePrefix}}todos (org_id, project_id, created_at DESC);

CREATE INDEX {{.TablePrefix}}todos_org_stale_idx
  ON {{.TablePrefix}}todos (org_id, created_at)
  WHERE done = 0;

-- +goose StatementEnd
