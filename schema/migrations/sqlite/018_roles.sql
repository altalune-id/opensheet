-- +goose Up
-- +goose StatementBegin

CREATE TABLE {{.TablePrefix}}roles (
  id           TEXT PRIMARY KEY,
  org_id       TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  description  TEXT NOT NULL DEFAULT '',
  permissions  TEXT NOT NULL,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  version      INTEGER NOT NULL DEFAULT 1,
  UNIQUE (org_id, id)
);
CREATE UNIQUE INDEX {{.TablePrefix}}roles_org_name_idx
  ON {{.TablePrefix}}roles (org_id, lower(name));

-- NOTE: SQLite cannot alter a CHECK, so memberships and invites are rebuilds; no table references either.
CREATE TABLE {{.TablePrefix}}memberships_new (
  id                  TEXT PRIMARY KEY,
  org_id              TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  user_id             TEXT NOT NULL REFERENCES {{.TablePrefix}}users(id) ON DELETE CASCADE,
  role                TEXT NOT NULL CHECK (role IN ('owner','admin','member','custom')),
  role_id             TEXT,
  system              INTEGER NOT NULL DEFAULT 0,
  created_at          TEXT NOT NULL,
  UNIQUE (org_id, user_id),
  UNIQUE (org_id, id),
  CONSTRAINT {{.TablePrefix}}memberships_role_custom CHECK ((role = 'custom') = (role_id IS NOT NULL)),
  CONSTRAINT {{.TablePrefix}}memberships_role_fk FOREIGN KEY (org_id, role_id) REFERENCES {{.TablePrefix}}roles (org_id, id)
);
INSERT INTO {{.TablePrefix}}memberships_new (id, org_id, user_id, role, role_id, system, created_at)
SELECT id, org_id, user_id, role, NULL, system, created_at FROM {{.TablePrefix}}memberships;
DROP TABLE {{.TablePrefix}}memberships;
ALTER TABLE {{.TablePrefix}}memberships_new RENAME TO {{.TablePrefix}}memberships;
CREATE INDEX {{.TablePrefix}}memberships_user_idx
  ON {{.TablePrefix}}memberships (user_id);
CREATE INDEX {{.TablePrefix}}memberships_role_idx
  ON {{.TablePrefix}}memberships (org_id, role_id) WHERE role_id IS NOT NULL;

CREATE TABLE {{.TablePrefix}}invites_new (
  id                  TEXT PRIMARY KEY,
  org_id              TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  email               TEXT NOT NULL,
  role                TEXT NOT NULL CHECK (role IN ('owner','admin','member','custom')),
  role_id             TEXT,
  token_hash          TEXT NOT NULL,
  expires_at          TEXT NOT NULL,
  accepted_at         TEXT,
  invited_by          TEXT NOT NULL REFERENCES {{.TablePrefix}}users(id),
  created_at          TEXT NOT NULL,
  UNIQUE (org_id, id),
  CONSTRAINT {{.TablePrefix}}invites_role_custom CHECK ((role = 'custom') = (role_id IS NOT NULL)),
  CONSTRAINT {{.TablePrefix}}invites_role_fk FOREIGN KEY (org_id, role_id) REFERENCES {{.TablePrefix}}roles (org_id, id)
);
INSERT INTO {{.TablePrefix}}invites_new (id, org_id, email, role, role_id, token_hash, expires_at, accepted_at, invited_by, created_at)
SELECT id, org_id, email, role, NULL, token_hash, expires_at, accepted_at, invited_by, created_at FROM {{.TablePrefix}}invites;
DROP TABLE {{.TablePrefix}}invites;
ALTER TABLE {{.TablePrefix}}invites_new RENAME TO {{.TablePrefix}}invites;
CREATE INDEX {{.TablePrefix}}invites_email_pending_idx
  ON {{.TablePrefix}}invites (email)
  WHERE accepted_at IS NULL;
CREATE INDEX {{.TablePrefix}}invites_role_idx
  ON {{.TablePrefix}}invites (org_id, role_id) WHERE role_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- NOTE: custom-role members and invites cannot satisfy the restored check and are dropped.
CREATE TABLE {{.TablePrefix}}memberships_old (
  id                  TEXT PRIMARY KEY,
  org_id              TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  user_id             TEXT NOT NULL REFERENCES {{.TablePrefix}}users(id) ON DELETE CASCADE,
  role                TEXT NOT NULL CHECK (role IN ('owner','admin','member')),
  system              INTEGER NOT NULL DEFAULT 0,
  created_at          TEXT NOT NULL,
  UNIQUE (org_id, user_id)
);
INSERT INTO {{.TablePrefix}}memberships_old (id, org_id, user_id, role, system, created_at)
SELECT id, org_id, user_id, role, system, created_at FROM {{.TablePrefix}}memberships WHERE role <> 'custom';
DROP TABLE {{.TablePrefix}}memberships;
ALTER TABLE {{.TablePrefix}}memberships_old RENAME TO {{.TablePrefix}}memberships;
CREATE INDEX {{.TablePrefix}}memberships_user_idx
  ON {{.TablePrefix}}memberships (user_id);

CREATE TABLE {{.TablePrefix}}invites_old (
  id                  TEXT PRIMARY KEY,
  org_id              TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  email               TEXT NOT NULL,
  role                TEXT NOT NULL CHECK (role IN ('owner','admin','member')),
  token_hash          TEXT NOT NULL,
  expires_at          TEXT NOT NULL,
  accepted_at         TEXT,
  invited_by          TEXT NOT NULL REFERENCES {{.TablePrefix}}users(id),
  created_at          TEXT NOT NULL
);
INSERT INTO {{.TablePrefix}}invites_old (id, org_id, email, role, token_hash, expires_at, accepted_at, invited_by, created_at)
SELECT id, org_id, email, role, token_hash, expires_at, accepted_at, invited_by, created_at FROM {{.TablePrefix}}invites WHERE role <> 'custom';
DROP TABLE {{.TablePrefix}}invites;
ALTER TABLE {{.TablePrefix}}invites_old RENAME TO {{.TablePrefix}}invites;
CREATE INDEX {{.TablePrefix}}invites_email_pending_idx
  ON {{.TablePrefix}}invites (email)
  WHERE accepted_at IS NULL;

DROP TABLE IF EXISTS {{.TablePrefix}}roles;

-- +goose StatementEnd
