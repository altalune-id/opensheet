-- +goose Up
-- +goose StatementBegin

CREATE TABLE {{.TablePrefix}}outbox_entries (
  id                  TEXT PRIMARY KEY,
  event_id            TEXT NOT NULL,
  org_id              TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  project_id          TEXT NOT NULL REFERENCES {{.TablePrefix}}projects(id) ON DELETE CASCADE,
  target              TEXT NOT NULL,
  payload             BLOB NOT NULL,
  attempt             INTEGER NOT NULL DEFAULT 0,
  next_attempt_at     TEXT NOT NULL,
  status              TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','delivered','failed')),
  last_error          TEXT NOT NULL DEFAULT '',
  created_at          TEXT NOT NULL,
  delivered_at        TEXT,
  UNIQUE (org_id, event_id, target)
);

CREATE INDEX {{.TablePrefix}}outbox_entries_due_idx
  ON {{.TablePrefix}}outbox_entries (org_id, status, next_attempt_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS {{.TablePrefix}}outbox_entries;

-- +goose StatementEnd
