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
		return fmt.Errorf("connection: expected a subcommand (ls, show, set, terms, rm)")
	}
	ctx := context.Background()
	sub, rest := args[0], args[1:]
	switch sub {
	case "ls":
		return connectionLs(ctx, rest)
	case "show":
		return connectionShow(ctx, rest)
	case "set":
		return connectionSet(ctx, rest)
	case "terms":
		return connectionTerms(ctx, rest)
	case "rm":
		return connectionRemove(ctx, rest)
	case "test":
		return connectionTest(ctx, rest)
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
	fmt.Fprintln(w, "NAME\tHOST\tADDRESS\tDATABASE\tUSERNAME\tREADS\tWRITES")
	for _, c := range conns {
		fmt.Fprintf(w, "%s\t%s\t%s:%d\t%s\t%s\t%s\t%s\n", c.Name, c.Host, c.Address, c.Port, c.Database, c.Username, c.Reads, c.Writes)
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
	fmt.Printf("reads:     %s\n", detail.Reads)
	fmt.Printf("writes:    %s\n", detail.Writes)
	fmt.Printf("detection: %s\n", stageList(detail.Detection))
	fmt.Printf("catalog:   %s\n", detail.CatalogStatus.Path)
	if detail.CatalogStatus.Unclassified > 0 {
		fmt.Printf("           %d unclassified column(s)\n", detail.CatalogStatus.Unclassified)
	}
	return nil
}

func connectionSet(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("connection set", flag.ExitOnError)
	reads := fs.String("reads", "", "always | uncertain | never: when this profile's reads ask a person first")
	maxRows := fs.Int("max-rows", 0, "operator ceiling for query(max_rows); the agent cannot raise it")
	maxBytes := fs.Int("max-bytes", 0, "size cap on a result's row data, in bytes")
	timeout := fs.Duration("timeout", 0, "SET LOCAL statement_timeout for every statement")
	scanSample := fs.Int("scan-sample", 0, "sample size catalog init examines per column")
	detection := fs.String("detection", "", "detection stages in order, comma-separated, or off: kind[:entity+entity][@raw], kinds patterns and list")
	writes := fs.String("writes", "", "off | ask | allow: whether this profile's sessions may write, and whether a person accepts each write")
	persistent := fs.String("persistent-tokens", "", "on | off: whether an agent declaring persistent scope gets tokens that outlive its session")
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	name := fs.Arg(0)
	if name == "" {
		return fmt.Errorf("connection set: expected a connection name")
	}
	if *reads != "" && !types.Reads(*reads).Valid() {
		return fmt.Errorf("connection set: --reads must be always, uncertain or never")
	}
	if *writes != "" && !types.Writes(*writes).Valid() {
		return fmt.Errorf("connection set: --writes must be off, ask or allow")
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
	params := client.PatchConnectionParams{Reads: types.Reads(*reads), Writes: types.Writes(*writes)}
	switch *persistent {
	case "":
	case "on", "off":
		on := *persistent == "on"
		params.PersistentTokens = &on
	default:
		return fmt.Errorf("connection set: --persistent-tokens must be on or off")
	}
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
	fmt.Printf("%q updated: reads=%s writes=%s detection=%s max_rows_ceiling=%d max_bytes=%d statement_timeout=%s scan_sample=%d\n",
		conn.Name, conn.Reads, conn.Writes, stageList(conn.Detection), conn.Limits.MaxRowsCeiling, conn.Limits.MaxBytes, conn.Limits.StatementTimeout, conn.Limits.ScanSample)
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
		// Removing takes the stored credential. The catalog file stays: it is in
		// the project repo, it is reviewed like code, and deleting it is not this
		// command's business.
		fmt.Printf("Remove %s? Its stored credential goes with it.\n", name)
		fmt.Printf("The catalog file on disk stays.\n")
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

// connectionTest logs in with a connection's credential once and says who the
// server says it is. keeper does not watch connections; this is how to ask.
func connectionTest(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("connection test", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.Arg(0) == "" {
		return fmt.Errorf("connection test: expected a connection name")
	}
	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()
	id, err := resolveConnectionID(ctx, cli, fs.Arg(0))
	if err != nil {
		return err
	}
	res, err := cli.TestConnection(ctx, id)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(res)
	}
	fmt.Printf("%q connected as %s · PostgreSQL %s · %s\n", fs.Arg(0), res.User, res.ServerVersion, time.Duration(res.RoundTrip).Round(time.Millisecond))
	return nil
}
