-- +goose Up
-- +goose StatementBegin

-- SECURITY: spreadsheets:write is already mintable but enforced nowhere, so keys carrying it exist; defaulting to false stops the tab-creation route from granting them write access retroactively when it deploys.
ALTER TABLE {{.Schema}}.{{.TablePrefix}}spreadsheets
  ADD COLUMN writable BOOLEAN NOT NULL DEFAULT false;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE {{.Schema}}.{{.TablePrefix}}spreadsheets DROP COLUMN IF EXISTS writable;

-- +goose StatementEnd
