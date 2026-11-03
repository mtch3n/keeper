package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/mtchen/keeper/internal/client"
)

// keeper hook <event> is what the plugin's hooks run. Each event reads the
// client's hook JSON on stdin and prints hook JSON on stdout, or nothing. A
// hook is guidance, not enforcement: every failure to reach the daemon or to
// read the input passes, and none of them starts the daemon.
func runHook(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: keeper hook session-start|pre-tool-use|waiting")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out any
	switch args[0] {
	case "session-start":
		out = sessionStartHook(ctx)
	case "pre-tool-use":
		data, _ := io.ReadAll(os.Stdin)
		var in hookInput
		if json.Unmarshal(data, &in) != nil {
			return nil
		}
		out = preToolUseHook(in, func() []string { return daemonHostAddresses(ctx) })
	case "waiting":
		out = waitingHook(ctx)
	default:
		return fmt.Errorf("unknown hook event %q", args[0])
	}
	if out == nil {
		return nil
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}

type hookInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	// TurnID is set by Codex only; Claude Code's hook input has no such field.
	TurnID string `json:"turn_id"`
}

func (in hookInput) isCodex() bool { return in.TurnID != "" }

type hookOutput struct {
	Specific hookSpecific `json:"hookSpecificOutput"`
}

type hookSpecific struct {
	EventName string `json:"hookEventName"`
	Context   string `json:"additionalContext,omitempty"`
	Decision  string `json:"permissionDecision,omitempty"`
	Reason    string `json:"permissionDecisionReason,omitempty"`
}

// keeperToolPrefix is how Claude Code names a plugin's MCP tool:
// mcp__plugin_<plugin>_<server>__<tool>.
const keeperToolPrefix = "mcp__plugin_keeper_keeper__"

func daemonClient(ctx context.Context) *client.Client {
	cli, err := dialOnly(ctx, client.DefaultSocketPath())
	if err != nil {
		return nil
	}
	return cli
}

func sessionStartHook(ctx context.Context) any {
	cli := daemonClient(ctx)
	if cli == nil {
		return nil
	}
	defer cli.Close()
	conns, err := cli.ListConnections(ctx)
	if err != nil || len(conns) == 0 {
		return nil
	}
	names := make([]string, len(conns))
	for i, c := range conns {
		names[i] = c.Name
	}
	return hookOutput{hookSpecific{
		EventName: "SessionStart",
		Context:   "Database work goes through keeper's MCP tools, which mask what comes back; do not use psql or a connection string. Profiles: " + strings.Join(names, ", ") + ".",
	}}
}

func daemonHostAddresses(ctx context.Context) []string {
	cli := daemonClient(ctx)
	if cli == nil {
		return nil
	}
	defer cli.Close()
	hosts, err := cli.ListHosts(ctx)
	if err != nil {
		return nil
	}
	addrs := make([]string, len(hosts))
	for i, h := range hosts {
		addrs[i] = h.Address
	}
	return addrs
}

func waitingHook(ctx context.Context) any {
	cli := daemonClient(ctx)
	if cli == nil {
		return nil
	}
	defer cli.Close()
	items, err := cli.ListApprovals(ctx)
	if err != nil {
		return nil
	}
	rep, err := cli.GetDoctor(ctx)
	if err != nil {
		return nil
	}
	return struct {
		Waiting int    `json:"waiting"`
		URL     string `json:"url"`
	}{len(items), rep.UIBase}
}

// preToolUseHook decides one tool call. registered is called only when the
// command runs a database client, so no other call touches the daemon.
func preToolUseHook(in hookInput, registered func() []string) any {
	if strings.HasPrefix(in.ToolName, keeperToolPrefix) {
		if in.isCodex() {
			return nil
		}
		// Allow only: a deny or ask rule of the user is evaluated by the client
		// regardless of what a hook returns, and a hook never overrides one.
		return hookOutput{hookSpecific{
			EventName: "PreToolUse",
			Decision:  "allow",
			Reason:    "keeper decides what runs and asks the person itself",
		}}
	}
	if in.ToolName != "Bash" {
		return nil
	}
	var hosts []string
	if len(pgTargets(in.ToolInput.Command)) > 0 {
		hosts = registered()
	}
	reason := deniedCommand(in.ToolInput.Command, hosts)
	if reason == "" {
		return nil
	}
	return hookOutput{hookSpecific{EventName: "PreToolUse", Decision: "deny", Reason: reason}}
}

var pgClients = map[string]bool{"psql": true, "pg_dump": true, "pg_dumpall": true, "pgcli": true}

// humanOnly are the keeper subcommands that only a person at a terminal may
// run. An empty set means every form of the subcommand.
var humanOnly = map[string]map[string]bool{
	"approve":    nil,
	"connect":    nil,
	"host":       nil,
	"vault":      nil,
	"update":     nil,
	"settings":   {"set": true},
	"ui":         nil,
	"connection": {"set": true, "rm": true},
	"catalog":    {"edit": true, "init": true},
}

