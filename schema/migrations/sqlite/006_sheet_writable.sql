-- +goose Up
-- +goose StatementBegin

-- SECURITY: defaults to 0, so no sheet published before this migration becomes writable by deploying it.
ALTER TABLE {{.TablePrefix}}sheets ADD COLUMN writable INTEGER NOT NULL DEFAULT 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE {{.TablePrefix}}sheets DROP COLUMN writable;

-- +goose StatementEnd
