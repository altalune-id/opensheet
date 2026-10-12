-- +goose Up
-- +goose StatementBegin

CREATE TABLE {{.Schema}}.{{.TablePrefix}}outbox_entries (
  id                  UUID PRIMARY KEY,
  event_id            UUID NOT NULL,
  org_id              UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}projects(id) ON DELETE CASCADE,
  target              TEXT NOT NULL,
  payload             BYTEA NOT NULL,
  attempt             INTEGER NOT NULL DEFAULT 0,
  next_attempt_at     TIMESTAMPTZ NOT NULL,
  status              TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','delivered','failed')),
  last_error          TEXT NOT NULL DEFAULT '',
  created_at          TIMESTAMPTZ NOT NULL,
  delivered_at        TIMESTAMPTZ,
  -- NOTE: scoped by org, not global, or one tenant's enqueue could block another's.
  UNIQUE (org_id, event_id, target)
);

CREATE INDEX {{.TablePrefix}}outbox_entries_due_idx
  ON {{.Schema}}.{{.TablePrefix}}outbox_entries (org_id, next_attempt_at)
  WHERE status = 'pending';

{{if .RLSEnforce}}
ALTER TABLE {{.Schema}}.{{.TablePrefix}}outbox_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}outbox_entries FORCE ROW LEVEL SECURITY;
CREATE POLICY {{.TablePrefix}}outbox_entries_tenant
  ON {{.Schema}}.{{.TablePrefix}}outbox_entries
  USING (org_id = {{.Schema}}.{{.TablePrefix}}current_org_id());
{{end}}

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}outbox_entries;

-- +goose StatementEnd
