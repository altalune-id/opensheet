-- +goose Up
-- +goose StatementBegin

CREATE TABLE {{.Schema}}.{{.TablePrefix}}webhook_endpoints (
  id                  UUID PRIMARY KEY,
  org_id              UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}projects(id) ON DELETE CASCADE,
  url                 TEXT NOT NULL,
  description         TEXT NOT NULL DEFAULT '',
  event_types         TEXT NOT NULL,
  secret_primary      BYTEA NOT NULL,
  secret_secondary    BYTEA,
  active              BOOLEAN NOT NULL,
  created_at          TIMESTAMPTZ NOT NULL,
  updated_at          TIMESTAMPTZ NOT NULL,
  CHECK (secret_secondary IS NULL OR length(secret_secondary) > 0)
);

CREATE INDEX {{.TablePrefix}}webhook_endpoints_org_project_idx
  ON {{.Schema}}.{{.TablePrefix}}webhook_endpoints (org_id, project_id);

CREATE TABLE {{.Schema}}.{{.TablePrefix}}webhook_deliveries (
  id                  UUID PRIMARY KEY,
  org_id              UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}projects(id) ON DELETE CASCADE,
  endpoint_id         UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}webhook_endpoints(id) ON DELETE CASCADE,
  delivery_id         UUID NOT NULL,
  event_id            UUID NOT NULL,
  event_type          TEXT NOT NULL,
  attempt             INTEGER NOT NULL,
  status_code         INTEGER NOT NULL DEFAULT 0,
  error               TEXT NOT NULL DEFAULT '',
  response_body       TEXT NOT NULL DEFAULT '',
  response_truncated  BOOLEAN NOT NULL DEFAULT false,
  response_headers    TEXT NOT NULL DEFAULT '[]',
  duration_ms         INTEGER NOT NULL,
  created_at          TIMESTAMPTZ NOT NULL
);

CREATE INDEX {{.TablePrefix}}webhook_deliveries_org_delivery_idx
  ON {{.Schema}}.{{.TablePrefix}}webhook_deliveries (org_id, delivery_id);

CREATE INDEX {{.TablePrefix}}outbox_entries_org_target_created_idx
  ON {{.Schema}}.{{.TablePrefix}}outbox_entries (org_id, target, created_at DESC);

{{if .RLSEnforce}}
ALTER TABLE {{.Schema}}.{{.TablePrefix}}webhook_endpoints ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}webhook_endpoints FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}webhook_endpoints_tenant
  ON {{.Schema}}.{{.TablePrefix}}webhook_endpoints
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());

ALTER TABLE {{.Schema}}.{{.TablePrefix}}webhook_deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}webhook_deliveries FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}webhook_deliveries_tenant
  ON {{.Schema}}.{{.TablePrefix}}webhook_deliveries
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());
{{end}}

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}outbox_entries_org_target_created_idx;
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}webhook_deliveries;
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}webhook_endpoints;

-- +goose StatementEnd
