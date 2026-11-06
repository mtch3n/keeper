// Package mcpapp is keeper's MCP stdio server. One process per harness
// window (SPEC §3.1): it starts keeperd if needed, holds one session-scoped
// connection to it for its whole life, and exposes exactly the tool surface
// of SPEC §6.1/§6.3 — the classification, credential and acceptance surface
// stays CLI/UI only.
package mcpapp

import (
	"bytes"
	"context"
	"io"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mtchen/keeper/internal/client"
)

// Run serves MCP over stdio until the transport closes. It watches the session
// on stdio so that a version mismatch can hand it to another build (UPG-D1).
func Run() error {
	s := &session{}
	var in io.Reader = os.Stdin
	if replay := s.resume(); replay != nil {
		in = io.MultiReader(bytes.NewReader(replay), os.Stdin)
	}
	transport := &mcp.IOTransport{
		Reader: sessionReader{r: in, s: s},
		Writer: sessionWriter{w: os.Stdout, s: s},
	}
	return newServer(&daemonHolder{session: s}).Run(context.Background(), transport)
}

// NewServer is keeper's MCP server with its whole tool surface, connecting to
// the daemon on first use. Run serves it over stdio; a test serves it over an
// in-memory transport, so what it measures is the server the plugin runs.
func NewServer() *mcp.Server {
	return newServer(&daemonHolder{})
}

func newServer(h *daemonHolder) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "keeper-mcp",
		Version: client.Version,
	}, nil)
	registerTools(server, h)
	return server
}
