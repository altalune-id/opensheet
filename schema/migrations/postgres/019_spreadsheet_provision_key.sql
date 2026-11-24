-- +goose Up
-- +goose StatementBegin

ALTER TABLE {{.Schema}}.{{.TablePrefix}}spreadsheets ADD COLUMN provision_key TEXT;

CREATE UNIQUE INDEX {{.TablePrefix}}spreadsheets_provision_key_idx
  ON {{.Schema}}.{{.TablePrefix}}spreadsheets (project_id, provision_key)
  WHERE provision_key IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}spreadsheets_provision_key_idx;

ALTER TABLE {{.Schema}}.{{.TablePrefix}}spreadsheets DROP COLUMN IF EXISTS provision_key;

-- +goose StatementEnd
