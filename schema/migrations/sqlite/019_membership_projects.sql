-- +goose Up
-- +goose StatementBegin

ALTER TABLE {{.TablePrefix}}memberships
  ADD COLUMN all_projects INTEGER NOT NULL DEFAULT 1 CHECK (role <> 'owner' OR all_projects = 1);

ALTER TABLE {{.TablePrefix}}invites
  ADD COLUMN all_projects INTEGER NOT NULL DEFAULT 1 CHECK (role <> 'owner' OR all_projects = 1);

-- SECURITY: SQLite has no RLS, so the composite keys are what keep a grant row inside its own org.
CREATE TABLE {{.TablePrefix}}membership_projects (
  org_id         TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  membership_id  TEXT NOT NULL,
  project_id     TEXT NOT NULL,
  created_at     TEXT NOT NULL,
  PRIMARY KEY (membership_id, project_id),
  FOREIGN KEY (org_id, membership_id) REFERENCES {{.TablePrefix}}memberships(org_id, id) ON DELETE CASCADE,
  FOREIGN KEY (org_id, project_id) REFERENCES {{.TablePrefix}}projects(org_id, id) ON DELETE CASCADE
);
CREATE INDEX {{.TablePrefix}}membership_projects_org_project_idx
  ON {{.TablePrefix}}membership_projects (org_id, project_id);

CREATE TABLE {{.TablePrefix}}invite_projects (
  org_id      TEXT NOT NULL REFERENCES {{.TablePrefix}}orgs(id) ON DELETE CASCADE,
  invite_id   TEXT NOT NULL,
  project_id  TEXT NOT NULL,
  created_at  TEXT NOT NULL,
  PRIMARY KEY (invite_id, project_id),
  FOREIGN KEY (org_id, invite_id) REFERENCES {{.TablePrefix}}invites(org_id, id) ON DELETE CASCADE,
  FOREIGN KEY (org_id, project_id) REFERENCES {{.TablePrefix}}projects(org_id, id) ON DELETE CASCADE
);
CREATE INDEX {{.TablePrefix}}invite_projects_org_project_idx
  ON {{.TablePrefix}}invite_projects (org_id, project_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- NOTE: dropping the grant makes every restricted member and invite reach every project again.
DROP TABLE IF EXISTS {{.TablePrefix}}invite_projects;
DROP TABLE IF EXISTS {{.TablePrefix}}membership_projects;
ALTER TABLE {{.TablePrefix}}invites DROP COLUMN all_projects;
ALTER TABLE {{.TablePrefix}}memberships DROP COLUMN all_projects;

-- +goose StatementEnd
