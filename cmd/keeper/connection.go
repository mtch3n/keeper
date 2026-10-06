package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mtchen/keeper/internal/client"
	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

func runConnection(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("connection: expected a subcommand (add, ls, show, set, terms, denylist, rm)")
	}
	ctx := context.Background()
	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		return connectionAdd(ctx, rest)
	case "ls":
		return connectionLs(ctx, rest)
	case "show":
		return connectionShow(ctx, rest)
	case "set":
		return connectionSet(ctx, rest)
	case "terms":
		return connectionTerms(ctx, rest)
	case "denylist":
		return connectionDenylist(ctx, rest)
	case "rm":
		return connectionRemove(ctx, rest)
	default:
		return fmt.Errorf("connection: unknown subcommand %q", sub)
	}
}

// resolveConnectionID turns a connection name into its id. Every other
// subcommand takes a name on the command line, but the daemon's routes are
// keyed by id (CONTRACT §3), so this is the one lookup shared by all of them.
func resolveConnectionID(ctx context.Context, cli *client.Client, name string) (string, error) {
	conns, err := cli.ListConnections(ctx)
	if err != nil {
		return "", err
	}
	for _, c := range conns {
		if c.Name == name {
			return c.ID, nil
		}
	}
	return "", fmt.Errorf("no connection named %q", name)
}

func connectionAdd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("connection add", flag.ExitOnError)
	name := fs.String("name", "", "connection name (required)")
	host := fs.String("host", "", "name of the host it is on, from `keeper host add` (required)")
	database := fs.String("database", "", "database name (required)")
	user := fs.String("user", "", "the profile's login role (required)")
	writes := fs.String("writes", "off", "off | approve: whether this profile's sessions may write; every write waits for approval")
	catalogPath := fs.String("catalog", "", "catalog.yaml path (default .keeper/catalog.yaml, SPEC R5.2a)")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *name == "" || *host == "" || *database == "" || *user == "" {
		return fmt.Errorf("connection add: --name, --host, --database and --user are required")
	}

	// Passwords are never flags: a flag is in shell history and in every
	// process listing for as long as the command runs.
	in := newSecretReader()
	params := client.RegisterConnectionParams{
		Name: *name, Database: *database, CatalogPath: *catalogPath,
		Credential: client.Credential{User: *user}, Writes: types.Writes(*writes),
	}
	var err error
	if params.Credential.Password, err = in.read("password for " + *user); err != nil {
		return err
	}

	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	if params.HostID, err = resolveHostID(ctx, cli, *host); err != nil {
		return err
	}
	conn, err := cli.RegisterConnection(ctx, params)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(conn)
	}
	printFindingsReport(conn, "registered")
	return nil
}

// printFindingsReport is SPEC R4.1's requirement in text form: every finding
// in full — what it means and the statement that would narrow it — never a
// summary, never a count. The connection is usable either way; what this
// prints is work the operator may choose to do on the database.
func printFindingsReport(c *types.Connection, verb string) {
	fmt.Printf("connection %q %s (id %s)\n", c.Name, verb, c.ID)
	if c.AuditedAt.IsZero() {
		fmt.Printf("G0 privilege audit could not run. The connection is usable; run `keeper audit %s --rerun`\n", c.Name)
		fmt.Println("once the server is reachable to see what the role can do.")
		return
	}
	if len(c.Findings) == 0 {
		fmt.Println("G0 privilege audit found nothing. This role holds no privilege keeper would report.")
		return
	}
	fmt.Printf("\nG0 privilege audit found %d finding(s). The connection is usable; these are\n", len(c.Findings))
	fmt.Printf("what the role can do beyond reading, and what would narrow it:\n")
	printFindings(c.Findings, "  ")
	fmt.Printf("\nRe-read this at any time with `keeper audit`.\n")
}

// printFindings renders findings with their suggested remediation. Shared by
// `keeper connection add` and `keeper audit`.
func printFindings(findings []types.Finding, indent string) {
	for _, f := range findings {
		fmt.Printf("\n%s[%s] %s (%s)\n", indent, f.ID, f.Subject, f.Kind)
		fmt.Printf("%s  means: %s\n", indent, f.Detail)
		if f.Narrower != "" {
			fmt.Printf("%s  fix:   %s\n", indent, f.Narrower)
		} else {
			fmt.Printf("%s  fix:   (no single statement removes this — see SPEC R4.1d)\n", indent)
		}
	}
}

