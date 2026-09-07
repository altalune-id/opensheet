-- +goose Up
-- +goose StatementBegin

CREATE INDEX {{.TablePrefix}}todos_org_stale_idx
  ON {{.TablePrefix}}todos (org_id, created_at)
  WHERE done = 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS {{.TablePrefix}}todos_org_stale_idx;

-- +goose StatementEnd
