-- +goose Up
-- +goose StatementBegin

-- NOTE: opensheet's osk_ keys hashed only the secret half and resolved by key_prefix; the template's api_keys that follow hash the whole plaintext, so legacy keys cannot be converted and must be re-minted.
DROP TABLE IF EXISTS {{.TablePrefix}}api_key_sheets;
DROP TABLE IF EXISTS {{.TablePrefix}}api_keys;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE TABLE {{.TablePrefix}}api_keys (
  id                  TEXT PRIMARY KEY,
  org_id              TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          TEXT NOT NULL REFERENCES {{.TablePrefix}}projects(id) ON DELETE CASCADE,
  name                TEXT NOT NULL,
  key_prefix          TEXT NOT NULL UNIQUE,
  secret_hash         BLOB NOT NULL,
  scopes              TEXT NOT NULL DEFAULT '[]',
  expires_at          TEXT,
  last_used_at        TEXT,
  revoked_at          TEXT,
  created_at          TEXT NOT NULL
);

CREATE INDEX {{.TablePrefix}}api_keys_org_project_idx
  ON {{.TablePrefix}}api_keys (org_id, project_id);

CREATE TABLE {{.TablePrefix}}api_key_sheets (
  api_key_id          TEXT NOT NULL REFERENCES {{.TablePrefix}}api_keys(id) ON DELETE CASCADE,
  sheet_id            TEXT NOT NULL REFERENCES {{.TablePrefix}}sheets(id) ON DELETE CASCADE,
  org_id              TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  PRIMARY KEY (api_key_id, sheet_id)
);

CREATE INDEX {{.TablePrefix}}api_key_sheets_sheet_idx
  ON {{.TablePrefix}}api_key_sheets (sheet_id);

CREATE INDEX {{.TablePrefix}}api_key_sheets_org_idx
  ON {{.TablePrefix}}api_key_sheets (org_id);

-- +goose StatementEnd
