package pgaudit

import (
	"fmt"
	"strings"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// This file holds the SQL text and the pure, DB-free mapping from a scanned
// row to a types.Finding. Keeping the mapping pure — no *pgx.Conn anywhere in
// this file — is what lets SPEC R4.1's finding shapes be table-tested without
// a live PostgreSQL: pgaudit.go's job is only to run these queries and hand
// the rows to the functions below.

// membershipRoleNames is the fixed set of built-in roles §4.1 checks
// membership in. pg_has_role(..., 'MEMBER') is used rather than 'USAGE',
// because 'USAGE' misses a membership reachable only through SET ROLE:
// SPEC R4.1b.
var membershipRoleNames = []string{
	"pg_read_server_files",
	"pg_write_server_files",
	"pg_execute_server_program",
	"pg_read_all_data",
	"pg_monitor",
	"pg_read_all_stats",
	"pg_signal_backend",
}

// membershipDetail says in one sentence what holding each membership means.
var membershipDetail = map[string]string{
	"pg_read_server_files":      "can read any file the server process can read, including other databases' data directories",
	"pg_write_server_files":     "can write files as the server process, including files PostgreSQL itself relies on",
	"pg_execute_server_program": "can run arbitrary programs on the database server via COPY TO/FROM PROGRAM",
	"pg_read_all_data":          "can read every table in every database on this server regardless of any GRANT",
	"pg_monitor":                "can read server-wide statistics and pg_stat_activity for every other session",
	"pg_read_all_stats":         "can read statistics views covering every other role's activity",
	"pg_signal_backend":         "can cancel or terminate other sessions' backends",
}

// fileAndProgramFunctions is the fixed set of pg_catalog functions that read
// files or run programs, checked per overload: SPEC R4.1c.
var fileAndProgramFunctions = []string{
	"pg_read_file",
	"pg_read_binary_file",
	"pg_ls_dir",
	"pg_stat_file",
	"lo_import",
	"lo_export",
}

const attributesQuery = `
SELECT rolname, rolsuper, rolcreatedb, rolcreaterole, rolbypassrls, rolreplication
FROM pg_roles
WHERE rolname = current_user`

const membershipQuery = `
SELECT pr.rolname, pg_has_role(current_user, pr.oid, 'MEMBER') AS is_member
FROM pg_roles pr
WHERE pr.rolname = ANY($1)`

// relationPrivilegeQuery finds every reachable relation this role holds a
// non-SELECT privilege on. INSERT, UPDATE and REFERENCES are checked with
// has_any_column_privilege, because a column-level grant such as
// GRANT INSERT (id) ON events is invisible to has_table_privilege: SPEC
// R4.1a. DELETE and TRUNCATE have no column-level grant in PostgreSQL's
// privilege model at all, so has_table_privilege is the correct (and only)
// predicate for those two.
const relationPrivilegeQuery = `
SELECT n.nspname, c.relname,
       has_any_column_privilege(current_user, c.oid, 'INSERT')     AS ins,
       has_any_column_privilege(current_user, c.oid, 'UPDATE')     AS upd,
       has_table_privilege(current_user, c.oid, 'DELETE')          AS del,
       has_table_privilege(current_user, c.oid, 'TRUNCATE')        AS trunc,
       has_any_column_privilege(current_user, c.oid, 'REFERENCES') AS refs
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'v', 'm', 'p', 'f')
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg\_toast%'`

const schemaPrivilegeQuery = `
SELECT n.nspname, has_schema_privilege(current_user, n.oid, 'CREATE') AS can_create
FROM pg_namespace n
WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg\_toast%'
  AND n.nspname NOT LIKE 'pg\_temp\_%'
  AND n.nspname NOT LIKE 'pg\_toast\_temp\_%'`

// functionPrivilegeQuery checks EXECUTE per overload: GRANT EXECUTE ON
// pg_read_file(text) attaches to one signature and is missed by both the
// membership list and the SECURITY DEFINER rule: SPEC R4.1c.
const functionPrivilegeQuery = `
SELECT n.nspname, p.proname,
       pg_get_function_identity_arguments(p.oid) AS args,
       has_function_privilege(current_user, p.oid, 'EXECUTE') AS can_exec
FROM pg_proc p
JOIN pg_namespace n ON n.oid = p.pronamespace
WHERE n.nspname = 'pg_catalog'
  AND p.proname = ANY($1)`

// securityDefinerQuery finds SECURITY DEFINER functions this role can reach.
// has_function_privilege already accounts for the default PUBLIC EXECUTE
// grant PostgreSQL functions carry unless explicitly revoked, so a bare ACL
// check is not what is happening here: SPEC R4.1d. pg_catalog is
// deliberately included (not excluded, unlike the other audits above)
// because vendor-owned SECDEF functions such as Azure Flexible Server's
// azure_* helpers live there: SPEC R4.1d.
const securityDefinerQuery = `
SELECT n.nspname, p.proname,
       pg_get_function_identity_arguments(p.oid) AS args
FROM pg_proc p
JOIN pg_namespace n ON n.oid = p.pronamespace
WHERE p.prosecdef
  AND n.nspname <> 'information_schema'
  AND n.nspname NOT LIKE 'pg\_toast%'
  AND has_function_privilege(current_user, p.oid, 'EXECUTE')
  AND has_schema_privilege(current_user, n.oid, 'USAGE')`

type roleAttributesRow struct {
	RoleName    string `db:"rolname"`
	Super       bool   `db:"rolsuper"`
	CreateDB    bool   `db:"rolcreatedb"`
	CreateRole  bool   `db:"rolcreaterole"`
	BypassRLS   bool   `db:"rolbypassrls"`
	Replication bool   `db:"rolreplication"`
}

type membershipRow struct {
	RoleName string `db:"rolname"`
	Member   bool   `db:"is_member"`
}

type relationPrivRow struct {
	Schema     string `db:"nspname"`
	Table      string `db:"relname"`
	Insert     bool   `db:"ins"`
	Update     bool   `db:"upd"`
	Delete     bool   `db:"del"`
	Truncate   bool   `db:"trunc"`
	References bool   `db:"refs"`
}

type schemaPrivRow struct {
	Schema string `db:"nspname"`
	Create bool   `db:"can_create"`
}

type functionPrivRow struct {
	Schema string `db:"nspname"`
	Name   string `db:"proname"`
	Args   string `db:"args"`
	Exec   bool   `db:"can_exec"`
}

// secDefRow deliberately does not carry prosrc. Nothing downstream reads a
// function's body now that findings are advisory, and a body can contain
// literals that SPEC R10a keeps out of anything loggable.
type secDefRow struct {
	Schema string `db:"nspname"`
	Name   string `db:"proname"`
	Args   string `db:"args"`
}

// quoteIdent double-quotes a SQL identifier, escaping embedded quotes, so
// Narrower statements are copyable as-is.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func attributeID(attr string) string  { return attr }
func membershipID(role string) string { return "membership:" + role }
func relationWriteID(schema, table string) string {
	return fmt.Sprintf("relation-write:%s.%s", schema, table)
}
func schemaCreateID(schema string) string { return "schema-create:" + schema }
func functionExecID(schema, name, args string) string {
	return fmt.Sprintf("function-exec:%s.%s(%s)", schema, name, args)
}
func securityDefinerID(schema, name, args string) string {
	return fmt.Sprintf("security-definer:%s.%s(%s)", schema, name, args)
}

// attributeFindings maps §4.1's role-attribute row to findings. ID is the bare
// attribute name, e.g. "rolsuper".
func attributeFindings(row roleAttributesRow) []types.Finding {
	role := quoteIdent(row.RoleName)
	var out []types.Finding
	add := func(attr, detail, narrower string, on bool) {
		if !on {
			return
		}
		out = append(out, types.Finding{
			ID:       attributeID(attr),
			Kind:     types.FindingAttribute,
			Subject:  attr,
			Detail:   detail,
			Narrower: narrower,
		})
	}
	add("rolsuper",
		"This role is a PostgreSQL superuser: every privilege check the database itself performs is bypassed for it, so keeper's checks are the only backstop left.",
		"ALTER ROLE "+role+" NOSUPERUSER;", row.Super)
	add("rolcreatedb",
		"This role can create new databases on the server.",
		"ALTER ROLE "+role+" NOCREATEDB;", row.CreateDB)
	add("rolcreaterole",
		"This role can create and alter other roles, including granting itself further privileges later.",
		"ALTER ROLE "+role+" NOCREATEROLE;", row.CreateRole)
	add("rolbypassrls",
		"This role bypasses row-level security policies on every table that has them.",
		"ALTER ROLE "+role+" NOBYPASSRLS;", row.BypassRLS)
	add("rolreplication",
		"This role can open a physical or logical replication connection and read the write-ahead log.",
		"ALTER ROLE "+role+" NOREPLICATION;", row.Replication)
	return out
}

// membershipFindings maps §4.1's membership rows to findings. currentUser
// names the audited role, for the Narrower REVOKE.
func membershipFindings(currentUser string, rows []membershipRow) []types.Finding {
	var out []types.Finding
	for _, r := range rows {
		if !r.Member {
			continue
		}
		detail := membershipDetail[r.RoleName]
		if detail == "" {
			detail = "grants additional server-wide capability"
		}
		out = append(out, types.Finding{
			ID:       membershipID(r.RoleName),
			Kind:     types.FindingMembership,
			Subject:  r.RoleName,
			Detail:   fmt.Sprintf("This role is a member of %s (directly, or reachable via SET ROLE), which %s.", r.RoleName, detail),
			Narrower: fmt.Sprintf(`REVOKE %s FROM %s;`, quoteIdent(r.RoleName), quoteIdent(currentUser)),
		})
	}
	return out
}

// relationWriteFindings maps §4.1a's per-relation privilege rows to findings,
// one per relation, naming the operations held. For a write credential
// (ports.RoleWrite), INSERT/UPDATE/DELETE are not findings — that is what the
// credential is for — but TRUNCATE and REFERENCES still are: SPEC R4.2a.
func relationWriteFindings(currentUser string, rows []relationPrivRow, role ports.Role) []types.Finding {
	writeCredential := role == ports.RoleWrite
	var out []types.Finding
	for _, r := range rows {
		var ops []string
		if r.Insert && !writeCredential {
			ops = append(ops, "INSERT")
		}
		if r.Update && !writeCredential {
			ops = append(ops, "UPDATE")
		}
		if r.Delete && !writeCredential {
			ops = append(ops, "DELETE")
		}
		if r.Truncate {
			ops = append(ops, "TRUNCATE")
		}
		if r.References {
			ops = append(ops, "REFERENCES")
		}
		if len(ops) == 0 {
			continue
		}
		rel := quoteIdent(r.Schema) + "." + quoteIdent(r.Table)
		out = append(out, types.Finding{
			ID:       relationWriteID(r.Schema, r.Table),
			Kind:     types.FindingRelationWrite,
			Subject:  r.Schema + "." + r.Table,
			Detail:   fmt.Sprintf("This role holds %s on %s.%s.", strings.Join(ops, ", "), r.Schema, r.Table),
			Narrower: fmt.Sprintf(`REVOKE %s ON %s FROM %s;`, strings.Join(ops, ", "), rel, quoteIdent(currentUser)),
		})
	}
	return out
}

// schemaCreateFindings maps §4.1a's schema CREATE rows to findings.
func schemaCreateFindings(currentUser string, rows []schemaPrivRow) []types.Finding {
	var out []types.Finding
	for _, r := range rows {
		if !r.Create {
			continue
		}
		out = append(out, types.Finding{
			ID:       schemaCreateID(r.Schema),
			Kind:     types.FindingSchemaCreate,
			Subject:  r.Schema,
			Detail:   fmt.Sprintf("This role can create new relations in schema %s, which an audit performed before they existed never examined.", r.Schema),
			Narrower: fmt.Sprintf(`REVOKE CREATE ON SCHEMA %s FROM %s;`, quoteIdent(r.Schema), quoteIdent(currentUser)),
		})
	}
	return out
}

// functionExecFindings maps §4.1c's per-overload EXECUTE rows to findings.
func functionExecFindings(currentUser string, rows []functionPrivRow) []types.Finding {
	var out []types.Finding
	for _, r := range rows {
		if !r.Exec {
			continue
		}
		out = append(out, types.Finding{
			ID:      functionExecID(r.Schema, r.Name, r.Args),
			Kind:    types.FindingFunctionExec,
			Subject: fmt.Sprintf("%s.%s(%s)", r.Schema, r.Name, r.Args),
			Detail:  fmt.Sprintf("This role can execute %s.%s(%s), which reads files or runs programs as the PostgreSQL server process.", r.Schema, r.Name, r.Args),
			Narrower: fmt.Sprintf(`REVOKE EXECUTE ON FUNCTION %s.%s(%s) FROM %s;`,
				quoteIdent(r.Schema), quoteIdent(r.Name), r.Args, quoteIdent(currentUser)),
		})
	}
	return out
}

// securityDefinerFindings maps §4.1d's reachable-SECDEF rows to findings.
// Narrower is left empty: a SECDEF function's reachability can come from a
// PUBLIC grant, an explicit grant, or schema USAGE combined with the default
// PUBLIC EXECUTE, and these functions are frequently vendor-owned (SPEC
// R4.1d's azure_sys_fn example), so no single REVOKE is safe to propose as
// copyable-as-is.
func securityDefinerFindings(rows []secDefRow) []types.Finding {
	var out []types.Finding
	for _, r := range rows {
		sig := fmt.Sprintf("%s.%s(%s)", r.Schema, r.Name, r.Args)
		out = append(out, types.Finding{
			ID:      securityDefinerID(r.Schema, r.Name, r.Args),
			Kind:    types.FindingSecurityDefine,
			Subject: sig,
			Detail:  fmt.Sprintf("SECURITY DEFINER function %s runs with its defining owner's privileges whenever this role calls it.", sig),
		})
	}
	return out
}
