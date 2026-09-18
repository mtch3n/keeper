package mcpapp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mtchen/keeper/internal/client"
	"github.com/mtchen/keeper/internal/types"
)

// registerTools adds exactly the ten tools SPEC §6.1/§6.3 and R8.7b expose to
// an agent, and nothing else. register_connection, set_policy, catalog init,
// approve, grants, allow and denylist are CLI/UI only (R4.1f, §6.3) and must
// never be added here.
func registerTools(s *mcp.Server, h *daemonHolder) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "set_session_intent",
		Description: "Set this session's stated task intent. Required before any query; shown on approval screens and recorded in the audit log.",
	}, h.setSessionIntent)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_connections",
		Description: "List every registered database connection available to this session.",
	}, h.listConnections)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "describe_connection",
		Description: "Describe one connection: engine, version, database, schema, role, audited privileges, catalog status, policy summary and mode.",
	}, h.describeConnection)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_schema",
		Description: "Get a connection's schema, filterable by schema and table pattern. Columns whose names are marked sensitive are omitted and counted, not listed.",
	}, h.getSchema)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "explain",
		Description: "Dry-run a statement: what it would touch, what would be masked, and whether it would escalate, without spending an approval.",
	}, h.explain)

	mcp.AddTool(s, &mcp.Tool{
		Name: "query",
		Description: "Run a statement (read or write) against a connection. Returns rows directly for a low-tier read, " +
			"or a ticket to poll with get_result when the statement needs approval. max_rows is bounded by an operator " +
			"ceiling this tool cannot raise.",
	}, h.query)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_result",
		Description: "Poll a ticket returned by query. Call again while state is pending_approval; wait_ms is capped at 25000.",
	}, h.getResult)

	mcp.AddTool(s, &mcp.Tool{
		Name: "request_input",
		Description: "Open a local browser form for a human to enter a sensitive value (an email, a name, ...) that this " +
			"session must never see directly. Present the returned url to the user, then poll get_input_result.",
	}, h.requestInput)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_input_result",
		Description: "Poll an input request. A ready result carries only an opaque token and its namespace, never the value itself.",
	}, h.getInputResult)

	mcp.AddTool(s, &mcp.Tool{
		Name: "request_authorization",
		Description: "Open a local browser form asking a human to authorize reading one relation (schema.table) this " +
			"session is not yet permitted to read. Present the returned url to the user, then poll get_authorization_result.",
	}, h.requestAuthorization)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_authorization_result",
		Description: "Poll an authorization request. Never returns a token — only a state change the next query observes.",
	}, h.getAuthorizationResult)
}

// --- set_session_intent ---

type SetSessionIntentIn struct {
	Intent string `json:"intent" jsonschema:"the session's stated task, e.g. 'reconciliation investigation OPS-441'"`
}

type OkOut struct {
	OK bool `json:"ok"`
}

func (h *daemonHolder) setSessionIntent(ctx context.Context, req *mcp.CallToolRequest, in SetSessionIntentIn) (*mcp.CallToolResult, OkOut, error) {
	cli, err := h.get(ctx, req)
	if err != nil {
		return nil, OkOut{}, err
	}
	if err := cli.SetSessionIntent(ctx, in.Intent); err != nil {
		return nil, OkOut{}, err
	}
	return nil, OkOut{OK: true}, nil
}

// --- list_connections ---

// ConnectionInfo is list_connections' per-item shape (SPEC §6.1): the role
// name is disclosed deliberately (it is the username, not a credential) so
// the agent can interpret a permission refusal.
type ConnectionInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Engine   string `json:"engine"`
	Database string `json:"database"`
	Role     string `json:"role"`
}

