-- +goose Up
-- +goose StatementBegin

CREATE TABLE {{.TablePrefix}}api_keys (
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

CREATE UNIQUE INDEX {{.TablePrefix}}api_keys_secret_hash_key
  ON {{.TablePrefix}}api_keys (secret_hash);

CREATE INDEX {{.TablePrefix}}api_keys_project_idx
  ON {{.TablePrefix}}api_keys (org_id, project_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS {{.TablePrefix}}api_keys;

-- +goose StatementEnd