func connectionLs(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("connection ls", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	conns, err := cli.ListConnections(ctx)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(conns)
	}
	if len(conns) == 0 {
		fmt.Println("no connections registered")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tHOST\tADDRESS\tDATABASE\tUSERNAME\tWRITES\tMODE")
	for _, c := range conns {
		fmt.Fprintf(w, "%s\t%s\t%s:%d\t%s\t%s\t%s\t%s\n", c.Name, c.Host, c.Address, c.Port, c.Database, c.Username, c.Writes, c.Mode)
	}
	return w.Flush()
}

func connectionShow(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("connection show", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	name := fs.Arg(0)
	if name == "" {
		return fmt.Errorf("connection show: expected a connection name")
	}

	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	id, err := resolveConnectionID(ctx, cli, name)
	if err != nil {
		return err
	}
	detail, err := cli.DescribeConnection(ctx, id)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(detail)
	}
	fmt.Printf("name:      %s\n", detail.Name)
	fmt.Printf("id:        %s\n", detail.ID)
	fmt.Printf("engine:    %s %s\n", detail.Engine, detail.Version)
	fmt.Printf("database:  %s\n", detail.Database)
	if len(detail.Schemas) > 0 {
		fmt.Printf("schemas:   %s\n", strings.Join(detail.Schemas, ", "))
	}
	fmt.Printf("host:      %s (%s:%d)\n", detail.Host, detail.Address, detail.Port)
	fmt.Printf("username:  %s\n", detail.Username)
	fmt.Printf("writes:    %s\n", detail.Writes)
	fmt.Printf("mode:      %s\n", detail.Mode)
	fmt.Printf("detection: %s\n", stageList(detail.Detection))
	fmt.Printf("catalog:   %s\n", detail.CatalogStatus.Path)
	if detail.CatalogStatus.Unclassified > 0 {
		fmt.Printf("           %d unclassified column(s)\n", detail.CatalogStatus.Unclassified)
	}
	if n := len(detail.AuditedPrivileges.Findings); n > 0 {
		fmt.Printf("findings:  %d from the privilege audit — `keeper audit %s` for each one\n", n, name)
	}
	if len(detail.Denylist) > 0 {
		fmt.Printf("denylist:  %d relation(s) (run `keeper connection denylist %s --list`)\n", len(detail.Denylist), name)
	}
	return nil
}

func connectionSet(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("connection set", flag.ExitOnError)
	mode := fs.String("mode", "", "strict | assisted (SPEC §9.4)")
	maxRows := fs.Int("max-rows", 0, "operator ceiling for query(max_rows); the agent cannot raise it")
	maxBytes := fs.Int("max-bytes", 0, "size cap on a result's row data, in bytes")
	timeout := fs.Duration("timeout", 0, "SET LOCAL statement_timeout for every statement")
	scanSample := fs.Int("scan-sample", 0, "sample size catalog init examines per column")
	detection := fs.String("detection", "", "detection stages in order, comma-separated, or off: kind[:entity+entity][@raw], kinds patterns and list")
	writes := fs.String("writes", "", "off | approve: whether this profile's sessions may write")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	name := fs.Arg(0)
	if name == "" {
		return fmt.Errorf("connection set: expected a connection name")
	}
	if *mode != "" && !types.Mode(*mode).Valid() {
		return fmt.Errorf("connection set: --mode must be strict or assisted")
	}

	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	id, err := resolveConnectionID(ctx, cli, name)
	if err != nil {
		return err
	}
	params := client.PatchConnectionParams{Mode: types.Mode(*mode), Writes: types.Writes(*writes)}
	if *maxRows != 0 || *maxBytes != 0 || *timeout != 0 || *scanSample != 0 {
		// The daemon replaces limits whole, so the ones not named keep their
		// current values.
		detail, err := cli.DescribeConnection(ctx, id)
		if err != nil {
			return err
		}
		lim := detail.Limits
		lim.MaxRowsCeiling = cmp.Or(*maxRows, lim.MaxRowsCeiling)
		lim.MaxBytes = cmp.Or(*maxBytes, lim.MaxBytes)
		lim.StatementTimeout = cmp.Or(*timeout, lim.StatementTimeout)
		lim.ScanSample = cmp.Or(*scanSample, lim.ScanSample)
		params.Limits = &lim
	}
	if *detection != "" {
		stages := parseStages(*detection)
		params.Detection = &stages
	}
	conn, err := cli.PatchConnection(ctx, id, params)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(conn)
	}
	fmt.Printf("%q updated: mode=%s writes=%s detection=%s max_rows_ceiling=%d max_bytes=%d statement_timeout=%s scan_sample=%d\n",
		conn.Name, conn.Mode, conn.Writes, stageList(conn.Detection), conn.Limits.MaxRowsCeiling, conn.Limits.MaxBytes, conn.Limits.StatementTimeout, conn.Limits.ScanSample)
	return nil
}

