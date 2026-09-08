-- +goose Up
-- +goose StatementBegin

CREATE TABLE {{.TablePrefix}}sessions (
  sid         TEXT PRIMARY KEY,
  user_id     TEXT NOT NULL REFERENCES {{.TablePrefix}}users(id) ON DELETE CASCADE,
  -- SECURITY: sealed Principal, AAD-bound to sid — it carries a live IdP ID token.
  payload     BLOB NOT NULL,
  expires_at  TEXT NOT NULL,
  created_at  TEXT NOT NULL
);

CREATE INDEX {{.TablePrefix}}sessions_expires_idx
  ON {{.TablePrefix}}sessions (expires_at);

CREATE INDEX {{.TablePrefix}}sessions_user_idx
  ON {{.TablePrefix}}sessions (user_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS {{.TablePrefix}}sessions_user_idx;
DROP INDEX IF EXISTS {{.TablePrefix}}sessions_expires_idx;
DROP TABLE IF EXISTS {{.TablePrefix}}sessions;

-- +goose StatementEnd
