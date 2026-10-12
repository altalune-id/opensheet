-- +goose Up
-- +goose StatementBegin

-- NOTE: a key principal carries no user, so user_id is nullable and created_by_key_id names the author instead.
ALTER TABLE {{.Schema}}.{{.TablePrefix}}todos ALTER COLUMN user_id DROP NOT NULL;

ALTER TABLE {{.Schema}}.{{.TablePrefix}}todos
  ADD COLUMN created_by_key_id UUID REFERENCES {{.Schema}}.{{.TablePrefix}}api_keys(id) ON DELETE SET NULL;

ALTER TABLE {{.Schema}}.{{.TablePrefix}}todos
  ADD CONSTRAINT {{.TablePrefix}}todos_author_not_both
  CHECK (user_id IS NULL OR created_by_key_id IS NULL);

CREATE INDEX {{.TablePrefix}}todos_created_by_key_idx
  ON {{.Schema}}.{{.TablePrefix}}todos (created_by_key_id)
  WHERE created_by_key_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS {{.Schema}}.{{.TablePrefix}}todos_created_by_key_idx;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}todos DROP CONSTRAINT IF EXISTS {{.TablePrefix}}todos_author_not_both;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}todos DROP COLUMN IF EXISTS created_by_key_id;
ALTER TABLE {{.Schema}}.{{.TablePrefix}}todos ALTER COLUMN user_id SET NOT NULL;

-- +goose StatementEnd
