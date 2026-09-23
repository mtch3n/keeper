package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"iter"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/mtchen/keeper/internal/client"
	"github.com/mtchen/keeper/internal/daemonapp"
	"github.com/mtchen/keeper/internal/types"
)

// ---------------------------------------------------------------- catalog ---

func runCatalog(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("catalog: expected a subcommand (init, ls, edit, grants)")
	}
	ctx := context.Background()
	sub, rest := args[0], args[1:]
	switch sub {
	case "init":
		return catalogInit(ctx, rest)
	case "ls":
		return catalogLs(ctx, rest)
	case "edit":
		return catalogEdit(ctx, rest)
	case "grants":
		return catalogGrants(ctx, rest)
	default:
		return fmt.Errorf("catalog: unknown subcommand %q", sub)
	}
}

func catalogInit(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("catalog init", flag.ContinueOnError)
	sample := fs.Int("sample", 0, "rows to sample per column; 0 skips sampling")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("catalog init: expected a connection name")
	}
	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	// The catalog routes are keyed by id; the command line takes a name.
	id, err := resolveConnectionID(ctx, cli, fs.Arg(0))
	if err != nil {
		return err
	}
	prop, err := cli.CatalogInit(ctx, id, *sample)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(prop)
	}

	// The grouping is the point. "Typed scalars → allow" and "name heuristic →
	// token" are safe to accept without reading; text with no heuristic match is
	// the review task, because that is where unnamed name and address columns
	// live and the rule pass cannot find them (SPEC R5.3).
	fmt.Printf("safe to accept in bulk: %d column(s)\n", len(prop.SafeToBulkAccept))
	for key, p := range sortedEntries(prop.SafeToBulkAccept) {
		fmt.Printf("  %-50s %s%s\n", key, p.Policy, policyArgs(p))
	}
	fmt.Printf("\nneeds review: %d column(s)\n", len(prop.NeedsReview))
	fmt.Println("  these are free-text columns no rule matched. A name or an address here")
	fmt.Println("  is invisible to the pattern pass, so a human decides each one.")
	for key, p := range sortedEntries(prop.NeedsReview) {
		rate := ""
		if r, ok := prop.SampleRates[key]; ok && r > 0 {
			rate = fmt.Sprintf("  (%.0f%% of sampled rows matched a rule)", r*100)
		}
		fmt.Printf("  %-50s %s%s%s\n", key, p.Policy, policyArgs(p), rate)
	}
	fmt.Println("\nreview and commit .keeper/catalog.yaml, then `keeper catalog edit` to change one.")
	return nil
}

func catalogLs(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("catalog ls", flag.ContinueOnError)
	only := fs.Bool("unclassified", false, "list only the backlog")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("catalog ls: expected a connection name")
	}
	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	// The catalog routes are keyed by id; the command line takes a name.
	id, err := resolveConnectionID(ctx, cli, fs.Arg(0))
	if err != nil {
		return err
	}
	cat, err := cli.GetCatalog(ctx, id)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(cat)
	}
	if *only {
		for _, k := range cat.Unclassified {
			fmt.Println(k)
		}
		fmt.Printf("\n%d unclassified column(s)\n", len(cat.Unclassified))
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "COLUMN\tPOLICY\tARGS")
	for key, p := range sortedEntries(cat.Entries) {
		fmt.Fprintf(w, "%s\t%s\t%s\n", key, p.Policy, strings.TrimSpace(policyArgs(p)))
	}
	w.Flush()
	fmt.Printf("\n%d classified, %d unclassified\n", len(cat.Entries), len(cat.Unclassified))
	return nil
}

