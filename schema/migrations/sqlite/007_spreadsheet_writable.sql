-- +goose Up
-- +goose StatementBegin

-- SECURITY: spreadsheets:write is already mintable but enforced nowhere, so keys carrying it exist; defaulting to 0 stops the tab-creation route from granting them write access retroactively when it deploys.
ALTER TABLE {{.TablePrefix}}spreadsheets ADD COLUMN writable INTEGER NOT NULL DEFAULT 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE {{.TablePrefix}}spreadsheets DROP COLUMN writable;

-- +goose StatementEnd
