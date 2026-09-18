-- SPEC §15 gate 6 — the Azure SECURITY DEFINER inventory.
--
-- R4.1d exists because a managed PostgreSQL ships superuser-owned SECDEF
-- functions an operator cannot REVOKE on, so an unconditional refusal would make
-- registration impossible there. The open question is how many there are: a
-- handful is a per-function acceptance flow, and several hundred is a wall.
--
-- Read-only. Run it as the role keeper would register, against the server you
-- actually use. Nothing here writes, and nothing here reads a row of application
-- data — every query is against the system catalogs.

-- 1. How many SECDEF functions can this role reach at all?
SELECT count(*) AS reachable_secdef
  FROM pg_proc p
  JOIN pg_namespace n ON n.oid = p.pronamespace
 WHERE p.prosecdef
   AND has_function_privilege(current_user, p.oid, 'EXECUTE')
   AND has_schema_privilege(current_user, n.oid, 'USAGE');

-- 2. Grouped by schema and owner, which is what decides whether R4.1d's
--    per-function acceptance is workable. Vendor-owned functions in pg_catalog
--    and azure_* schemas are the ones nobody can revoke.
SELECT n.nspname                     AS schema,
       pg_get_userbyid(p.proowner)   AS owner,
       count(*)                      AS n
  FROM pg_proc p
  JOIN pg_namespace n ON n.oid = p.pronamespace
 WHERE p.prosecdef
   AND has_function_privilege(current_user, p.oid, 'EXECUTE')
   AND has_schema_privilege(current_user, n.oid, 'USAGE')
 GROUP BY 1, 2
 ORDER BY n DESC;

-- 3. The ones whose owner is a superuser — the subset that actually widens what
--    the role can do, and the subset R4.1g binds an acceptance to by hashing
--    prosrc and the signature. The first column is the finding id keeper would
--    ask you to accept.
SELECT n.nspname || '.' || p.proname || '(' ||
         pg_get_function_identity_arguments(p.oid) || ')' AS finding_id,
       pg_get_userbyid(p.proowner)                        AS owner
  FROM pg_proc p
  JOIN pg_namespace n ON n.oid = p.pronamespace
  JOIN pg_roles r ON r.oid = p.proowner
 WHERE p.prosecdef
   AND r.rolsuper
   AND has_function_privilege(current_user, p.oid, 'EXECUTE')
   AND has_schema_privilege(current_user, n.oid, 'USAGE')
 ORDER BY 1;

-- 4. Whether PUBLIC holds EXECUTE, which is the route an ACL-only check misses
--    and the reason R4.1c audits per overload.
SELECT count(*) AS public_executable_secdef
  FROM pg_proc p
 WHERE p.prosecdef
   AND (p.proacl IS NULL OR array_to_string(p.proacl, ',') LIKE '%=X/%');
