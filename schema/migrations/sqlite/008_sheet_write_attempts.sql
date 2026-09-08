-- +goose Up
-- +goose StatementBegin

CREATE TABLE {{.TablePrefix}}sheet_write_attempts (
  sheet_id            TEXT NOT NULL REFERENCES {{.TablePrefix}}sheets(id) ON DELETE CASCADE,
  tab                 TEXT NOT NULL,
  -- NOTE: idem_key, not key: `ON CONFLICT (sheet_id, tab, key)` beside `SET key = key` reads as a trap.
  idem_key            TEXT NOT NULL,
  body_hash           TEXT NOT NULL,
  -- NOTE: nullable, because the no-op DO UPDATE returns the freshly inserted row on the claimed path, where no payload exists yet.
  payload             BLOB,
  done                INTEGER NOT NULL,
  claim_token         TEXT NOT NULL,
  created_at          TEXT NOT NULL,
  expires_at          TEXT NOT NULL,
  org_id              TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          TEXT NOT NULL REFERENCES {{.TablePrefix}}projects(id) ON DELETE CASCADE,
  PRIMARY KEY (sheet_id, tab, idem_key)
);

CREATE INDEX {{.TablePrefix}}sheet_write_attempts_org_project_idx
  ON {{.TablePrefix}}sheet_write_attempts (org_id, project_id);

CREATE INDEX {{.TablePrefix}}sheet_write_attempts_expires_idx
  ON {{.TablePrefix}}sheet_write_attempts (expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS {{.TablePrefix}}sheet_write_attempts_expires_idx;
DROP INDEX IF EXISTS {{.TablePrefix}}sheet_write_attempts_org_project_idx;
DROP TABLE IF EXISTS {{.TablePrefix}}sheet_write_attempts;

-- +goose StatementEnd
