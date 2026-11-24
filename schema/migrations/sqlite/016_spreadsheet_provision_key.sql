-- +goose Up
-- +goose StatementBegin

ALTER TABLE {{.TablePrefix}}spreadsheets ADD COLUMN provision_key TEXT;

CREATE UNIQUE INDEX {{.TablePrefix}}spreadsheets_provision_key_idx
  ON {{.TablePrefix}}spreadsheets (project_id, provision_key)
  WHERE provision_key IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS {{.TablePrefix}}spreadsheets_provision_key_idx;

ALTER TABLE {{.TablePrefix}}spreadsheets DROP COLUMN provision_key;

-- +goose StatementEnd
