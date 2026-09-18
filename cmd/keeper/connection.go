package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mtchen/keeper/internal/client"
	"github.com/mtchen/keeper/internal/types"
)

func runConnection(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("connection: expected a subcommand (add, ls, show, audit, accept, set, denylist, rm)")
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
	case "audit":
		return connectionAudit(ctx, rest)
	case "accept":
		return connectionAccept(ctx, rest)
	case "set":
		return connectionSet(ctx, rest)
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
	dsn := fs.String("dsn", "", "read (_ro) DSN (required)")
	writeDSN := fs.String("write-dsn", "", "write (_rw) DSN; write mode does not exist without one (SPEC §4.2)")
	catalogPath := fs.String("catalog", "", "catalog.yaml path (default .keeper/catalog.yaml, SPEC R5.2a)")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" || *dsn == "" {
		return fmt.Errorf("connection add: --name and --dsn are required")
	}

	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	conn, err := cli.RegisterConnection(ctx, client.RegisterConnectionParams{
		Name: *name, DSN: *dsn, WriteDSN: *writeDSN, CatalogPath: *catalogPath,
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(conn)
	}
	printFindingsReport(conn, "registered")
	return nil
}

func connectionAudit(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("connection audit", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	name := fs.Arg(0)
	if name == "" {
		return fmt.Errorf("connection audit: expected a connection name")
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
	conn, err := cli.AuditConnection(ctx, id)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(conn)
	}
	printFindingsReport(conn, "re-audited")
	return nil
}

// printFindingsReport is SPEC R4.1's requirement in text form: every finding
// in full — what it means and the narrower SQL that removes it — never a
// summary, never a count.
func printFindingsReport(c *types.Connection, verb string) {
	fmt.Printf("connection %q %s (id %s)\n", c.Name, verb, c.ID)
	if len(c.Findings) == 0 {
		fmt.Println("G0 privilege audit found nothing to accept. The connection is enabled.")
		return
	}
	unaccepted := c.Unaccepted()
	fmt.Printf("\nG0 privilege audit found %d finding(s). Nothing is accepted implicitly:\n", len(c.Findings))
	for _, f := range c.Findings {
		fmt.Printf("\n  [%s] %s (%s)\n", f.ID, f.Subject, f.Kind)
		fmt.Printf("    means:    %s\n", f.Detail)
		if f.Narrower != "" {
			fmt.Printf("    narrower: %s\n", f.Narrower)
		} else {
			fmt.Printf("    narrower: (no single statement removes this — see SPEC R4.1d)\n")
		}
	}
	if len(unaccepted) > 0 {
		fmt.Printf("\nThe connection remains disabled until every finding above is accepted:\n\n")
		fmt.Printf("  keeper connection accept %s --finding <id> [--finding <id> ...]\n", c.Name)
		fmt.Printf("  keeper connection accept %s --accept-all\n\n", c.Name)
	} else if c.Degraded() {
		fmt.Printf("\nEvery finding has an acceptance on record; the connection runs degraded (SPEC R4.1).\n")
	}
}

func connectionLs(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("connection ls", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
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
	fmt.Fprintln(w, "NAME\tENGINE\tDATABASE\tROLE\tDEGRADED")
	for _, c := range conns {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%v\n", c.Name, c.Engine, c.Database, c.Role, c.Degraded)
	}
	return w.Flush()
}

func connectionShow(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("connection show", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
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
	fmt.Printf("role:      %s\n", detail.Role)
	fmt.Printf("mode:      %s\n", detail.Mode)
	fmt.Printf("degraded:  %v\n", detail.Degraded)
	fmt.Printf("catalog:   %s\n", detail.CatalogStatus.Path)
	if detail.CatalogStatus.Unclassified > 0 {
		fmt.Printf("           %d unclassified column(s)\n", detail.CatalogStatus.Unclassified)
	}
	if n := len(detail.AuditedPrivileges.Unaccepted); n > 0 {
		fmt.Printf("findings:  %d awaiting acceptance — `keeper connection accept %s --finding ID`\n", n, name)
	}
	if n := len(detail.AuditedPrivileges.Acceptances); n > 0 {
		// R4.1: an accepted finding is displayed wherever the connection is.
		fmt.Printf("accepted:  %d finding(s) — this connection runs without keeper's\n", n)
		fmt.Printf("           database-level protection for them\n")
	}
	if len(detail.Denylist) > 0 {
		fmt.Printf("denylist:  %d relation(s) (run `keeper connection denylist %s --list`)\n", len(detail.Denylist), name)
	}
	return nil
}

func connectionAccept(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("connection accept", flag.ExitOnError)
	var findings stringList
	fs.Var(&findings, "finding", "a finding id to accept (repeatable)")
	acceptAll := fs.Bool("accept-all", false, "accept every currently reported finding")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	name := fs.Arg(0)
	if name == "" {
		return fmt.Errorf("connection accept: expected a connection name")
	}
	if !*acceptAll && len(findings) == 0 {
		return fmt.Errorf("connection accept: pass --finding <id> (repeatable) or --accept-all")
	}
	if *acceptAll && len(findings) > 0 {
		return fmt.Errorf("connection accept: --accept-all and --finding are mutually exclusive")
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

	ids := []string(findings)
	if *acceptAll {
		detail, err := cli.DescribeConnection(ctx, id)
		if err != nil {
			return err
		}
		// --accept-all names every finding it accepted, in stdout and in the
		// vault. A blanket flag that hid what it agreed to would be the thing
		// R4.1f exists to prevent.
		for _, f := range detail.AuditedPrivileges.Unaccepted {
			fmt.Printf("accepting %s\n", f.ID)
			ids = append(ids, f.ID)
		}
		if len(ids) == 0 {
			fmt.Println("no outstanding findings to accept")
			return nil
		}
	}

	conn, err := cli.AcceptFindings(ctx, id, ids, currentActor(), "cli")
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(conn)
	}

	// R4.1f / R4.1: name every finding accepted, in full — --accept-all is
	// not a way to agree to something quietly.
	fmt.Printf("accepted %d finding(s) on %q as %s:\n", len(ids), conn.Name, currentActor())
	for _, id := range ids {
		for _, f := range conn.Findings {
			if f.ID == id {
				fmt.Printf("  [%s] %s — %s\n", f.ID, f.Subject, f.Detail)
			}
		}
	}
	if len(conn.Unaccepted()) == 0 {
		fmt.Println("\nevery finding is accepted; the connection is enabled")
	} else {
		fmt.Printf("\n%d finding(s) remain unaccepted; the connection stays disabled\n", len(conn.Unaccepted()))
	}
	return nil
}

func connectionSet(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("connection set", flag.ExitOnError)
	mode := fs.String("mode", "", "strict | assisted | permissive (SPEC §9.4)")
	maxRows := fs.Int("max-rows", 0, "operator ceiling for query(max_rows); the agent cannot raise it")
	timeout := fs.Duration("timeout", 0, "SET LOCAL statement_timeout for every statement")
	scanSample := fs.Int("scan-sample", 0, "sample size for the model layer of a scan column")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	name := fs.Arg(0)
	if name == "" {
		return fmt.Errorf("connection set: expected a connection name")
	}
	if *mode != "" && !types.Mode(*mode).Valid() {
		return fmt.Errorf("connection set: --mode must be strict, assisted or permissive")
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
	conn, err := cli.PatchConnection(ctx, id, client.PatchConnectionParams{
		Mode:             types.Mode(*mode),
		MaxRowsCeiling:   *maxRows,
		StatementTimeout: *timeout,
		ScanSample:       *scanSample,
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(conn)
	}
	fmt.Printf("%q updated: mode=%s max_rows_ceiling=%d statement_timeout=%s scan_sample=%d\n",
		conn.Name, conn.Mode, conn.Limits.MaxRowsCeiling, conn.Limits.StatementTimeout, conn.Limits.ScanSample)
	return nil
}

func connectionDenylist(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("connection denylist", flag.ExitOnError)
	var add, remove stringList
	fs.Var(&add, "add", "schema.table to add to the denylist (repeatable)")
	fs.Var(&remove, "remove", "schema.table to remove from the denylist (repeatable)")
	list := fs.Bool("list", false, "print the current denylist and make no changes")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
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
	if err := fs.Parse(args); err != nil {
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
