-- +goose Up
-- +goose StatementBegin

{{if .RLSEnforce}}
-- SECURITY: a transaction-local set_config resets to '' rather than NULL when the transaction ends,
-- so a pooled connection that once served a tenant would make ''::uuid throw 22P02 on every later
-- cross-tenant read. NULLIF restores the intended "no scope means no tenant rows" behaviour.
CREATE OR REPLACE FUNCTION {{.Schema}}.{{.TablePrefix}}current_org_id()
RETURNS uuid
LANGUAGE sql STABLE
SET search_path = {{.Schema}}, pg_catalog, pg_temp
AS $$
  SELECT NULLIF(current_setting('app.current_org_id', true), '')::uuid;
$$;

ALTER TABLE {{.Schema}}.{{.TablePrefix}}orgs ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}orgs FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}orgs_tenant
  ON {{.Schema}}.{{.TablePrefix}}orgs
  USING (id = {{.Schema}}.{{.TablePrefix}}current_org_id());

ALTER TABLE {{.Schema}}.{{.TablePrefix}}memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}memberships FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}memberships_tenant
  ON {{.Schema}}.{{.TablePrefix}}memberships
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());

ALTER TABLE {{.Schema}}.{{.TablePrefix}}projects ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}projects FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}projects_tenant
  ON {{.Schema}}.{{.TablePrefix}}projects
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());

ALTER TABLE {{.Schema}}.{{.TablePrefix}}invites ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}invites FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}invites_tenant
  ON {{.Schema}}.{{.TablePrefix}}invites
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());

ALTER TABLE {{.Schema}}.{{.TablePrefix}}todos ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}todos FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}todos_tenant
  ON {{.Schema}}.{{.TablePrefix}}todos
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());
{{end}}

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

{{if .RLSEnforce}}
DROP POLICY IF EXISTS {{.TablePrefix}}todos_tenant ON {{.Schema}}.{{.TablePrefix}}todos;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}todos NO FORCE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}todos DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS {{.TablePrefix}}invites_tenant ON {{.Schema}}.{{.TablePrefix}}invites;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}invites NO FORCE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}invites DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS {{.TablePrefix}}projects_tenant ON {{.Schema}}.{{.TablePrefix}}projects;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}projects NO FORCE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}projects DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS {{.TablePrefix}}memberships_tenant ON {{.Schema}}.{{.TablePrefix}}memberships;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}memberships NO FORCE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}memberships DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS {{.TablePrefix}}orgs_tenant ON {{.Schema}}.{{.TablePrefix}}orgs;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}orgs NO FORCE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}orgs DISABLE ROW LEVEL SECURITY;

DROP FUNCTION IF EXISTS {{.Schema}}.{{.TablePrefix}}current_org_id();
{{end}}

-- +goose StatementEnd
