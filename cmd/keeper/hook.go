package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
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
		addrs[i] = net.JoinHostPort(h.Address, strconv.Itoa(h.Port))
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
	if len(pgTargets(shellCommands(in.ToolInput.Command))) > 0 {
		hosts = registered()
	}
	reason := deniedCommand(in.ToolInput.Command, hosts)
	if reason == "" {
		return nil
	}
	return hookOutput{hookSpecific{EventName: "PreToolUse", Decision: "deny", Reason: reason}}
}

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

// deniedCommand returns why a shell command must not run, or "". registered
// holds every host the daemon knows as "address:port"; an entry without a port
// stands for 5432.
func deniedCommand(command string, registered []string) string {
	cmds := shellCommands(command)
	for _, c := range cmds {
		if sub, rest := keeperSubcommand(c.words); sub != "" {
			set, listed := humanOnly[sub]
			if listed && (set == nil || (len(rest) > 0 && set[rest[0]])) {
				return "keeper " + sub + " is for a person at a terminal. Ask the user to run it, and use keeper's MCP tools for the data."
			}
		}
	}
	for _, target := range pgTargets(cmds) {
		for _, r := range registered {
			if sameServer(target, r) {
				return "That database host is registered with keeper, and raw clients bypass its masking. Use keeper's query tool instead."
			}
		}
	}
	return ""
}