func catalogEdit(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("catalog edit", flag.ContinueOnError)
	policy := fs.String("policy", "", "allow, scan, partial, token, redact or drop")
	namespace := fs.String("namespace", "", "token namespace; mandatory for token (R5.2b)")
	form := fs.String("form", "", "partial form; mandatory for partial (R5.2d)")
	hideName := fs.Bool("hide-name", false, "suppress the column name on every response surface")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("catalog edit: expected a connection and schema.table.column")
	}
	if *policy == "" {
		return fmt.Errorf("catalog edit: --policy is required")
	}
	entry := types.ColumnPolicy{
		Policy:    types.Policy(*policy),
		Namespace: *namespace,
		Form:      types.PartialForm(*form),
		HideName:  *hideName,
	}
	// Validate before the round trip so the error names the field rather than
	// arriving as a 400 from the other end.
	if err := entry.Validate(); err != nil {
		return err
	}

	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	// The catalog routes are keyed by id; the command line takes a name.
	id, err := resolveConnectionID(ctx, cli, fs.Arg(0))
	if err != nil {
		return err
	}
	res, err := cli.PutCatalogColumns(ctx, id, map[string]types.ColumnPolicy{fs.Arg(1): entry})
	if err != nil {
		return err
	}
	fmt.Printf("%s → %s%s\n", fs.Arg(1), entry.Policy, policyArgs(entry))
	fmt.Printf("%d unclassified column(s) remain\n", len(res.Unclassified))
	return nil
}

func catalogGrants(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("catalog grants", flag.ContinueOnError)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("catalog grants: expected a connection name")
	}
	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	// The catalog routes are keyed by id; the command line takes a name.
	id, err := resolveConnectionID(ctx, cli, fs.Arg(0))
	if err != nil {
		return err
	}
	stmts, err := cli.CatalogGrants(ctx, id)
	if err != nil {
		return err
	}
	// §2.4: for a column the agent never needs, the correct control is not
	// keeper. These statements move those columns out of keeper's reach
	// entirely, and survive keeper being wrong about everything else.
	fmt.Println("-- applying these revokes every `drop` column from the role's reach.")
	fmt.Println("-- `SELECT *` then fails for the whole statement rather than omitting the column;")
	fmt.Println("-- get_schema already reports only what the role may read (SPEC R5.5).")
	for _, s := range stmts {
		fmt.Println(s)
	}
	return nil
}

// --------------------------------------------------------------- approve ---

func runApprove(args []string) error {
	fs := flag.NewFlagSet("approve", flag.ContinueOnError)
	refuse := fs.Bool("refuse", false, "refuse instead of approving")
	standing := fs.Bool("standing", false, "also create a standing allow rule for the paths")
	session := fs.Bool("session", false, "also create a session allow rule for the paths")
	ceiling := fs.Int("row-ceiling", 0, "row ceiling for the allow rule")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ctx := context.Background()
	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	queue, err := cli.ListApprovals(ctx)
	if err != nil {
		return err
	}
	if fs.NArg() == 0 {
		if *asJSON {
			return printJSON(queue)
		}
		return printQueue(queue)
	}

	// SPEC §3.4's stated limitation: this raises the bar and does not close the
	// gap. An agent with shell access runs as the same OS user and can reach the
	// same daemon; "not exposed as an MCP tool" is enforceable, "cannot originate
	// from the agent" is not. Closing it properly needs a separate OS user or an
	// out-of-band confirmation.
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("approve: refusing to decide without a terminal on stdin")
	}

	ticket := fs.Arg(0)
	item, ok := findTicket(queue, ticket)
	if !ok {
		return fmt.Errorf("approve: no queued ticket %q", ticket)
	}
	printApproval(item)

	decision := client.DecisionApprove
	if *refuse {
		decision = client.DecisionRefuse
	}
	verb := "Approve"
	if *refuse {
		verb = "Refuse"
	}
	if !confirm(fmt.Sprintf("%s this statement?", verb)) {
		fmt.Println("nothing decided")
		return nil
	}

	var grant *client.GrantParams
	if *standing || *session {
		if *ceiling <= 0 {
			// §9.3: identical relations with a different WHERE can return three
			// orders of magnitude more rows, so the ceiling is not optional.
			return fmt.Errorf("approve: --row-ceiling is required with --standing or --session")
		}
		life := types.GrantSession
		if *standing {
			life = types.GrantStanding
		}
		grant = &client.GrantParams{Lifetime: life, RowCeiling: *ceiling}
	}

	if err := cli.DecideApproval(ctx, ticket, decision, grant); err != nil {
		return err
	}
	fmt.Printf("%sd %s\n", strings.ToLower(verb), ticket)
	if grant != nil {
		fmt.Printf("allow rule created: %s, ceiling %d rows\n", grant.Lifetime, grant.RowCeiling)
	}
	return nil
}

