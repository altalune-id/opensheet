-- +goose Up
-- +goose StatementBegin

ALTER TABLE {{.TablePrefix}}sheets ADD COLUMN content_digest TEXT NOT NULL DEFAULT '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE {{.TablePrefix}}sheets DROP COLUMN content_digest;

-- +goose StatementEnd