func (h *daemonHolder) listConnections(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, []ConnectionInfo, error) {
	cli, err := h.get(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	conns, err := cli.ListConnections(ctx)
	if err != nil {
		return nil, nil, err
	}
	out := make([]ConnectionInfo, len(conns))
	for i, c := range conns {
		out[i] = ConnectionInfo{ID: c.ID, Name: c.Name, Engine: c.Engine, Database: c.Database, Role: c.Role}
	}
	return nil, out, nil
}

// --- describe_connection ---

type DescribeConnectionIn struct {
	ID string `json:"id" jsonschema:"the connection id from list_connections"`
}

func (h *daemonHolder) describeConnection(ctx context.Context, req *mcp.CallToolRequest, in DescribeConnectionIn) (*mcp.CallToolResult, client.ConnectionDetail, error) {
	cli, err := h.get(ctx, req)
	if err != nil {
		return nil, client.ConnectionDetail{}, err
	}
	detail, err := cli.DescribeConnection(ctx, in.ID)
	if err != nil {
		return nil, client.ConnectionDetail{}, err
	}
	return nil, *detail, nil
}

// --- get_schema ---

type GetSchemaIn struct {
	ConnectionID string `json:"connection_id"`
	Schema       string `json:"schema,omitzero" jsonschema:"restrict to this schema name"`
	TablePattern string `json:"table_pattern,omitzero" jsonschema:"restrict to tables matching this pattern"`
}

func (h *daemonHolder) getSchema(ctx context.Context, req *mcp.CallToolRequest, in GetSchemaIn) (*mcp.CallToolResult, client.SchemaResult, error) {
	cli, err := h.get(ctx, req)
	if err != nil {
		return nil, client.SchemaResult{}, err
	}
	res, err := cli.GetSchema(ctx, in.ConnectionID, in.Schema, in.TablePattern)
	if err != nil {
		return nil, client.SchemaResult{}, err
	}
	return nil, *res, nil
}

// --- explain ---

// ToolParam is one bound parameter, exactly as SPEC §6.2 puts it on the
// wire: {"value": …} or {"token": "⟨…⟩"}. keeper never substitutes a
// resolved value into statement text — a token resolves to a bound
// parameter or the call fails.
type ToolParam struct {
	Value any    `json:"value,omitzero"`
	Token string `json:"token,omitzero"`
}

func toClientParams(params []ToolParam) []client.Param {
	if params == nil {
		return nil
	}
	out := make([]client.Param, len(params))
	for i, p := range params {
		out[i] = client.Param{Value: p.Value, Token: p.Token}
	}
	return out
}

type ExplainIn struct {
	ConnectionID string      `json:"connection_id"`
	SQL          string      `json:"sql"`
	Params       []ToolParam `json:"params,omitzero"`
}

func (h *daemonHolder) explain(ctx context.Context, req *mcp.CallToolRequest, in ExplainIn) (*mcp.CallToolResult, types.ExplainResult, error) {
	cli, err := h.get(ctx, req)
	if err != nil {
		return nil, types.ExplainResult{}, err
	}
	res, err := cli.Explain(ctx, in.ConnectionID, in.SQL, toClientParams(in.Params))
	if err != nil {
		return nil, types.ExplainResult{}, err
	}
	return nil, *res, nil
}

// --- query ---

type QueryIn struct {
	ConnectionID string      `json:"connection_id"`
	SQL          string      `json:"sql"`
	Params       []ToolParam `json:"params,omitzero"`
	MaxRows      int         `json:"max_rows,omitzero" jsonschema:"bounded by the connection's operator ceiling; cannot be raised past it"`
}

// QueryOut is query's response: either the fields of a completed
// types.QueryResult, or {ticket, state, reason, audit_id} when the
// statement escalated (SPEC §6.1). Exactly one shape is populated.
type QueryOut struct {
	Rows       [][]any                    `json:"rows,omitzero"`
	Columns    []types.ColumnMeta         `json:"columns,omitzero"`
	RowCount   int                        `json:"row_count,omitzero"`
	Truncated  bool                       `json:"truncated,omitzero"`
	Transforms map[string]types.Transform `json:"transforms,omitzero"`
	Tier       types.Tier                 `json:"tier,omitzero"`
	AuditID    string                     `json:"audit_id,omitzero"`

	Ticket string            `json:"ticket,omitzero"`
	State  types.TicketState `json:"state,omitzero"`
	Reason string            `json:"reason,omitzero"`
}

func (h *daemonHolder) query(ctx context.Context, req *mcp.CallToolRequest, in QueryIn) (*mcp.CallToolResult, QueryOut, error) {
	cli, err := h.get(ctx, req)
	if err != nil {
		return nil, QueryOut{}, err
	}
	outcome, err := cli.Query(ctx, in.ConnectionID, in.SQL, toClientParams(in.Params), in.MaxRows)
	if err != nil {
		return nil, QueryOut{}, err
	}
	if outcome.Result != nil {
		r := outcome.Result
		return nil, QueryOut{
			Rows: r.Rows, Columns: r.Columns, RowCount: r.RowCount, Truncated: r.Truncated,
			Transforms: r.Transforms, Tier: r.Tier, AuditID: r.AuditID,
		}, nil
	}
	t := outcome.Ticket
	return nil, QueryOut{Ticket: t.ID, State: t.State, Reason: t.Reason, AuditID: t.AuditID}, nil
}

// --- get_result ---

type GetResultIn struct {
	Ticket string `json:"ticket"`
	WaitMs int    `json:"wait_ms,omitzero" jsonschema:"how long to block waiting for a decision, capped at 25000ms"`
}

// GetResultOut is get_result's response (SPEC §6.1): {state, rows?,
// transforms?, error?}.
type GetResultOut struct {
	State      types.TicketState          `json:"state"`
	Rows       [][]any                    `json:"rows,omitzero"`
	Transforms map[string]types.Transform `json:"transforms,omitzero"`
	Error      *types.Error               `json:"error,omitzero"`
}

func (h *daemonHolder) getResult(ctx context.Context, req *mcp.CallToolRequest, in GetResultIn) (*mcp.CallToolResult, GetResultOut, error) {
	cli, err := h.get(ctx, req)
	if err != nil {
		return nil, GetResultOut{}, err
	}
	res, err := cli.GetResult(ctx, in.Ticket, in.WaitMs)
	if err != nil {
		return nil, GetResultOut{}, err
	}
	out := GetResultOut{State: res.State, Error: res.Error}
	if res.Result != nil {
		out.Rows = res.Result.Rows
		out.Transforms = res.Result.Transforms
	}
	return nil, out, nil
}

// --- request_input ---

type RequestInputIn struct {
	ConnectionID string `json:"connection_id"`
	Namespace    string `json:"namespace" jsonschema:"the token namespace; two columns tokenized under the same namespace can be joined on"`
	Purpose      string `json:"purpose,omitzero" jsonschema:"screened, user-visible task text; never a value"`
}

// LocalRequestOut is request_input / request_authorization's response
// (SPEC R8.7b, R8.7g): a link to the local page, never the value itself.
type LocalRequestOut struct {
	RequestID string `json:"request_id"`
	URL       string `json:"url"`
	State     string `json:"state"`
	ExpiresAt string `json:"expires_at"`
}

func fromLocalRequest(r *types.LocalRequest) LocalRequestOut {
	return LocalRequestOut{
		RequestID: r.ID,
		URL:       r.URL,
		State:     r.State,
		ExpiresAt: r.ExpiresAt.Format(time.RFC3339),
	}
}

func (h *daemonHolder) requestInput(ctx context.Context, req *mcp.CallToolRequest, in RequestInputIn) (*mcp.CallToolResult, LocalRequestOut, error) {
	cli, err := h.get(ctx, req)
	if err != nil {
		return nil, LocalRequestOut{}, err
	}
	r, err := cli.RequestInput(ctx, in.ConnectionID, in.Namespace, in.Purpose)
	if err != nil {
		return nil, LocalRequestOut{}, err
	}
	return nil, fromLocalRequest(r), nil
}

// --- get_input_result ---

type GetInputResultIn struct {
	RequestID string `json:"request_id"`
	WaitMs    int    `json:"wait_ms,omitzero" jsonschema:"how long to block waiting for the human to submit the form, capped at 25000ms"`
}

func (h *daemonHolder) getInputResult(ctx context.Context, req *mcp.CallToolRequest, in GetInputResultIn) (*mcp.CallToolResult, client.RequestResult, error) {
	cli, err := h.get(ctx, req)
	if err != nil {
		return nil, client.RequestResult{}, err
	}
	res, err := cli.GetInputResult(ctx, in.RequestID, in.WaitMs)
	if err != nil {
		return nil, client.RequestResult{}, err
	}
	return nil, *res, nil
}

// --- request_authorization ---

type RequestAuthorizationIn struct {
	ConnectionID string `json:"connection_id"`
	Path         string `json:"path" jsonschema:"the relation this session may not yet read, as schema.table"`
	Purpose      string `json:"purpose,omitzero" jsonschema:"screened, user-visible task text; never a value"`
}

func (h *daemonHolder) requestAuthorization(ctx context.Context, req *mcp.CallToolRequest, in RequestAuthorizationIn) (*mcp.CallToolResult, LocalRequestOut, error) {
	cli, err := h.get(ctx, req)
	if err != nil {
		return nil, LocalRequestOut{}, err
	}
	rel, err := parseRelationPath(in.Path)
	if err != nil {
		return nil, LocalRequestOut{}, err
	}
	path := types.PathRef{ConnectionID: in.ConnectionID, Relation: rel}
	r, err := cli.RequestAuthorization(ctx, in.ConnectionID, path, in.Purpose)
	if err != nil {
		return nil, LocalRequestOut{}, err
	}
	return nil, fromLocalRequest(r), nil
}

// --- get_authorization_result ---

type GetAuthorizationResultIn struct {
	RequestID string `json:"request_id"`
	WaitMs    int    `json:"wait_ms,omitzero" jsonschema:"how long to block waiting for the human to decide, capped at 25000ms"`
}

// AuthorizationResultOut is get_authorization_result's response: a state
// only. It has no Token field at all — not omitted, absent — because an
// authorization result must never carry one (SPEC R8.7g).
type AuthorizationResultOut struct {
	State string `json:"state"`
}

func (h *daemonHolder) getAuthorizationResult(ctx context.Context, req *mcp.CallToolRequest, in GetAuthorizationResultIn) (*mcp.CallToolResult, AuthorizationResultOut, error) {
	cli, err := h.get(ctx, req)
	if err != nil {
		return nil, AuthorizationResultOut{}, err
	}
	res, err := cli.GetAuthorizationResult(ctx, in.RequestID, in.WaitMs)
	if err != nil {
		return nil, AuthorizationResultOut{}, err
	}
	return nil, AuthorizationResultOut{State: res.State}, nil
}
