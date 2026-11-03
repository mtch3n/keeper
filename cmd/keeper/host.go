package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"

	"github.com/mtchen/keeper/internal/client"
)

func runHost(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("host: expected a subcommand (ls, rm)")
	}
	ctx := context.Background()
	sub, rest := args[0], args[1:]
	switch sub {
	case "ls":
		return hostLs(ctx, rest)
	case "rm":
		return hostRemove(ctx, rest)
	default:
		return fmt.Errorf("host: unknown subcommand %q", sub)
	}
}

// resolveHostID turns a host name into its id, as resolveConnectionID does
// for connections.
func resolveHostID(ctx context.Context, cli *client.Client, name string) (string, error) {
	hosts, err := cli.ListHosts(ctx)
	if err != nil {
		return "", err
	}
	for _, h := range hosts {
		if h.Name == name {
			return h.ID, nil
		}
	}
	return "", fmt.Errorf("no host named %q (run `keeper host ls`)", name)
}

func hostLs(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("host ls", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	hosts, err := cli.ListHosts(ctx)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(hosts)
	}
	if len(hosts) == 0 {
		fmt.Println("no hosts registered")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tADDRESS\tPORT\tSSLMODE\tCONNECTIONS")
	for _, h := range hosts {
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%d\n", h.Name, h.Address, h.Port, h.SSLMode, len(h.Connections))
	}
	return w.Flush()
}

func hostRemove(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("host rm", flag.ExitOnError)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	name := fs.Arg(0)
	if name == "" {
		return fmt.Errorf("host rm: expected a host name")
	}

	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()

	id, err := resolveHostID(ctx, cli, name)
	if err != nil {
		return err
	}
	if err := cli.RemoveHost(ctx, id); err != nil {
		return err
	}
	fmt.Printf("host %q removed\n", name)
	return nil
}

// secretReader reads passwords: from the terminal with echo off, or one line
// at a time from stdin when stdin is not a terminal, so a script can pipe them.
type secretReader struct {
	tty   bool
	lines *bufio.Scanner
}

func newSecretReader() *secretReader {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		return &secretReader{tty: true}
	}
	return &secretReader{lines: bufio.NewScanner(os.Stdin)}
}

func (r *secretReader) read(prompt string) (string, error) {
	if r.tty {
		fmt.Fprintf(os.Stderr, "%s: ", prompt)
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", prompt, err)
		}
		return string(b), nil
	}
	if !r.lines.Scan() {
		if err := r.lines.Err(); err != nil {
			return "", fmt.Errorf("read %s: %w", prompt, err)
		}
		return "", fmt.Errorf("read %s: stdin ended; pipe one password per line", prompt)
	}
	return strings.TrimRight(r.lines.Text(), "\r"), nil
}
