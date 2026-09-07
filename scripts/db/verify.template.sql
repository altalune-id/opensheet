\set ON_ERROR_STOP on
\set QUIET on

DO $$
DECLARE
  wrong_owned  int;
  wrong_seq    int;
  missing_defs int;
  offenders    text;
  owner_bypass bool;
BEGIN
  SELECT rolbypassrls
    INTO owner_bypass
    FROM pg_roles
   WHERE rolname = '@@APP@@_owner';

  IF owner_bypass IS NULL THEN
    RAISE EXCEPTION 'verify: role @@APP@@_owner does not exist'
      USING HINT = 'Re-run bootstrap.';
  END IF;

  IF NOT owner_bypass THEN
    RAISE EXCEPTION 'verify: @@APP@@_owner lacks BYPASSRLS'
      USING HINT = 'ALTER ROLE @@APP@@_owner BYPASSRLS; without it the SECURITY DEFINER org readers it owns see zero orgs.';
  END IF;

  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '@@APP@@_maintenance') THEN
    RAISE NOTICE 'verify: @@APP@@_maintenance still exists — retired; drop it once nothing connects as it';
  END IF;

  SELECT count(*),
         string_agg(format('%I(%I)', c.relname, r.rolname), ', ')
    INTO wrong_owned, offenders
    FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    JOIN pg_roles r     ON r.oid = c.relowner
   WHERE n.nspname = 'public'
     AND c.relkind IN ('r','p')
     AND r.rolname <> '@@APP@@_owner';

  IF wrong_owned > 0 THEN
    RAISE EXCEPTION 'verify: % table(s) not owned by @@APP@@_owner: %', wrong_owned, offenders
      USING HINT = 'Migrations must SET ROLE @@APP@@_owner. Check AUTHL_*_DATABASE_MIGRATIONROLE.';
  END IF;

  SELECT count(*)
    INTO wrong_seq
    FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    JOIN pg_roles r     ON r.oid = c.relowner
   WHERE n.nspname = 'public'
     AND c.relkind = 'S'
     AND r.rolname <> '@@APP@@_owner';

  IF wrong_seq > 0 THEN
    RAISE EXCEPTION 'verify: % sequence(s) not owned by @@APP@@_owner', wrong_seq;
  END IF;

  SELECT count(*)
    INTO missing_defs
    FROM (VALUES
      ('@@APP@@_service'),
      ('@@APP@@_editor')
    ) AS req(grantee)
   WHERE NOT EXISTS (
     SELECT 1
       FROM pg_default_acl da
       JOIN pg_roles gr ON gr.oid = da.defaclrole
       JOIN pg_namespace ns ON ns.oid = da.defaclnamespace
      WHERE gr.rolname = '@@APP@@_owner'
        AND ns.nspname = 'public'
        AND da.defaclobjtype = 'r'
        AND array_to_string(da.defaclacl, ',') LIKE '%' || req.grantee || '=%'
   );

  IF missing_defs > 0 THEN
    RAISE EXCEPTION 'verify: default privileges missing for % expected grantee(s)', missing_defs
      USING HINT = 'Re-run bootstrap to re-apply ALTER DEFAULT PRIVILEGES.';
  END IF;

  RAISE NOTICE 'verify: OK';
END $$;