func printQueue(queue []types.ApprovalItem) error {
	if len(queue) == 0 {
		fmt.Println("nothing is waiting")
		return nil
	}
	// R9.1: oldest first, and every row names its origin. Two agents on the same
	// database with similar SQL are otherwise indistinguishable, which is the
	// normal case with three windows open.
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TICKET\tAGENT\tWORKSPACE\tINTENT\tCONNECTION\tTIER\tAGE")
	for _, it := range queue {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n",
			it.TicketID, it.Session.Client.Name, it.Session.Client.Workspace,
			truncate(it.Session.Intent, 40), it.Connection, it.Tier, age(it.CreatedAt))
	}
	w.Flush()
	fmt.Println("\nkeeper approve <ticket> to decide one.")
	return nil
}

// printApproval is §9.2's layout: facts first, SQL last. A human cannot tell
// from the SQL alone what the result contains.
func printApproval(it types.ApprovalItem) {
	f := it.Facts
	fmt.Printf("intent    %s\n", f.Intent)
	fmt.Printf("agent     %s · %s\n", it.Session.Client.Name, it.Session.Client.Workspace)
	if it.Write != nil {
		fmt.Printf("impact    %s · %d rows as of the preview, %s ago · %s\n",
			it.Write.Operation, it.Write.RowCount, age(it.Write.PreviewedAt), relations(f.Relations))
		fmt.Println("          the count is not a lock; the executed count is reported back")
	} else {
		fmt.Printf("impact    %d rows · %s\n", f.EstimatedRows, relations(f.Relations))
	}
	if len(f.Egress) > 0 {
		fmt.Printf("egress    %s\n", strings.Join(f.Egress, ", "))
	}
	fmt.Printf("cost      est. %.0f\n", f.EstimatedCost)
	if len(f.Reasons) > 0 {
		fmt.Printf("why       %s\n", strings.Join(f.Reasons, ", "))
	}
	fmt.Println(strings.Repeat("-", 60))
	fmt.Printf("SQL       %s\n", renderSQL(it.SQL))
	if it.Write != nil {
		fmt.Println()
		fmt.Println("Approving executes a change to stored data. Revoking this approval")
		fmt.Println("afterwards stops future writes and restores nothing.")
	}
	fmt.Println()
}

// ----------------------------------------------------------------- allow ---

func runAllow(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("allow: expected a subcommand (ls, revoke)")
	}
	ctx := context.Background()
	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	switch args[0] {
	case "ls":
		fs := flag.NewFlagSet("allow ls", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "emit JSON")
		if err := parseFlags(fs, args[1:]); err != nil {
			return err
		}
		grants, err := cli.ListGrants(ctx)
		if err != nil {
			return err
		}
		if *asJSON {
			return printJSON(grants)
		}
		if len(grants) == 0 {
			fmt.Println("no allow rules")
			return nil
		}
		// last used and uses are the point of the listing: an entry granted in
		// March and never used since is the one to revoke, and nothing else in
		// the product can tell you that.
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tPATH\tLIFETIME\tCEILING\tGRANTED\tLAST USED\tUSES")
		for _, g := range grants {
			last := "never"
			if !g.LastUsedAt.IsZero() {
				last = age(g.LastUsedAt) + " ago"
			}
			life := string(g.Lifetime)
			if g.Suspended {
				life += " (suspended)"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s ago\t%s\t%d\n",
				g.ID, g.Path, life, g.RowCeiling, age(g.CreatedAt), last, g.Uses)
		}
		w.Flush()
		fmt.Println("\nA rule covers exactly the path it names: no wildcards, and a view is")
		fmt.Println("its own path (SPEC R9.3c).")
		return nil

	case "revoke":
		if len(args) != 2 {
			return fmt.Errorf("allow revoke: expected one rule id")
		}
		if err := cli.RevokeGrant(ctx, args[1]); err != nil {
			return err
		}
		fmt.Printf("revoked %s\n", args[1])
		return nil

	default:
		return fmt.Errorf("allow: unknown subcommand %q", args[0])
	}
}

