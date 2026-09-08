-- +goose Up
-- +goose StatementBegin

CREATE TABLE {{.Schema}}.{{.TablePrefix}}sheet_write_attempts (
  sheet_id            UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}sheets(id) ON DELETE CASCADE,
  tab                 TEXT NOT NULL,
  -- NOTE: idem_key, not key: `ON CONFLICT (sheet_id, tab, key)` beside `SET key = key` reads as a trap.
  idem_key            TEXT NOT NULL,
  body_hash           TEXT NOT NULL,
  -- NOTE: nullable, because the no-op DO UPDATE returns the freshly inserted row on the claimed path, where no payload exists yet.
  payload             BYTEA,
  done                BOOLEAN NOT NULL,
  -- NOTE: read back through RETURNING to tell an insert from a conflict; created_at cannot serve, because timestamptz truncates sub-microsecond nanoseconds.
  claim_token         UUID NOT NULL,
  created_at          TIMESTAMPTZ NOT NULL,
  expires_at          TIMESTAMPTZ NOT NULL,
  org_id              UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}projects(id) ON DELETE CASCADE,
  PRIMARY KEY (sheet_id, tab, idem_key)
);

CREATE INDEX {{.TablePrefix}}sheet_write_attempts_org_project_idx
  ON {{.Schema}}.{{.TablePrefix}}sheet_write_attempts (org_id, project_id);

CREATE INDEX {{.TablePrefix}}sheet_write_attempts_expires_idx
  ON {{.Schema}}.{{.TablePrefix}}sheet_write_attempts (expires_at);

{{if .RLSEnforce}}
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheet_write_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheet_write_attempts FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}sheet_write_attempts_tenant
  ON {{.Schema}}.{{.TablePrefix}}sheet_write_attempts
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());
{{end}}

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

{{if .RLSEnforce}}
DROP POLICY IF EXISTS {{.TablePrefix}}sheet_write_attempts_tenant ON {{.Schema}}.{{.TablePrefix}}sheet_write_attempts;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheet_write_attempts NO FORCE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheet_write_attempts DISABLE ROW LEVEL SECURITY;
{{end}}

DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}sheet_write_attempts_expires_idx;
DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}sheet_write_attempts_org_project_idx;
DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}sheet_write_attempts;

-- +goose StatementEnd