// deniedCommand returns why a shell command must not run, or "". registered is
// the address list of every host the daemon knows.
func deniedCommand(command string, registered []string) string {
	for _, words := range commandWords(command, 0) {
		if sub, rest := keeperSubcommand(words); sub != "" {
			set, listed := humanOnly[sub]
			if listed && (set == nil || (len(rest) > 0 && set[rest[0]])) {
				return "keeper " + sub + " is for a person at a terminal. Ask the user to run it, and use keeper's MCP tools for the data."
			}
		}
	}
	for _, target := range pgTargets(command) {
		for _, r := range registered {
			if sameHost(target, r) {
				return "That database host is registered with keeper, and raw clients bypass its masking. Use keeper's query tool instead."
			}
		}
	}
	return ""
}

// commandWords splits a shell line into simple commands, each a list of words.
// A quoted word holding a space is split again, so `bash -c "psql -h x"` is
// seen. It reads shell well enough for guidance, and does not claim more.
func commandWords(line string, depth int) [][]string {
	var cmds [][]string
	var cur []string
	var word strings.Builder
	var quote rune
	inWord := false
	flushWord := func() {
		if inWord {
			cur = append(cur, word.String())
		}
		word.Reset()
		inWord = false
	}
	flushCmd := func() {
		flushWord()
		if len(cur) > 0 {
			cmds = append(cmds, cur)
		}
		cur = nil
	}
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == ' ' || r == '\t':
			flushWord()
		case strings.ContainsRune(";&|\n()`", r):
			flushCmd()
		default:
			word.WriteRune(r)
			inWord = true
		}
	}
	flushCmd()
	if depth < 2 {
		for _, c := range cmds {
			for _, w := range c {
				if strings.ContainsAny(w, " \t\n") {
					cmds = append(cmds, commandWords(w, depth+1)...)
				}
			}
		}
	}
	return cmds
}

var launchers = map[string]bool{"sudo": true, "env": true, "time": true, "nohup": true, "exec": true, "command": true, "nice": true}

// program returns the index of the command a word list runs, past env
// assignments, launchers and a launcher's own options, or -1.
func program(words []string) int {
	launched := false
	for i := 0; i < len(words); i++ {
		w := words[i]
		switch {
		case strings.Contains(w, "=") && !strings.HasPrefix(w, "-") && !strings.Contains(w, "/"):
		case launchers[path.Base(w)]:
			launched = true
		case launched && strings.HasPrefix(w, "-"):
			if w == "-u" || w == "-g" {
				i++
			}
		default:
			return i
		}
	}
	return -1
}

func keeperSubcommand(words []string) (string, []string) {
	i := program(words)
	if i < 0 || path.Base(words[i]) != "keeper" {
		return "", nil
	}
	var rest []string
	for _, w := range words[i+1:] {
		if !strings.HasPrefix(w, "-") {
			rest = append(rest, w)
		}
	}
	if len(rest) == 0 {
		return "", nil
	}
	return rest[0], rest[1:]
}

// pgTargets lists every host a database client in the command is aimed at: a
// -h or --host argument, the host of a connection URI, a host= conninfo key
// and PGHOST=.
func pgTargets(command string) []string {
	var targets []string
	hasClient := false
	cmds := commandWords(command, 0)
	for _, words := range cmds {
		i := program(words)
		if i < 0 || !pgClients[path.Base(words[i])] {
			continue
		}
		hasClient = true
		args := words[i+1:]
		for j, a := range args {
			switch {
			case a == "-h" || a == "--host":
				if j+1 < len(args) {
					targets = append(targets, args[j+1])
				}
			case strings.HasPrefix(a, "--host="):
				targets = append(targets, a[len("--host="):])
			case strings.HasPrefix(a, "-h") && len(a) > 2 && !strings.HasPrefix(a, "--"):
				targets = append(targets, a[2:])
			case strings.HasPrefix(a, "postgres://") || strings.HasPrefix(a, "postgresql://"):
				if u, err := url.Parse(a); err == nil && u.Hostname() != "" {
					targets = append(targets, u.Hostname())
				}
			case strings.Contains(a, "host="):
				for _, kv := range strings.Fields(a) {
					if v, ok := strings.CutPrefix(kv, "host="); ok {
						targets = append(targets, v)
					}
				}
			}
		}
	}
	if !hasClient {
		return nil
	}
	// PGHOST may be set on the client's own line or by an earlier export.
	for _, words := range cmds {
		for _, w := range words {
			if v, ok := strings.CutPrefix(w, "PGHOST="); ok {
				targets = append(targets, v)
			}
		}
	}
	return targets
}

func normalizeHost(h string) string {
	h = strings.ToLower(strings.Trim(h, "[]"))
	if h == "localhost" {
		return "127.0.0.1"
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		return "127.0.0.1"
	}
	return h
}

func sameHost(a, b string) bool { return normalizeHost(a) == normalizeHost(b) }
