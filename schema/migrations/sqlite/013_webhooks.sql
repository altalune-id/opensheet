-- +goose Up
-- +goose StatementBegin

CREATE TABLE {{.TablePrefix}}webhook_endpoints (
  id                  TEXT PRIMARY KEY,
  org_id              TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          TEXT NOT NULL REFERENCES {{.TablePrefix}}projects(id) ON DELETE CASCADE,
  url                 TEXT NOT NULL,
  description         TEXT NOT NULL DEFAULT '',
  event_types         TEXT NOT NULL,
  secret_primary      BLOB NOT NULL,
  secret_secondary    BLOB,
  active              INTEGER NOT NULL,
  created_at          TEXT NOT NULL,
  updated_at          TEXT NOT NULL,
  CHECK (secret_secondary IS NULL OR length(secret_secondary) > 0)
);

CREATE INDEX {{.TablePrefix}}webhook_endpoints_org_project_idx
  ON {{.TablePrefix}}webhook_endpoints (org_id, project_id);

CREATE TABLE {{.TablePrefix}}webhook_deliveries (
  id                  TEXT PRIMARY KEY,
  org_id              TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          TEXT NOT NULL REFERENCES {{.TablePrefix}}projects(id) ON DELETE CASCADE,
  endpoint_id         TEXT NOT NULL REFERENCES {{.TablePrefix}}webhook_endpoints(id) ON DELETE CASCADE,
  delivery_id         TEXT NOT NULL,
  event_id            TEXT NOT NULL,
  event_type          TEXT NOT NULL,
  attempt             INTEGER NOT NULL,
  status_code         INTEGER NOT NULL DEFAULT 0,
  error               TEXT NOT NULL DEFAULT '',
  response_body       TEXT NOT NULL DEFAULT '',
  response_truncated  INTEGER NOT NULL DEFAULT 0,
  response_headers    TEXT NOT NULL DEFAULT '[]',
  duration_ms         INTEGER NOT NULL,
  created_at          TEXT NOT NULL
);

CREATE INDEX {{.TablePrefix}}webhook_deliveries_org_delivery_idx
  ON {{.TablePrefix}}webhook_deliveries (org_id, delivery_id);

CREATE INDEX {{.TablePrefix}}outbox_entries_org_target_created_idx
  ON {{.TablePrefix}}outbox_entries (org_id, target, created_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS {{.TablePrefix}}outbox_entries_org_target_created_idx;
DROP TABLE IF EXISTS {{.TablePrefix}}webhook_deliveries;
DROP TABLE IF EXISTS {{.TablePrefix}}webhook_endpoints;

-- +goose StatementEnd
