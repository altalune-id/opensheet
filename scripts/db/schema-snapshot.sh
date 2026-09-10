#!/usr/bin/env bash
set -euo pipefail
dsn="$1"; sch="$2"; out="$3"
mkdir -p "$out"

pg_dump --restrict-key=fixed --schema-only --no-owner --schema="$sch" "$dsn" > "$out/pg_dump.sql"

psql "$dsn" -Atc "
  SELECT c.relname, p.polname, p.polcmd, p.polpermissive,
         pg_get_expr(p.polqual, p.polrelid), pg_get_expr(p.polwithcheck, p.polrelid)
  FROM pg_policy p JOIN pg_class c ON c.oid = p.polrelid
  WHERE c.relnamespace = to_regnamespace('$sch') ORDER BY 1, 2" > "$out/policies.txt"

psql "$dsn" -Atc "
  SELECT relname, relrowsecurity, relforcerowsecurity FROM pg_class
  WHERE relkind = 'r' AND relnamespace = to_regnamespace('$sch') ORDER BY 1" > "$out/rls.txt"

psql "$dsn" -Atc "
  SELECT p.proname, pg_get_function_identity_arguments(p.oid), p.prosecdef, p.provolatile,
         p.proconfig::text, pg_get_userbyid(p.proowner), coalesce(p.proacl::text, '')
  FROM pg_proc p WHERE p.pronamespace = to_regnamespace('$sch') ORDER BY 1, 2" > "$out/functions.txt"

psql "$dsn" -Atc "
  SELECT conrelid::regclass::text, conname, contype, confdeltype, pg_get_constraintdef(oid)
  FROM pg_constraint WHERE connamespace = to_regnamespace('$sch') ORDER BY 1, 2" > "$out/constraints.txt"

psql "$dsn" -Atc "
  SELECT c.relname, a.attnum, a.attname, format_type(a.atttypid, a.atttypmod), a.attnotnull
  FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid
  WHERE c.relnamespace = to_regnamespace('$sch') AND c.relkind = 'r' AND a.attnum > 0
    AND NOT a.attisdropped ORDER BY 1, 2" > "$out/columns.txt"
