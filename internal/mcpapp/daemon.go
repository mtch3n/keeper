package mcpapp

import (
	"context"
	"errors"
	"os"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mtchen/keeper/internal/client"
	"github.com/mtchen/keeper/internal/types"
)

// daemonHolder lazily connects to keeperd on the first tool call. keeper-mcp
// is one process per harness window (SPEC §3.1's diagram), so one connection
// — one session (R3.4e) — serves every tool call this process ever makes.
type daemonHolder struct {
	mu  sync.Mutex
	cli *client.Client
	err error // sticky: a version mismatch or dead daemon does not retry itself
}

// get returns the daemon connection, dialing (and starting keeperd if
// needed) on first use. req supplies the identity of whatever actually
// connected to this MCP server (Claude Code, Codex, ...): its clientInfo
// name and version, forwarded verbatim, are what makes the shared approval
// queue legible across windows (SPEC R9.1). PID and workspace are this
// keeper-mcp process's own — one entry per window, per the same requirement.
func (h *daemonHolder) get(ctx context.Context, req *mcp.CallToolRequest) (*client.Client, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cli != nil {
		return h.cli, nil
	}
	if h.err != nil {
		return nil, h.err
	}

	sock := client.DefaultSocketPath()
	if err := client.StartDaemon(ctx, sock); err != nil {
		h.err = err
		return nil, err
	}

	info := types.ClientInfo{PID: os.Getpid()}
	if ci := req.ClientInfo(); ci != nil {
		info.Name = ci.Name
		info.Version = ci.Version
	}
	if wd, err := os.Getwd(); err == nil {
		info.Workspace = wd
	}

	cli, err := client.Dial(ctx, sock, info)
	if err != nil {
		// A version mismatch is not sticky in the way a dead daemon is: an
		// operator may run `keeper daemon restart` and this same process
		// should be able to try again on its next tool call.
		if _, ok := errors.AsType[*client.VersionMismatchError](err); !ok {
			h.err = err
		}
		return nil, err
	}
	h.cli = cli
	return cli, nil
}