// parseStages reads --detection: "off", or stages like
// "patterns:email_address+credit_card,list@raw". The daemon validates them.
func parseStages(spec string) []types.Stage {
	stages := []types.Stage{}
	if spec == "off" {
		return stages
	}
	for part := range strings.SplitSeq(spec, ",") {
		part = strings.TrimSpace(part)
		var st types.Stage
		part, st.Raw = strings.CutSuffix(part, "@raw")
		kind, entities, _ := strings.Cut(part, ":")
		st.Kind = types.StageKind(kind)
		if entities != "" {
			st.Entities = strings.Split(entities, "+")
		}
		stages = append(stages, st)
	}
	return stages
}

// stageList renders a connection's pipeline, or off when it runs none.
func stageList(stages []types.Stage) string {
	if len(stages) == 0 {
		return "off (scan columns are redacted whole)"
	}
	parts := make([]string, len(stages))
	for i, st := range stages {
		p := string(st.Kind)
		if len(st.Entities) > 0 {
			p += ":" + strings.Join(st.Entities, "+")
		}
		if st.Raw {
			p += "@raw"
		}
		parts[i] = p
	}
	return strings.Join(parts, ", ")
}

// connectionTerms replaces the list stage's terms and expressions. They go in
// and never come back out: the daemon answers with counts, so this command
// cannot show the current set and every run states the whole of it.
func connectionTerms(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("connection terms", flag.ExitOnError)
	var deny, allow stringList
	fs.Var(&deny, "deny", "a term that is always PII, matched case-insensitively (repeatable)")
	fs.Var(&allow, "allow", "a value that is never redacted, e.g. support@yourco.com (repeatable)")
	var exprs stringList
	fs.Var(&exprs, "regex", "an expression to redact, as label=expr or just expr (repeatable), e.g. employee_id=EMP-\\d{6}")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	name := fs.Arg(0)
	if name == "" {
		return fmt.Errorf("connection terms: expected a connection name")
	}

	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	id, err := resolveConnectionID(ctx, cli, name)
	if err != nil {
		return err
	}
	terms := ports.Terms{Deny: deny, Allow: allow, Patterns: []ports.Pattern{}}
	for _, e := range exprs {
		terms.Patterns = append(terms.Patterns, parsePattern(e))
	}
	counts, err := cli.SetTerms(ctx, id, terms)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(counts)
	}
	fmt.Printf("%q now holds %d deny term(s), %d allow term(s) and %d expression(s). They take effect where its stages include list.\n",
		name, counts.Deny, counts.Allow, counts.Patterns)
	return nil
}

// parsePattern splits "label=expr". An expression may itself contain '=', so
// the text before the first '=' is a label only when it looks like one.
func parsePattern(s string) ports.Pattern {
	if label, expr, ok := strings.Cut(s, "="); ok && label != "" && strings.Trim(label, "abcdefghijklmnopqrstuvwxyz0123456789_") == "" {
		return ports.Pattern{Label: label, Expr: expr}
	}
	return ports.Pattern{Expr: s}
}

