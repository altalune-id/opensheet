-- +goose Up
-- +goose StatementBegin

ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheets
  ADD COLUMN soft_delete BOOLEAN NOT NULL DEFAULT false;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheets
  DROP COLUMN IF EXISTS soft_delete;

-- +goose StatementEnd
