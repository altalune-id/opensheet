\set ON_ERROR_STOP on
\set QUIET on

-- Meant to be run via provision.sh, which wraps this + ops.template.sql in a
-- single BEGIN/COMMIT so partial failures roll back cleanly.

DO $$
DECLARE
  caller_super  bool;
  caller_create bool;
  caller_bypass bool;
BEGIN
  SELECT rolsuper, rolcreaterole, rolbypassrls
    INTO caller_super, caller_create, caller_bypass
    FROM pg_roles
   WHERE rolname = current_user;

  IF NOT (caller_super OR caller_create) THEN
    RAISE EXCEPTION 'bootstrap requires CREATEROLE or SUPERUSER (current role: %)', current_user;
  END IF;

  -- Postgres only lets a role that already holds BYPASSRLS confer it.
  IF NOT (caller_super OR caller_bypass)
     AND NOT EXISTS (SELECT 1 FROM pg_roles
                      WHERE rolname = '@@APP@@_owner' AND rolbypassrls) THEN
    RAISE EXCEPTION 'bootstrap requires BYPASSRLS or SUPERUSER to grant it to @@APP@@_owner (current role: %)', current_user
      USING HINT = 'Have a superuser run: CREATE ROLE @@APP@@_owner NOLOGIN CREATEROLE INHERIT BYPASSRLS; (or ALTER ROLE @@APP@@_owner BYPASSRLS if it exists) then re-run.';
  END IF;

  IF current_database() IN ('postgres', 'template0', 'template1') THEN
    RAISE EXCEPTION 'bootstrap must run inside the application DB, not %', current_database();
  END IF;
END $$;

-- Install trusted extensions as the caller (neondb_owner / postgres) — the
-- migrator role can't. Migrations' `CREATE EXTENSION IF NOT EXISTS` no-ops
-- after this.
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;

-- CREATEROLE: ops.sql SECURITY DEFINER procedures owned by @@APP@@_owner manage human roles.
-- SECURITY: BYPASSRLS is a role attribute, not a privilege — `GRANT @@APP@@_owner TO x` does not
-- confer it; only SET ROLE, or a SECURITY DEFINER function owned by @@APP@@_owner, picks it up.
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '@@APP@@_owner') THEN
    CREATE ROLE @@APP@@_owner NOLOGIN NOSUPERUSER NOCREATEDB CREATEROLE INHERIT BYPASSRLS;
  END IF;
END $$;

DO $$ BEGIN
  IF NOT (SELECT rolbypassrls FROM pg_roles WHERE rolname = '@@APP@@_owner') THEN
    ALTER ROLE @@APP@@_owner BYPASSRLS;
  END IF;
END $$;

-- psql :'var' interpolation doesn't reach inside dollar-quoted DO blocks;
-- gate role creation with \gset + \if so the password literal lives in plain
-- SQL context where psql client-side substitution works.
SELECT NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '@@APP@@_migrator') AS need_migrator \gset
\if :need_migrator
CREATE ROLE @@APP@@_migrator LOGIN PASSWORD :'migrator_password' NOSUPERUSER NOCREATEDB NOCREATEROLE INHERIT;
\endif

SELECT NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '@@APP@@_service') AS need_service \gset
\if :need_service
CREATE ROLE @@APP@@_service LOGIN PASSWORD :'service_password' NOSUPERUSER NOCREATEDB NOCREATEROLE INHERIT;
\endif

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '@@APP@@_editor') THEN
    CREATE ROLE @@APP@@_editor NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE INHERIT;
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '@@APP@@_reader') THEN
    CREATE ROLE @@APP@@_reader NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE INHERIT;
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '@@APP@@_ops') THEN
    CREATE ROLE @@APP@@_ops NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE INHERIT;
  END IF;
END $$;

GRANT @@APP@@_owner TO @@APP@@_migrator;
GRANT pg_read_all_data TO @@APP@@_reader;

GRANT @@APP@@_editor TO @@APP@@_owner WITH ADMIN OPTION;
GRANT @@APP@@_reader TO @@APP@@_owner WITH ADMIN OPTION;
GRANT @@APP@@_ops    TO @@APP@@_owner WITH ADMIN OPTION;

DO $$ BEGIN
  EXECUTE format('GRANT @@APP@@_owner TO %I', current_user);
END $$;

DO $$ BEGIN
  EXECUTE format(
    'GRANT CONNECT ON DATABASE %I TO @@APP@@_migrator, @@APP@@_service, @@APP@@_editor, @@APP@@_reader, @@APP@@_ops',
    current_database());
END $$;

GRANT USAGE ON SCHEMA public TO @@APP@@_service, @@APP@@_editor, @@APP@@_reader;

ALTER SCHEMA public OWNER TO @@APP@@_owner;

REVOKE CREATE ON SCHEMA public FROM PUBLIC;

DO $$ BEGIN
  EXECUTE format('REVOKE TEMP ON DATABASE %I FROM PUBLIC', current_database());
EXCEPTION WHEN insufficient_privilege THEN
  RAISE NOTICE 'skipped REVOKE TEMP FROM PUBLIC (provider restriction)';
END $$;

ALTER DEFAULT PRIVILEGES FOR ROLE @@APP@@_owner IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO @@APP@@_service;
ALTER DEFAULT PRIVILEGES FOR ROLE @@APP@@_owner IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO @@APP@@_service;
ALTER DEFAULT PRIVILEGES FOR ROLE @@APP@@_owner IN SCHEMA public
  GRANT EXECUTE ON FUNCTIONS TO @@APP@@_service;

ALTER DEFAULT PRIVILEGES FOR ROLE @@APP@@_owner IN SCHEMA public
  GRANT SELECT ON TABLES TO @@APP@@_editor;
ALTER DEFAULT PRIVILEGES FOR ROLE @@APP@@_owner IN SCHEMA public
  GRANT UPDATE ON TABLES TO @@APP@@_editor;
ALTER DEFAULT PRIVILEGES FOR ROLE @@APP@@_owner IN SCHEMA public
  GRANT SELECT ON SEQUENCES TO @@APP@@_editor;

-- SECURITY: @@APP@@_maintenance was a LOGIN BYPASSRLS credential. Roles are cluster-scoped,
-- so dropping the database does not remove it. Skipped with a notice if something depends on it.
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '@@APP@@_maintenance') THEN
    EXECUTE 'DROP OWNED BY @@APP@@_maintenance';
    EXECUTE 'DROP ROLE @@APP@@_maintenance';
  END IF;
EXCEPTION WHEN dependent_objects_still_exist OR insufficient_privilege THEN
  RAISE NOTICE 'skipped dropping @@APP@@_maintenance (%)', SQLERRM;
END $$;

DO $$ BEGIN
  EXECUTE format('REVOKE @@APP@@_owner FROM %I', current_user);
END $$;
