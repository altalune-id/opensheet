-- +goose Up
-- +goose StatementBegin

ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheets
  ADD COLUMN generation      BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN validated_at    TIMESTAMPTZ,
  ADD COLUMN contract_ok     BOOLEAN NOT NULL DEFAULT true,
  ADD COLUMN contract_reason TEXT NOT NULL DEFAULT '';

CREATE TABLE {{.Schema}}.{{.TablePrefix}}sheet_rows (
  sheet_id            UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}sheets(id) ON DELETE CASCADE,
  -- The RESOLVED tab name, never '', mirroring sheet_snapshots: an empty sheets.tab means
  -- "first tab", so keying on sheet_id alone would keep serving a renamed tab's rows.
  tab                 TEXT NOT NULL,
  row_id              TEXT NOT NULL,
  row_index           INTEGER NOT NULL CHECK (row_index >= 0),
  data                JSONB NOT NULL,
  deleted_at          TIMESTAMPTZ,
  org_id              UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}projects(id) ON DELETE CASCADE,
  PRIMARY KEY (sheet_id, tab, row_id)
);

CREATE INDEX {{.TablePrefix}}sheet_rows_org_project_idx
  ON {{.Schema}}.{{.TablePrefix}}sheet_rows (org_id, project_id);

CREATE INDEX {{.TablePrefix}}sheet_rows_page_idx
  ON {{.Schema}}.{{.TablePrefix}}sheet_rows (sheet_id, tab, row_index);

{{if .RLSEnforce}}
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheet_rows ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheet_rows FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}sheet_rows_tenant
  ON {{.Schema}}.{{.TablePrefix}}sheet_rows
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());
{{end}}

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

{{if .RLSEnforce}}
DROP POLICY IF EXISTS {{.TablePrefix}}sheet_rows_tenant ON {{.Schema}}.{{.TablePrefix}}sheet_rows;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheet_rows NO FORCE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheet_rows DISABLE ROW LEVEL SECURITY;
{{end}}

DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}sheet_rows_page_idx;
DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}sheet_rows_org_project_idx;
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}sheet_rows;

ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheets
  DROP COLUMN IF EXISTS contract_reason,
  DROP COLUMN IF EXISTS contract_ok,
  DROP COLUMN IF EXISTS validated_at,
  DROP COLUMN IF EXISTS generation;

-- +goose StatementEnd
