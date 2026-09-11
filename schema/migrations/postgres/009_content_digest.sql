-- +goose Up
-- +goose StatementBegin

ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheets
  ADD COLUMN content_digest TEXT NOT NULL DEFAULT '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheets
  DROP COLUMN IF EXISTS content_digest;

-- +goose StatementEnd
