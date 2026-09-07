-- +goose Up
-- +goose StatementBegin

CREATE TABLE {{.TablePrefix}}bootstrap (
  id            INTEGER PRIMARY KEY CHECK (id = 1),
  onboarded_at  DATETIME NOT NULL,
  onboarded_by  TEXT NOT NULL REFERENCES {{.TablePrefix}}users(id),
  method        TEXT NOT NULL CHECK (method IN ('env-genesis','web-onboard','cli-init'))
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS {{.TablePrefix}}bootstrap;

-- +goose StatementEnd
