-- +goose Up
-- +goose StatementBegin

ALTER TABLE {{.TablePrefix}}sheets ADD COLUMN soft_delete INTEGER NOT NULL DEFAULT 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE {{.TablePrefix}}sheets DROP COLUMN soft_delete;

-- +goose StatementEnd