// -------------------------------------------------------------- activity ---

func runActivity(args []string) error {
	fs := flag.NewFlagSet("activity", flag.ContinueOnError)
	session := fs.String("session", "", "filter by session id")
	conn := fs.String("connection", "", "filter by connection")
	tier := fs.Int("tier", -1, "filter by tier")
	since := fs.Duration("since", 0, "how far back to look, e.g. 24h")
	limit := fs.Int("limit", 50, "maximum records")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ctx := context.Background()
	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	if fs.NArg() == 1 {
		rec, err := cli.GetActivityRecord(ctx, fs.Arg(0))
		if err != nil {
			return err
		}
		return printJSON(rec)
	}

	f := client.ActivityFilter{SessionID: *session, Limit: *limit}
	if *conn != "" {
		// Records carry the connection's id. A name passed through unresolved
		// matched nothing and printed an empty log.
		if f.ConnectionID, err = resolveConnectionID(ctx, cli, *conn); err != nil {
			return err
		}
	}
	if *tier >= 0 {
		t := types.Tier(*tier)
		f.Tier = &t
	}
	if *since > 0 {
		f.Since = time.Now().Add(-*since)
	}
	recs, err := cli.ListActivity(ctx, f)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(recs)
	}
	if len(recs) == 0 {
		fmt.Println("no activity")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "WHEN\tCONNECTION\tTIER\tROWS\tPOLICIES APPLIED\tSTATEMENT")
	for _, r := range recs {
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%s\t%s\n",
			r.At.Format(time.RFC3339), r.Connection, r.Tier, r.RowCount,
			transformSummary(r.Transforms), truncate(renderSQL(r.Statement), 60))
	}
	w.Flush()
	// R10d: the log records what keeper intended to emit. Saying "values masked"
	// here would claim something the log cannot establish.
	fmt.Println("\nPolicies applied, not bytes verified: the log records the decision,")
	fmt.Println("not proof that redaction ran correctly (SPEC R10d).")
	fmt.Println("Literals are never stored, so the statement shown is the statement as kept.")
	return nil
}

// --------------------------------------------------------------- version ---

// runVersion prints both versions, always, whether or not they agree.
//
// One number answers the wrong question. The daemon and its clients refuse each
// other across a mismatch (SPEC §3.4), so "what version is keeper" is really two
// questions — what is this binary, and what is the process it is talking to —
// and a single line cannot tell you which one you are looking at. Printing both
// makes a skew visible before it becomes a confusing refusal.
func runVersion(args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ctx := context.Background()

	out := struct {
		Client string `json:"client"`
		Daemon string `json:"daemon,omitzero"`
		Match  bool   `json:"match"`
		Reason string `json:"reason,omitzero"`
	}{Client: client.Version}

	// dialOnly, not connectDaemon: asking what version is running must not start
	// something in order to answer.
	if cli, err := dialOnly(ctx, client.DefaultSocketPath()); err == nil {
		defer cli.Close()
		out.Daemon = cli.DaemonVersion()
		out.Match = out.Daemon == out.Client
	} else {
		var mismatch *client.VersionMismatchError
		if errors.As(err, &mismatch) {
			// The handshake refused precisely because they differ, and it knows
			// both numbers. That is the answer, not a failure to get one.
			out.Daemon = mismatch.DaemonVersion
			out.Reason = "the daemon refused this client over the difference"
		} else {
			out.Reason = err.Error()
		}
	}

	if *asJSON {
		return printJSON(out)
	}
	fmt.Printf("client   %s\n", out.Client)
	if out.Daemon == "" {
		fmt.Printf("daemon   not reachable (%s)\n", out.Reason)
		return nil
	}
	fmt.Printf("daemon   %s\n", out.Daemon)
	if !out.Match {
		fmt.Println()
		fmt.Println("These differ, so they will refuse each other. `keeper daemon restart`")
		fmt.Println("replaces the running daemon — never automatic, because it cancels every")
		fmt.Println("other window's pending tickets and permanently invalidates every token")
		fmt.Println("those sessions hold (SPEC R3.4d).")
	}
	return nil
}

