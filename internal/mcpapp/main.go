// Package mcpapp is keeper's MCP stdio server. One process per harness
// window (SPEC §3.1): it starts keeperd if needed, holds one session-scoped
// connection to it for its whole life, and exposes exactly the tool surface
// of SPEC §6.1/§6.3 — the classification, credential and acceptance surface
// stays CLI/UI only.
package mcpapp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mtchen/keeper/internal/client"
)

// Run serves MCP over stdio until the transport closes.
func Run() error {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "keeper-mcp",
		Version: client.Version,
	}, nil)

	registerTools(server, &daemonHolder{})

	return server.Run(context.Background(), &mcp.StdioTransport{})
}
