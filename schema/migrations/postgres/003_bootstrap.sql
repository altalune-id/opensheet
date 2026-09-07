-- +goose Up
-- +goose StatementBegin

CREATE TABLE {{.Schema}}.{{.TablePrefix}}bootstrap (
  id            INT PRIMARY KEY CHECK (id = 1),
  onboarded_at  TIMESTAMPTZ NOT NULL,
  onboarded_by  UUID NOT NULL REFERENCES {{.Schema}}.{{.TablePrefix}}users(id),
  method        TEXT NOT NULL CHECK (method IN ('env-genesis','web-onboard','cli-init'))
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS {{.Schema}}.{{.TablePrefix}}bootstrap;

-- +goose StatementEnd