// ---------------------------------------------------------------- doctor ---

func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ctx := context.Background()

	// doctor is what you run when the daemon will not start, so it never starts
	// one. A daemonless report is still a report.
	sock := client.DefaultSocketPath()
	cli, err := dialOnly(ctx, sock)
	if err != nil {
		return daemonlessDoctor(sock, err, *asJSON)
	}
	defer cli.Close()

	rep, err := cli.GetDoctor(ctx)
	if err != nil {
		// The handshake succeeded, so the daemon is up. Reporting this as the
		// daemonless case pointed at a stale socket and sent people after the
		// one thing that was working.
		return fmt.Errorf("doctor: the daemon is running but its report failed: %w", err)
	}
	// The daemon does not report its own liveness in the body — reaching this
	// line is the report. Without this the JSON form said `"daemon_running":
	// false` beside a full report from a running daemon, which is the one
	// field a script would branch on.
	rep.DaemonRunning = true
	if *asJSON {
		return printJSON(rep)
	}
	fmt.Printf("daemon        running\n")
	fmt.Printf("version       daemon %s · cli %s%s\n", rep.Version, client.Version, versionNote(rep.Version))
	fmt.Printf("socket        %s\n", sock)
	// R4.3: silent degradation to a weaker key source is a defect, so the source
	// in use is always named. There is no locked state to report beside it —
	// keeperd opens the vault before it serves and exits if it cannot, so a
	// daemon that answered this call has an open vault by construction.
	fmt.Printf("vault         open, key source %s\n", rep.KeySource)
	if rep.Judge.Configured {
		state := "unreachable"
		if rep.Judge.Available {
			state = "available"
		}
		fmt.Printf("judge         %s %s\n", rep.Judge.Identity, state)
	}
	if rep.Detector != nil && rep.Detector.Name != "" {
		// R8.5g: "what was examining my data, and could it talk to anyone" has
		// to be answerable after the fact, so it is answerable now.
		fmt.Printf("detector      %s %s, network %s\n", rep.Detector.Name, rep.Detector.Version, rep.Detector.NetworkPosture)
	}
	for _, c := range rep.Connections {
		state := "ok"
		if c.FreshKnown && !c.CatalogFresh {
			state += " · catalog is stale"
		} else if !c.FreshKnown {
			state += " · catalog freshness unknown"
		}
		fmt.Printf("connection    %-20s %s\n", c.Name, state)
		if c.Unclassified > 0 {
			fmt.Printf("              %d unclassified column(s) — `keeper catalog ls %s --unclassified`\n", c.Unclassified, c.Name)
		}
		if c.Findings > 0 {
			// A pointer, not a verdict: the findings do not stop this
			// connection, and doctor reporting them as a fault would be
			// the acceptance gate wearing a different hat (SPEC R4.1).
			fmt.Printf("              %d privilege finding(s) — `keeper audit %s`\n", c.Findings, c.Name)
		}
	}
	return nil
}

