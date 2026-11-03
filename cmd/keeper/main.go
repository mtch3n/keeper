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
	case "connect":
		return runConnect(rest)
	case "host":
		return runHost(rest)
	case "connection":
		return runConnection(rest)
	case "catalog":
		return runCatalog(rest)
	case "approve":
		return runApprove(rest)
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
	case "hook":
		return runHook(rest)
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
  keeper connect [--name N]
      reads a PostgreSQL connection string at a hidden prompt, or one line from stdin
  keeper host ls
  keeper host rm <name>
  keeper connection ls
  keeper connection show <name>
  keeper connection set <name> [--reads always|uncertain|never] [--detection kind[:entity+entity][@raw],...|off] [--writes off|ask|allow] [--persistent-tokens on|off] [--max-rows N] [--max-bytes N] [--timeout D] [--scan-sample N]
  keeper connection terms <name> [--deny T]... [--allow T]... [--regex label=expr]...
  keeper connection test <name>
  keeper connection rm <name>
  keeper catalog init <name> [--sample N]
  keeper catalog edit <name> <schema.table.column> --policy P [--namespace N] [--form F] [--hide-name]
  keeper catalog grants <name>
  keeper catalog ls <name> [--unclassified]
  keeper approve [<ticket>]
  keeper activity [--session S] [--connection C] [--since D] [--limit N]
  keeper doctor
  keeper update [--check] [--force]
  keeper vault export > file | import [--replace] file | rotate-master
  keeper daemon start | restart | status
  keeper ui

Every command accepts --json for machine-readable output.
`)
}
