-- +goose Up
-- +goose StatementBegin

-- SECURITY: defaults to false, so no sheet published before this migration becomes writable by deploying it.
ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheets
  ADD COLUMN writable BOOLEAN NOT NULL DEFAULT false;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE {{.Schema}}.{{.TablePrefix}}sheets DROP COLUMN IF EXISTS writable;

-- +goose StatementEnd