// daemonlessDoctor reports what can be established without the daemon. It checks
// that the keychain item exists and never reads it: reading the master key would
// open the vault, which only keeperd may do (R3.1).
func daemonlessDoctor(sock string, dialErr error, asJSON bool) error {
	dir, _ := configDir()
	report := struct {
		DaemonRunning bool   `json:"daemon_running"`
		Reason        string `json:"reason"`
		Socket        string `json:"socket"`
		SocketPresent bool   `json:"socket_present"`
		ConfigDir     string `json:"config_dir"`
		VaultPresent  bool   `json:"vault_present"`
		CLIVersion    string `json:"cli_version"`
	}{
		Reason:     dialErr.Error(),
		Socket:     sock,
		ConfigDir:  dir,
		CLIVersion: client.Version,
	}
	if _, err := os.Stat(sock); err == nil {
		report.SocketPresent = true
	}
	if dir != "" {
		if _, err := os.Stat(dir + "/vault.age"); err == nil {
			report.VaultPresent = true
		}
	}
	if asJSON {
		return printJSON(report)
	}
	fmt.Println("daemon        not reachable")
	fmt.Printf("reason        %s\n", report.Reason)
	fmt.Printf("socket        %s (%s)\n", sock, presence(report.SocketPresent))
	fmt.Printf("config        %s\n", dir)
	fmt.Printf("vault file    %s\n", presence(report.VaultPresent))
	fmt.Printf("version       cli %s · daemon unknown\n", client.Version)
	fmt.Println()
	fmt.Println("A present socket with an unreachable daemon is a stale socket; `keeper daemon")
	fmt.Println("start` elects one under a lock and cleans it up. Never delete it by hand: a")
	fmt.Println("socket refuses connections between bind and listen, so a live daemon looks")
	fmt.Println("exactly like a dead one for that window (SPEC R3.4c).")
	return nil
}

// ----------------------------------------------------------------- vault ---

func runVault(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("vault: expected a subcommand (export, rotate-master)")
	}
	ctx := context.Background()
	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	switch args[0] {
	case "export":
		out, err := cli.ExportVault(ctx)
		if err != nil {
			return err
		}
		// Losing the keychain item without an export loses every connection.
		return printJSON(out)

	case "rotate-master":
		if err := cli.RotateMaster(ctx); err != nil {
			return err
		}
		fmt.Println("master key rotated")
		return nil

	default:
		return fmt.Errorf("vault: unknown subcommand %q", args[0])
	}
}

// ---------------------------------------------------------------- daemon ---

func runDaemon(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("daemon: expected a subcommand (serve, start, restart, status)")
	}
	ctx := context.Background()
	sock := client.DefaultSocketPath()

	switch args[0] {
	case "serve":
		// The daemon itself, in the foreground. `keeper daemon start` spawns
		// this; systemd or a terminal runs it directly.
		if code := daemonapp.Run(args[1:]); code != 0 {
			return fmt.Errorf("keeperd exited with status %d", code)
		}
		return nil

	case "start":
		if err := client.StartDaemon(ctx, sock); err != nil {
			return err
		}
		fmt.Printf("daemon running on %s\n", sock)
		return nil

	case "status":
		cli, err := dialOnly(ctx, sock)
		if err != nil {
			fmt.Printf("not running (%v)\n", err)
			return nil
		}
		defer cli.Close()
		// No session id to print: the CLI dials as a human client, which is how
		// the daemon tells it from an agent (SPEC §6.3).
		fmt.Printf("running, daemon %s\n", cli.DaemonVersion())
		return nil

	case "restart":
		// A restart invalidates every token every agent is holding: the reverse
		// map is memory and HMAC is one-way, so the tokens in their contexts
		// become permanently unresolvable and the queries that minted them have
		// to be re-run (SPEC R3.4d). That is why nothing does this automatically.
		if term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Println("Restarting cancels every pending ticket and permanently invalidates")
			fmt.Println("every token any agent is currently holding. They will have to re-run")
			fmt.Println("the queries that produced them.")
			if !confirm("Restart the daemon?") {
				fmt.Println("not restarted")
				return nil
			}
		}
		if err := client.RestartDaemon(ctx, sock); err != nil {
			return err
		}
		fmt.Println("daemon restarted")
		return nil

	default:
		return fmt.Errorf("daemon: unknown subcommand %q", args[0])
	}
}

