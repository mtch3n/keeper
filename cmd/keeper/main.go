// Command keeper is the operator CLI: connection registration and G0
// acceptance, catalog editing, approvals, allow-rule management, activity
// review, doctor, vault control and daemon lifecycle. It is the only place
// (with the web UI) any of that surface is reachable — SPEC §6.3 keeps all
// of it off the MCP tool list.
package main

import (
	"fmt"
	"os"

	"github.com/mtchen/keeper/internal/mcpapp"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "keeper:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return fmt.Errorf("no command given")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "connection":
		return runConnection(rest)
	case "catalog":
		return runCatalog(rest)
	case "approve":
		return runApprove(rest)
	case "allow":
		return runAllow(rest)
	case "activity":
		return runActivity(rest)
	case "doctor":
		return runDoctor(rest)
	case "vault":
		return runVault(rest)
	case "daemon":
		return runDaemon(rest)
	case "ui":
		return runUI(rest)
	case "version":
		return runVersion(rest)
	case "update":
		return runUpdate(rest)
	case "mcp":
		// The MCP server, on stdio. Nothing else may write to stdout from here
		// on: the transport is the stream.
		return mcpapp.Run()
	case "-h", "--help", "help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `keeper — operate a keeper daemon and its registered connections

Usage:
  keeper connection add --name X --dsn ... [--write-dsn ...] [--catalog path]
  keeper connection ls
  keeper connection show <name>
  keeper connection audit <name>
  keeper connection accept <name> --finding ID [--finding ID ...] | --accept-all
  keeper connection set <name> [--mode ...] [--max-rows N] [--timeout D] [--scan-sample N]
  keeper connection denylist <name> [--add schema.table] [--remove schema.table] [--list]
  keeper connection rm <name>
  keeper catalog init <name> [--sample N]
  keeper catalog edit <name> <schema.table.column> --policy P [--namespace N] [--form F] [--hide-name]
  keeper catalog grants <name>
  keeper catalog ls <name> [--unclassified]
  keeper approve [<ticket>]
  keeper allow ls | keeper allow revoke <id>
  keeper activity [--session S] [--connection C] [--tier N] [--since D] [--limit N]
  keeper doctor
  keeper update [--check] [--force]
  keeper vault unlock | export | rotate-master
  keeper daemon start | restart | status
  keeper ui

Every command accepts --json for machine-readable output.
`)
}
