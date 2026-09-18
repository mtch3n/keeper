package api

import (
	"context"
	"net/http"

	"github.com/mtchen/keeper/internal/daemon"
	"github.com/mtchen/keeper/internal/types"
)

// param is §6.2's wire shape: {"value": ...} or {"token": "..."}. A token
// resolves to a bound parameter or the call fails; keeper never substitutes into
// statement text (R6.2).
type param struct {
	Value any    `json:"value,omitzero"`
	Token string `json:"token,omitzero"`
}

func toParams(in []param) []daemon.Param {
	out := make([]daemon.Param, 0, len(in))
	for _, p := range in {
		out = append(out, daemon.Param{Value: p.Value, Token: p.Token})
	}
	return out
}

type sessionRequest struct {
	Client types.ClientInfo `json:"client"`
}

type sessionResponse struct {
	SessionID     string `json:"session_id"`
	DaemonVersion string `json:"daemon_version"`
}

// openSession is the handshake. It compares versions: on a mismatch the client
// errors naming both and `keeper daemon restart`, and never restarts the daemon
// itself, because one window doing so cancels every other window's tickets.
func (s *Server) openSession(ctx context.Context, _ *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	var req sessionRequest
	if err := s.readJSON(w, r, &req); err != nil {
		return nil, err
	}
	if err := s.d.CheckVersion(r.Header.Get("X-Keeper-Version")); err != nil {
		return nil, err
	}
	sess, err := s.d.OpenSession(ctx, req.Client)
	if err != nil {
		return nil, err
	}
	return sessionResponse{SessionID: sess.ID(), DaemonVersion: s.d.Version()}, nil
}

type intentRequest struct {
	Intent string `json:"intent"`
}

type okResponse struct {
	OK bool `json:"ok"`
}

func (s *Server) setIntent(ctx context.Context, rq *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	var req intentRequest
	if err := s.readJSON(w, r, &req); err != nil {
		return nil, err
	}
	if err := s.d.SetIntent(ctx, rq.session, req.Intent); err != nil {
		return nil, err
	}
	return okResponse{OK: true}, nil
}

type statementRequest struct {
	SQL     string  `json:"sql"`
	Params  []param `json:"params,omitzero"`
	MaxRows int     `json:"max_rows,omitzero"`
}

func (s *Server) explain(ctx context.Context, rq *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	var req statementRequest
	if err := s.readJSON(w, r, &req); err != nil {
		return nil, err
	}
	return s.d.Explain(ctx, rq.session, r.PathValue("id"), req.SQL, toParams(req.Params))
}

// query returns rows, or a ticket. §3.2: a harness times out a tool call long
// before a human notices a terminal, so an escalation is a handle to poll and
// never a blocked response.
func (s *Server) query(ctx context.Context, rq *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	var req statementRequest
	if err := s.readJSON(w, r, &req); err != nil {
		return nil, err
	}
	res, tk, err := s.d.Query(ctx, rq.session, r.PathValue("id"), req.SQL, toParams(req.Params), req.MaxRows)
	switch {
	case err != nil:
		return nil, err
	case res != nil:
		return res, nil
	default:
		return tk, nil
	}
}

type ticketResponse struct {
	Ticket  string             `json:"ticket"`
	State   types.TicketState  `json:"state"`
	Tier    types.Tier         `json:"tier,omitzero"`
	Reason  string             `json:"reason,omitzero"`
	AuditID string             `json:"audit_id,omitzero"`
	Result  *types.QueryResult `json:"result,omitzero"`
	Error   *types.Error       `json:"error,omitzero"`
}

// ticket is get_result. The daemon refuses a ticket issued to another session
// (R3.4b) and reports it as unknown, so holding one is not an oracle.
func (s *Server) ticket(ctx context.Context, rq *reqInfo, _ http.ResponseWriter, r *http.Request) (any, error) {
	v, err := s.d.WaitTicket(ctx, rq.session, r.PathValue("id"), s.waitFor(r))
	if err != nil {
		return nil, err
	}
	out := ticketResponse{
		Ticket: v.Ticket.ID, State: v.Ticket.State, Tier: v.Ticket.Tier,
		Reason: v.Ticket.Reason, AuditID: v.Ticket.AuditID, Result: v.Result,
	}
	if v.Error != nil {
		// The ticket's failure is converted here like every other error, and not
		// where it was produced.
		out.Error, _ = asError(v.Error)
	}
	return out, nil
}

type localRequestRequest struct {
	Kind         types.RequestKind `json:"kind"`
	ConnectionID string            `json:"connection_id"`
	Namespace    string            `json:"namespace,omitzero"`
	Purpose      string            `json:"purpose,omitzero"`
	Path         *types.PathRef    `json:"path,omitzero"`
}

// openRequest is R8.7g's one mechanism: an input request takes a value the agent
// must never see and returns a token; an authorization request grants a path and
// returns nothing at all.
func (s *Server) openRequest(ctx context.Context, rq *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	var req localRequestRequest
	if err := s.readJSON(w, r, &req); err != nil {
		return nil, err
	}
	return s.d.OpenRequest(ctx, rq.session, daemon.RequestSpec{
		Kind:         req.Kind,
		ConnectionID: req.ConnectionID,
		Namespace:    req.Namespace,
		Purpose:      req.Purpose,
		Path:         req.Path,
	})
}

func (s *Server) listConnections(ctx context.Context, _ *reqInfo, _ http.ResponseWriter, _ *http.Request) (any, error) {
	return s.d.Connections(ctx)
}

func (s *Server) describeConnection(ctx context.Context, _ *reqInfo, _ http.ResponseWriter, r *http.Request) (any, error) {
	return s.d.Describe(ctx, r.PathValue("id"))
}

func (s *Server) schema(ctx context.Context, _ *reqInfo, _ http.ResponseWriter, r *http.Request) (any, error) {
	q := r.URL.Query()
	return s.d.Schema(ctx, r.PathValue("id"), q.Get("schema"), q.Get("table"))
}

type versionResponse struct {
	Version string `json:"version"`
}

func (s *Server) version(_ context.Context, _ *reqInfo, _ http.ResponseWriter, _ *http.Request) (any, error) {
	return versionResponse{Version: s.d.Version()}, nil
}
