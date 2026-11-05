package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"iter"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"unicode"

	"golang.org/x/term"

	"github.com/mtchen/keeper/internal/client"
	"github.com/mtchen/keeper/internal/daemonapp"
	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// ---------------------------------------------------------------- catalog ---

func runCatalog(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("catalog: expected a subcommand (init, ls, edit)")
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

	printProposal(os.Stdout, fs.Arg(0), prop)
	return nil
}

// printProposal prints what catalog init proposed. Init writes nothing: the
// only way to apply a proposal is keeper catalog edit.
func printProposal(w io.Writer, name string, prop *ports.InitProposal) {
	// The grouping is the point. "Typed scalars → allow" and a column whose
	// samples the detector found PII in at the threshold rate are safe to accept
	// without reading; other text is the review task, because that is where
	// name and address columns live and pattern detection cannot find them.
	if len(prop.SafeToBulkAccept)+len(prop.NeedsReview) == 0 {
		fmt.Fprintln(w, "no proposals: this connection has detection off, or the detector could not examine its samples.")
		fmt.Fprintln(w, "classify columns with `keeper catalog edit`, or turn detection on with `keeper connection set --detection scan`.")
	}
	for _, d := range prop.Degradations {
		fmt.Fprintf(w, "degraded: %s — %s\n", d.Layer, d.Reason)
	}
	fmt.Fprintf(w, "safe to accept in bulk: %d column(s)\n", len(prop.SafeToBulkAccept))
	for key, p := range sortedEntries(prop.SafeToBulkAccept) {
		fmt.Fprintf(w, "  %-50s %s%s\n", key, p.Policy, policyArgs(p))
	}
	fmt.Fprintf(w, "\nneeds review: %d column(s)\n", len(prop.NeedsReview))
	fmt.Fprintln(w, "  these are free-text columns the detector did not match at the threshold rate. A name or")
	fmt.Fprintln(w, "  an address here is invisible to pattern detection, so a human decides each one.")
	for key, p := range sortedEntries(prop.NeedsReview) {
		rate := ""
		if r, ok := prop.SampleRates[key]; ok && r > 0 {
			rate = fmt.Sprintf("  (%.0f%% of sampled rows matched a rule)", r*100)
		}
		fmt.Fprintf(w, "  %-50s %s%s%s\n", key, p.Policy, policyArgs(p), rate)
	}
	fmt.Fprintf(w, "\nthis only proposed: nothing is changed yet. To apply one, run `keeper catalog edit %s <schema.table.column> --policy <policy>`.\n", name)
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
	policy := fs.String("policy", "", "allow, scan, token or redact")
	namespace := fs.String("namespace", "", "token namespace; mandatory for token")
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
	fmt.Printf("ui            %s\n", rep.UIBase)
	fmt.Printf("activity log  encrypted with the vault's key, kept %d day(s)\n", rep.LogRetentionDays)
	fmt.Printf("databases     connected only when needed; closed after %d idle minute(s)\n", rep.ConnectionIdleMinutes)
	if rep.Detector != nil && rep.Detector.Name != "" {
		// R8.5g: "what was examining my data, and could it talk to anyone" has
		// to be answerable after the fact, so it is answerable now.
		fmt.Printf("detector      %s %s, network %s\n", rep.Detector.Name, rep.Detector.Version, rep.Detector.NetworkPosture)
	}
	for _, c := range rep.Connections {
		fmt.Printf("connection    %s: reads %s, writes %s\n", c.Name, c.Reads, c.Writes)
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
		return fmt.Errorf("vault: expected a subcommand (export, import)")
	}
	ctx := context.Background()
	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	switch args[0] {
	case "export":
		// Losing the keychain item without an export loses every connection.
		in := newSecretReader()
		pass, err := in.read("passphrase to seal the export")
		if err != nil {
			return err
		}
		again, err := in.read("the passphrase again")
		if err != nil {
			return err
		}
		if pass == "" || pass != again {
			return fmt.Errorf("vault export: the passphrases are empty or differ")
		}
		out, err := cli.ExportVault(ctx, pass)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(out.Data)
		return err

	case "import":
		fs := flag.NewFlagSet("vault import", flag.ExitOnError)
		replace := fs.Bool("replace", false, "replace a vault that already holds connections")
		if err := parseFlags(fs, args[1:]); err != nil {
			return err
		}
		if fs.Arg(0) == "" {
			return fmt.Errorf("vault import: expected the export file")
		}
		data, err := os.ReadFile(fs.Arg(0))
		if err != nil {
			return err
		}
		conns, err := cli.ListConnections(ctx)
		if err != nil {
			return err
		}
		if len(conns) > 0 && !*replace {
			return fmt.Errorf("vault import: this vault holds %d connection(s); pass --replace to replace them with the export", len(conns))
		}
		pass, err := newSecretReader().read("passphrase the export was sealed with")
		if err != nil {
			return err
		}
		if err := cli.ImportVault(ctx, data, pass); err != nil {
			return err
		}
		fmt.Println("vault imported")
		return warnMissingCatalogs(ctx, cli)

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

	sess, err := cli.GetUISession(ctx)
	if err != nil {
		return err
	}
	fmt.Println(uiURL(sess, fs.Arg(0)))
	return nil
}

// uiURL is the link that opens the dashboard for the person who runs the
// command. The secret rides in the fragment, which no server log and no Referer
// ever holds; next, when given, is the page to land on.
func uiURL(s *client.UISession, next string) string {
	q := ""
	if strings.HasPrefix(next, "/") {
		q = "?next=" + url.QueryEscape(next)
	}
	return s.Base + "/auth" + q + "#" + s.Secret
}

// ---------------------------------------------------------------- shared ---

// renderSQL makes a trojan-source payload and terminal control bytes visible
// rather than letting the terminal act on them. SPEC R9.2: bidirectional
// overrides, an escape and the other control characters render as their code
// point, so a statement cannot read one way and run another or rewrite the
// screen. Newline and tab stay.
func renderSQL(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case unicode.IsControl(r), strings.ContainsRune("\u061c\u200e\u200f\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069", r):
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
	if len(parts) == 0 {
		return ""
	}
	return "  " + strings.Join(parts, " ")
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

// missingCatalogs names each profile whose catalog is a shared file that does
// not exist on this machine. An export carries the catalog of a profile that
// keeps it in keeper's state directory and leaves a shared path where it is, so
// on a new machine such a profile starts with no declared columns.
func missingCatalogs(details []*client.ConnectionDetail) []string {
	var out []string
	for _, d := range details {
		if p := d.CatalogStatus.Path; p != "" {
			if _, err := os.Stat(p); err != nil {
				out = append(out, d.Name+" ("+p+")")
			}
		}
	}
	return out
}

func warnMissingCatalogs(ctx context.Context, cli *client.Client) error {
	conns, err := cli.ListConnections(ctx)
	if err != nil {
		return err
	}
	var details []*client.ConnectionDetail
	for _, c := range conns {
		if d, err := cli.DescribeConnection(ctx, c.ID); err == nil {
			details = append(details, d)
		}
	}
	if missing := missingCatalogs(details); len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "warning: these profiles keep their catalog in a shared file that is absent here, so their declared columns are not applied until it is restored: %s\n", strings.Join(missing, ", "))
	}
	return nil
}
