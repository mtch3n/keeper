package mcpapp

import (
	"slices"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// wantTools is exactly SPEC §6.1/§6.3's agent-facing surface. Nothing else
// may be registered: register_connection, set_policy, catalog init, approve,
// grants, allow and denylist are CLI/UI only (R4.1f).
var wantTools = []string{
	"set_session_intent",
	"list_connections",
	"describe_connection",
	"get_schema",
	"explain",
	"query",
	"get_result",
	"request_input",
	"get_input_result",
	"request_authorization",
	"get_authorization_result",
}

// forbiddenTools names SPEC §6.3's CLI/UI-only surface. If any of these ever
// gets added as an MCP tool, R4.1f (no MCP tool creates a G0 acceptance) and
// R6.3 (no classification input from the harness model) are both broken.
var forbiddenTools = []string{
	"register_connection",
	"set_policy",
	"catalog_init",
	"approve",
	"grants",
	"allow",
	"denylist",
}

func TestToolListIsExactly(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "keeper-mcp-test"}, nil)
	registerTools(server, &daemonHolder{})

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ss, err := server.Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	defer ss.Close()

	cli := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	cs, err := cli.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer cs.Close()

	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	var got []string
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
	}
	sort.Strings(got)

	want := slices.Clone(wantTools)
	sort.Strings(want)

	if !slices.Equal(got, want) {
		t.Fatalf("tool list = %v, want exactly %v", got, want)
	}

	for _, forbidden := range forbiddenTools {
		if slices.Contains(got, forbidden) {
			t.Errorf("forbidden tool %q is registered (SPEC R4.1f/R6.3)", forbidden)
		}
	}
}

// MCP structured content must be a JSON object. The SDK accepts any output
// type and advertises its schema, so a tool returning a slice registers
// cleanly and then fails on every call, when the client validates the result.
func TestEveryOutputSchemaIsAnObject(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "keeper-mcp-test"}, nil)
	registerTools(server, &daemonHolder{})

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ss, err := server.Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	defer ss.Close()

	cli := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	cs, err := cli.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer cs.Close()

	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range res.Tools {
		if tool.OutputSchema == nil {
			continue
		}
		schema, ok := tool.OutputSchema.(map[string]any)
		if !ok || schema["type"] != "object" {
			t.Errorf("%s: output schema type = %v, want object", tool.Name, schema["type"])
		}
	}
}
