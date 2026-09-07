\set ON_ERROR_STOP on
\set QUIET on

-- Meant to be run via provision.sh, which wraps bootstrap + this file in a
-- single BEGIN/COMMIT. When run standalone, wrap with your own transaction
-- and grant/revoke @@APP@@_owner membership around it.

-- Re-open the caller's membership window (bootstrap revokes it at its tail).
DO $$ BEGIN
  EXECUTE format('GRANT @@APP@@_owner TO %I', current_user);
END $$;

CREATE SCHEMA IF NOT EXISTS @@APP@@_ops AUTHORIZATION @@APP@@_owner;

REVOKE ALL   ON SCHEMA @@APP@@_ops FROM PUBLIC;
GRANT  USAGE ON SCHEMA @@APP@@_ops TO   @@APP@@_ops;

CREATE OR REPLACE FUNCTION @@APP@@_ops._assert_known_group(group_role text)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
BEGIN
  IF group_role NOT IN ('@@APP@@_editor', '@@APP@@_reader', '@@APP@@_ops') THEN
    RAISE EXCEPTION 'unknown group_role %; expected @@APP@@_editor | @@APP@@_reader | @@APP@@_ops', group_role;
  END IF;
END $$;

ALTER FUNCTION @@APP@@_ops._assert_known_group(text) OWNER TO @@APP@@_owner;

CREATE OR REPLACE PROCEDURE @@APP@@_ops.onboard(
  username    text,
  password    text,
  group_role  text,
  valid_until date DEFAULT NULL
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
BEGIN
  IF username IS NULL OR btrim(username) = '' THEN
    RAISE EXCEPTION 'username must not be empty';
  END IF;
  IF password IS NULL OR length(password) < 16 THEN
    RAISE EXCEPTION 'password must be at least 16 characters';
  END IF;

  PERFORM @@APP@@_ops._assert_known_group(group_role);

  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = username) THEN
    RAISE EXCEPTION 'role % already exists; use grant_membership() to add app access, or rotate_password() to update credentials', username;
  END IF;

  IF valid_until IS NULL THEN
    EXECUTE format(
      'CREATE ROLE %I LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE INHERIT',
      username, password);
  ELSE
    EXECUTE format(
      'CREATE ROLE %I LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE INHERIT VALID UNTIL %L',
      username, password, valid_until::text);
  END IF;

  EXECUTE format('GRANT %I TO %I', group_role, username);

  RAISE NOTICE 'onboarded % into % (valid_until=%)', username, group_role, coalesce(valid_until::text, 'no expiry');
END $$;

ALTER PROCEDURE @@APP@@_ops.onboard(text, text, text, date) OWNER TO @@APP@@_owner;

CREATE OR REPLACE PROCEDURE @@APP@@_ops.rotate_password(
  username        text,
  new_password    text,
  new_valid_until date DEFAULT NULL
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
BEGIN
  IF username IS NULL OR btrim(username) = '' THEN
    RAISE EXCEPTION 'username must not be empty';
  END IF;
  IF new_password IS NULL OR length(new_password) < 16 THEN
    RAISE EXCEPTION 'new_password must be at least 16 characters';
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = username) THEN
    RAISE EXCEPTION 'role % does not exist', username;
  END IF;

  IF new_valid_until IS NULL THEN
    EXECUTE format('ALTER ROLE %I WITH PASSWORD %L', username, new_password);
  ELSE
    EXECUTE format('ALTER ROLE %I WITH PASSWORD %L VALID UNTIL %L',
                   username, new_password, new_valid_until::text);
  END IF;

  RAISE NOTICE 'rotated password for % (valid_until=%)', username, coalesce(new_valid_until::text, 'unchanged');
END $$;

ALTER PROCEDURE @@APP@@_ops.rotate_password(text, text, date) OWNER TO @@APP@@_owner;

CREATE OR REPLACE PROCEDURE @@APP@@_ops.grant_membership(
  username   text,
  group_role text
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
BEGIN
  IF username IS NULL OR btrim(username) = '' THEN
    RAISE EXCEPTION 'username must not be empty';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = username) THEN
    RAISE EXCEPTION 'role % does not exist — call onboard() first', username;
  END IF;
  PERFORM @@APP@@_ops._assert_known_group(group_role);
  EXECUTE format('GRANT %I TO %I', group_role, username);
  RAISE NOTICE 'granted % to %', group_role, username;
END $$;

ALTER PROCEDURE @@APP@@_ops.grant_membership(text, text) OWNER TO @@APP@@_owner;

