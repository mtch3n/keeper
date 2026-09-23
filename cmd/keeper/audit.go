package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/mtchen/keeper/internal/client"
)

// runAudit is `keeper audit`: the privilege audit as its own surface. It was
// once the tail of `connection add`, where it read as a gate to get past on
// the way to a working connection. It is not one — every finding here is the
// database's answer about what this role can do, and the only thing that
// changes it is a statement run against the database. Separating the report
// from registration is what lets it be read that way. SPEC R4.1.
func runAudit(args []string) error {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "JSON output")
	rerun := fs.Bool("rerun", false, "re-run G0 against the named connection before reporting")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	name := fs.Arg(0)
	if *rerun && name == "" {
		return fmt.Errorf("audit: --rerun needs a connection name")
	}

	ctx := context.Background()
	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	if *rerun {
		id, err := resolveConnectionID(ctx, cli, name)
		if err != nil {
			return err
		}
		if _, err := cli.AuditConnection(ctx, id); err != nil {
			return err
		}
	}

	reports, err := cli.Audit(ctx)
	if err != nil {
		return err
	}
	if name != "" {
		reports = matching(reports, name)
		if len(reports) == 0 {
			return fmt.Errorf("audit: no connection named %q", name)
		}
	}
	if *jsonOut {
		return printJSON(reports)
	}
	printAudit(reports)
	return nil
}

func matching(reports []client.AuditReport, name string) []client.AuditReport {
	var out []client.AuditReport
	for _, r := range reports {
		if strings.EqualFold(r.Name, name) || r.ConnectionID == name {
			out = append(out, r)
		}
	}
	return out
}

func printAudit(reports []client.AuditReport) {
	if len(reports) == 0 {
		fmt.Println("no connections registered")
		return
	}
	total := 0
	for _, r := range reports {
		total += len(r.Findings)
		fmt.Printf("%s  (%s as %s)\n", r.Name, r.Database, r.Role)
		if r.AuditedAt.IsZero() {
			fmt.Println("  never audited — `keeper audit " + r.Name + " --rerun`")
			continue
		}
		fmt.Printf("  audited %s ago\n", age(r.AuditedAt))
		if len(r.Findings) == 0 {
			fmt.Println("  nothing to report: this role holds no privilege keeper would flag")
			continue
		}
		printFindings(r.Findings, "  ")
		fmt.Println()
	}
	if total > 0 {
		// The statements are printed to be copied into psql. keeper does not
		// run them: it holds a credential whose privileges are the subject of
		// the report, and a tool that narrows its own grants is a tool that
		// can widen them.
		fmt.Printf("%d finding(s). Each `fix` is a statement to run on the database yourself;\n", total)
		fmt.Println("keeper does not run them. Nothing here stops a connection from working.")
	}
}
