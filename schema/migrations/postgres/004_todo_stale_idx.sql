-- +goose Up
-- +goose StatementBegin

CREATE INDEX {{.TablePrefix}}todos_org_stale_idx
  ON {{.Schema}}.{{.TablePrefix}}todos (org_id, created_at)
  WHERE done = false;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}todos_org_stale_idx;

-- +goose StatementEnd
