-- +goose Up
-- +goose StatementBegin

ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheets
  ADD COLUMN header_columns TEXT NOT NULL DEFAULT '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheets
  DROP COLUMN IF EXISTS header_columns;

-- +goose StatementEnd