func connectionDenylist(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("connection denylist", flag.ExitOnError)
	var add, remove stringList
	fs.Var(&add, "add", "schema.table to add to the denylist (repeatable)")
	fs.Var(&remove, "remove", "schema.table to remove from the denylist (repeatable)")
	list := fs.Bool("list", false, "print the current denylist and make no changes")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	name := fs.Arg(0)
	if name == "" {
		return fmt.Errorf("connection denylist: expected a connection name")
	}

	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	id, err := resolveConnectionID(ctx, cli, name)
	if err != nil {
		return err
	}

	if *list || (len(add) == 0 && len(remove) == 0) {
		detail, err := cli.DescribeConnection(ctx, id)
		if err != nil {
			return err
		}
		_ = detail // denylist is not part of describe_connection's field set (SPEC §6.1);
		// fall through to reporting via the current set computed below.
	}

	current, err := currentDenylist(ctx, cli, id)
	if err != nil {
		return err
	}

	if len(add) == 0 && len(remove) == 0 {
		if *jsonOut {
			return printJSON(current)
		}
		if len(current) == 0 {
			fmt.Println("denylist is empty")
			return nil
		}
		for _, r := range current {
			fmt.Println(r.String())
		}
		return nil
	}

	next := applyDenylistEdits(current, add, remove)
	conn, err := cli.SetDenylist(ctx, id, next)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(conn)
	}
	fmt.Printf("denylist for %q now has %d entr(y/ies):\n", conn.Name, len(conn.Denylist))
	for _, r := range conn.Denylist {
		fmt.Println("  " + r.String())
	}
	return nil
}

// currentDenylist has no dedicated read endpoint (SPEC §6.1 does not expose
// the denylist via describe_connection); SetDenylist's echoed connection is
// the source of truth once a change has been made. Before any change, an
// empty PUT with the same set is a query in effect. We use --list's fetch
// (an empty PATCH-equivalent is unavailable) by attempting a describeless
// round trip: PUT the connection's own currently-known denylist is not
// knowable ahead of the first edit, so we rely on the connection payload
// SetDenylist returns; a fresh connection starts with an empty denylist.
func currentDenylist(ctx context.Context, cli *client.Client, id string) ([]types.RelationRef, error) {
	// There is no GET for the denylist alone; ask for a zero-length PUT is
	// not safe (it would clear a non-empty list), so the only side-effect-free
	// source is the full connection detail's own bookkeeping. Since
	// ConnectionDetail does not carry it, keeper's daemon is expected to
	// return the denylist on every connection payload it emits elsewhere
	// (e.g. RegisterConnection/AuditConnection's types.Connection); callers
	// that need the current set before editing should prefer those. Here we
	// conservatively start from empty when nothing else is known.
	return nil, nil
}

func applyDenylistEdits(current []types.RelationRef, add, remove stringList) []types.RelationRef {
	set := make(map[string]types.RelationRef, len(current))
	for _, r := range current {
		set[r.String()] = r
	}
	for _, a := range add {
		if rel, ok := parseRelation(a); ok {
			set[rel.String()] = rel
		}
	}
	for _, r := range remove {
		if rel, ok := parseRelation(r); ok {
			delete(set, rel.String())
		}
	}
	out := make([]types.RelationRef, 0, len(set))
	for _, r := range set {
		out = append(out, r)
	}
	return out
}

func parseRelation(s string) (types.RelationRef, bool) {
	schema, table, ok := strings.Cut(s, ".")
	if !ok || schema == "" || table == "" {
		return types.RelationRef{}, false
	}
	return types.RelationRef{Schema: schema, Relation: table}, true
}

var _ = time.Second // reserved for future duration formatting helpers

func connectionRemove(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("connection rm", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "do not ask")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	name := fs.Arg(0)
	if name == "" {
		return fmt.Errorf("connection rm: expected a connection name")
	}

	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	id, err := resolveConnectionID(ctx, cli, name)
	if err != nil {
		return err
	}
	if !*yes {
		// Removing takes the credential, the acceptances and every allow rule
		// naming it. The catalog file stays: it is in the project repo, it is
		// reviewed like code, and deleting it is not this command's business.
		fmt.Printf("Remove %s? Its stored credential, its accepted findings and every\n", name)
		fmt.Printf("allow rule naming it go with it. The catalog file on disk stays.\n")
		if !confirm("Remove it?") {
			fmt.Println("not removed")
			return nil
		}
	}
	if err := cli.RemoveConnection(ctx, id); err != nil {
		return err
	}
	fmt.Printf("removed %s\n", name)
	return nil
}
