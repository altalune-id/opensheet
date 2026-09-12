-- +goose Up
-- +goose StatementBegin

ALTER TABLE {{.TablePrefix}}sheets ADD COLUMN generation INTEGER NOT NULL DEFAULT 0;
ALTER TABLE {{.TablePrefix}}sheets ADD COLUMN validated_at TEXT;
ALTER TABLE {{.TablePrefix}}sheets ADD COLUMN contract_ok INTEGER NOT NULL DEFAULT 1;
ALTER TABLE {{.TablePrefix}}sheets ADD COLUMN contract_reason TEXT NOT NULL DEFAULT '';

CREATE TABLE {{.TablePrefix}}sheet_rows (
  sheet_id            TEXT NOT NULL REFERENCES {{.TablePrefix}}sheets(id) ON DELETE CASCADE,
  -- The RESOLVED tab name, never '', mirroring sheet_snapshots: an empty sheets.tab means
  -- "first tab", so keying on sheet_id alone would keep serving a renamed tab's rows.
  tab                 TEXT NOT NULL,
  row_id              TEXT NOT NULL,
  row_index           INTEGER NOT NULL CHECK (row_index >= 0),
  data                TEXT NOT NULL,
  deleted_at          TEXT,
  org_id              TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          TEXT NOT NULL REFERENCES {{.TablePrefix}}projects(id) ON DELETE CASCADE,
  PRIMARY KEY (sheet_id, tab, row_id)
);

CREATE INDEX {{.TablePrefix}}sheet_rows_org_project_idx
  ON {{.TablePrefix}}sheet_rows (org_id, project_id);

CREATE INDEX {{.TablePrefix}}sheet_rows_page_idx
  ON {{.TablePrefix}}sheet_rows (sheet_id, tab, row_index);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS {{.TablePrefix}}sheet_rows_page_idx;
DROP INDEX IF EXISTS {{.TablePrefix}}sheet_rows_org_project_idx;
DROP TABLE IF EXISTS {{.TablePrefix}}sheet_rows;

ALTER TABLE {{.TablePrefix}}sheets DROP COLUMN contract_reason;
ALTER TABLE {{.TablePrefix}}sheets DROP COLUMN contract_ok;
ALTER TABLE {{.TablePrefix}}sheets DROP COLUMN validated_at;
ALTER TABLE {{.TablePrefix}}sheets DROP COLUMN generation;

-- +goose StatementEnd
