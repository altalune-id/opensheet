-- +goose Up
-- +goose StatementBegin

{{if .RLSEnforce}}
ALTER TABLE {{.Schema}}.{{.TablePrefix}}credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}credentials FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}credentials_tenant
  ON {{.Schema}}.{{.TablePrefix}}credentials
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());

ALTER TABLE {{.Schema}}.{{.TablePrefix}}spreadsheets ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}spreadsheets FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}spreadsheets_tenant
  ON {{.Schema}}.{{.TablePrefix}}spreadsheets
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());

ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheets ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheets FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}sheets_tenant
  ON {{.Schema}}.{{.TablePrefix}}sheets
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());

ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}api_keys_tenant
  ON {{.Schema}}.{{.TablePrefix}}api_keys
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());

-- A join table still needs its own policy; without one it would be readable across tenants.
ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_key_sheets ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_key_sheets FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}api_key_sheets_tenant
  ON {{.Schema}}.{{.TablePrefix}}api_key_sheets
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());

ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheet_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheet_snapshots FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}sheet_snapshots_tenant
  ON {{.Schema}}.{{.TablePrefix}}sheet_snapshots
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());
{{end}}

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

{{if .RLSEnforce}}
DROP POLICY IF EXISTS {{.TablePrefix}}sheet_snapshots_tenant ON {{.Schema}}.{{.TablePrefix}}sheet_snapshots;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheet_snapshots NO FORCE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheet_snapshots DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS {{.TablePrefix}}api_key_sheets_tenant ON {{.Schema}}.{{.TablePrefix}}api_key_sheets;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_key_sheets NO FORCE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_key_sheets DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS {{.TablePrefix}}api_keys_tenant ON {{.Schema}}.{{.TablePrefix}}api_keys;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_keys NO FORCE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}api_keys DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS {{.TablePrefix}}sheets_tenant ON {{.Schema}}.{{.TablePrefix}}sheets;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheets NO FORCE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheets DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS {{.TablePrefix}}spreadsheets_tenant ON {{.Schema}}.{{.TablePrefix}}spreadsheets;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}spreadsheets NO FORCE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}spreadsheets DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS {{.TablePrefix}}credentials_tenant ON {{.Schema}}.{{.TablePrefix}}credentials;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}credentials NO FORCE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}credentials DISABLE ROW LEVEL SECURITY;
{{end}}

-- +goose StatementEnd