// -------------------------------------------------------------------- ui ---

func runUI(args []string) error {
	fs := flag.NewFlagSet("ui", flag.ContinueOnError)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ctx := context.Background()
	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	rep, err := cli.GetDoctor(ctx)
	if err != nil {
		return err
	}
	if rep.UIBase == "" {
		return fmt.Errorf("ui: the daemon is not serving a UI")
	}
	fmt.Println(rep.UIBase)
	return nil
}

// ---------------------------------------------------------------- shared ---

// renderSQL makes a trojan-source payload visible rather than letting the
// terminal act on it. SPEC R9.2: bidirectional overrides render as their code
// point, so a statement cannot read one way and run another.
func renderSQL(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '‪', '‫', '‬', '‭', '‮',
			'⁦', '⁧', '⁨', '⁩', '‏', '‎':
			fmt.Fprintf(&b, "[U+%04X]", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func policyArgs(p types.ColumnPolicy) string {
	var parts []string
	if p.Namespace != "" {
		parts = append(parts, "namespace="+p.Namespace)
	}
	if p.Form != "" {
		parts = append(parts, "form="+string(p.Form))
	}
	if p.HideName {
		parts = append(parts, "hide_name")
	}
	if len(parts) == 0 {
		return ""
	}
	return "  " + strings.Join(parts, " ")
}

func transformSummary(m map[string]types.Transform) string {
	if len(m) == 0 {
		return "-"
	}
	counts := map[types.Policy]int{}
	for _, t := range m {
		if t.Policy != types.PolicyAllow {
			counts[t.Policy]++
		}
	}
	if len(counts) == 0 {
		return "-"
	}
	var parts []string
	for _, p := range []types.Policy{types.PolicyToken, types.PolicyPartial, types.PolicyRedact, types.PolicyDrop, types.PolicyScan} {
		if n := counts[p]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, p))
		}
	}
	return strings.Join(parts, " ")
}

func relations(rels []types.RelationRef) string {
	var s []string
	for _, r := range rels {
		s = append(s, r.String())
	}
	if len(s) == 0 {
		return "(none)"
	}
	return strings.Join(s, ", ")
}

func findTicket(queue []types.ApprovalItem, id string) (types.ApprovalItem, bool) {
	for _, it := range queue {
		if it.TicketID == id {
			return it, true
		}
	}
	return types.ApprovalItem{}, false
}

func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

func age(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + "d"
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// sortedEntries iterates a catalog map in a stable order, so two runs of the
// same command produce the same output and a diff of them means something.
func sortedEntries(m map[string]types.ColumnPolicy) iter.Seq2[string, types.ColumnPolicy] {
	keys := slices.Sorted(maps.Keys(m))
	return func(yield func(string, types.ColumnPolicy) bool) {
		for _, k := range keys {
			if !yield(k, m[k]) {
				return
			}
		}
	}
}

// configDir mirrors keeperd's, for the daemonless report. It creates nothing:
// doctor looks, it does not arrange.
func configDir() (string, error) {
	if v := os.Getenv("KEEPER_HOME"); v != "" {
		return v, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "keeper"), nil
}

// versionNote says so when the two disagree, rather than leaving a reader to
// compare two strings on the same line and notice.
func versionNote(daemon string) string {
	if daemon == "" || daemon == client.Version {
		return ""
	}
	return "  ← these differ; they will refuse each other"
}

func presence(ok bool) string {
	if ok {
		return "present"
	}
	return "absent"
}