CREATE OR REPLACE PROCEDURE @@APP@@_ops.revoke_membership(
  username   text,
  group_role text
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
BEGIN
  IF username IS NULL OR btrim(username) = '' THEN
    RAISE EXCEPTION 'username must not be empty';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = username) THEN
    RAISE NOTICE 'role % does not exist; nothing to revoke', username;
    RETURN;
  END IF;
  PERFORM @@APP@@_ops._assert_known_group(group_role);
  EXECUTE format('REVOKE %I FROM %I', group_role, username);
  RAISE NOTICE 'revoked % from %', group_role, username;
END $$;

ALTER PROCEDURE @@APP@@_ops.revoke_membership(text, text) OWNER TO @@APP@@_owner;

-- Locks the account cluster-wide (NOLOGIN) but only revokes this app's
-- memberships. Other apps' memberships stay assigned but are moot while
-- NOLOGIN — call each app's offboard() to fully clear the graph.
CREATE OR REPLACE PROCEDURE @@APP@@_ops.offboard(username text)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
  grp text;
BEGIN
  IF username IS NULL OR btrim(username) = '' THEN
    RAISE EXCEPTION 'username must not be empty';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = username) THEN
    RAISE NOTICE 'role % does not exist; nothing to offboard', username;
    RETURN;
  END IF;

  FOR grp IN
    SELECT r.rolname
      FROM pg_auth_members am
      JOIN pg_roles r ON r.oid = am.roleid
      JOIN pg_roles m ON m.oid = am.member
     WHERE m.rolname = username
       AND r.rolname LIKE '@@APP@@\_%' ESCAPE '\'
  LOOP
    EXECUTE format('REVOKE %I FROM %I', grp, username);
  END LOOP;

  EXECUTE format('REVOKE ALL ON DATABASE %I FROM %I', current_database(), username);
  EXECUTE format('ALTER ROLE %I NOLOGIN', username);

  RAISE NOTICE 'offboarded % (locked, memberships revoked)', username;
END $$;

ALTER PROCEDURE @@APP@@_ops.offboard(text) OWNER TO @@APP@@_owner;

CREATE OR REPLACE FUNCTION @@APP@@_ops.list_operators()
RETURNS TABLE (
  username    name,
  groups      text[],
  valid_until timestamptz,
  is_locked   boolean,
  is_expired  boolean
)
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
  SELECT r.rolname AS username,
         COALESCE(
           (SELECT array_agg(g.rolname ORDER BY g.rolname)
              FROM pg_auth_members am
              JOIN pg_roles g ON g.oid = am.roleid
             WHERE am.member = r.oid
               AND g.rolname LIKE '@@APP@@\_%' ESCAPE '\'),
           '{}'::text[]) AS groups,
         r.rolvaliduntil AS valid_until,
         NOT r.rolcanlogin AS is_locked,
         (r.rolvaliduntil IS NOT NULL AND r.rolvaliduntil < now()) AS is_expired
    FROM pg_roles r
   WHERE EXISTS (
           SELECT 1
             FROM pg_auth_members am
             JOIN pg_roles g ON g.oid = am.roleid
            WHERE am.member = r.oid
              AND g.rolname LIKE '@@APP@@\_%' ESCAPE '\')
     AND r.rolname NOT LIKE '@@APP@@\_%' ESCAPE '\'
   ORDER BY r.rolname;
$$;

ALTER FUNCTION @@APP@@_ops.list_operators() OWNER TO @@APP@@_owner;

REVOKE ALL ON ALL ROUTINES IN SCHEMA @@APP@@_ops FROM PUBLIC;

GRANT EXECUTE ON PROCEDURE @@APP@@_ops.onboard(text, text, text, date)   TO @@APP@@_ops;
GRANT EXECUTE ON PROCEDURE @@APP@@_ops.rotate_password(text, text, date) TO @@APP@@_ops;
GRANT EXECUTE ON PROCEDURE @@APP@@_ops.grant_membership(text, text)      TO @@APP@@_ops;
GRANT EXECUTE ON PROCEDURE @@APP@@_ops.revoke_membership(text, text)     TO @@APP@@_ops;
GRANT EXECUTE ON PROCEDURE @@APP@@_ops.offboard(text)                    TO @@APP@@_ops;
GRANT EXECUTE ON FUNCTION  @@APP@@_ops.list_operators()                  TO @@APP@@_ops;

DO $$ BEGIN
  EXECUTE format('REVOKE @@APP@@_owner FROM %I', current_user);
END $$;
