-- +goose Up
-- +goose StatementBegin

ALTER TABLE {{.TablePrefix}}sheets ADD COLUMN header_columns TEXT NOT NULL DEFAULT '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE {{.TablePrefix}}sheets DROP COLUMN header_columns;

-- +goose StatementEnd
