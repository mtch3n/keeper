package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/mtchen/keeper/internal/daemon"
	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

type registerRequest struct {
	Name string `json:"name"`
	DSN  string `json:"dsn"`
	// WriteDSN is the separate _rw credential. There is no boolean that enables
	// writes; if this is absent, write mode does not exist (§4.2).
	WriteDSN    string `json:"write_dsn,omitzero"`
	CatalogPath string `json:"catalog_path,omitzero"`
}

// registerConnection audits the role and stores the connection disabled, with
// its findings. Nothing here logs or echoes the DSN.
func (s *Server) registerConnection(ctx context.Context, _ *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	var req registerRequest
	if err := s.readJSON(w, r, &req); err != nil {
		return nil, err
	}
	return s.d.Register(ctx, daemon.RegisterSpec{
		Name: req.Name, DSN: req.DSN, WriteDSN: req.WriteDSN, CatalogPath: req.CatalogPath,
	})
}

func (s *Server) auditConnection(ctx context.Context, _ *reqInfo, _ http.ResponseWriter, r *http.Request) (any, error) {
	return s.d.Audit(ctx, r.PathValue("id"))
}

type acceptRequest struct {
	FindingIDs []string `json:"finding_ids"`
	Actor      string   `json:"actor"`
	// Via is "cli" or "ui". It is never "mcp": R4.1f and §6.3.
	Via string `json:"via"`
}

func (s *Server) acceptFindings(ctx context.Context, rq *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	var req acceptRequest
	if err := s.readJSON(w, r, &req); err != nil {
		return nil, err
	}
	if req.Via == "" {
		req.Via = defaultVia(rq.surface)
	}
	return s.d.Accept(ctx, r.PathValue("id"), req.FindingIDs, req.Actor, req.Via)
}

func defaultVia(sf surface) string {
	if sf == surfaceLoopback {
		return "ui"
	}
	return "cli"
}

type patchRequest struct {
	Mode   *types.Mode   `json:"mode,omitzero"`
	Limits *types.Limits `json:"limits,omitzero"`
}

func (s *Server) patchConnection(ctx context.Context, _ *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	var req patchRequest
	if err := s.readJSON(w, r, &req); err != nil {
		return nil, err
	}
	return s.d.Update(ctx, r.PathValue("id"), daemon.Patch{Mode: req.Mode, Limits: req.Limits})
}

type denylistRequest struct {
	Relations []types.RelationRef `json:"relations"`
}

func (s *Server) putDenylist(ctx context.Context, _ *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	var req denylistRequest
	if err := s.readJSON(w, r, &req); err != nil {
		return nil, err
	}
	return s.d.SetDenylist(ctx, r.PathValue("id"), req.Relations)
}

// listApprovals is the shared queue, oldest first, always (R9.1).
func (s *Server) listApprovals(_ context.Context, _ *reqInfo, _ http.ResponseWriter, _ *http.Request) (any, error) {
	return s.d.Approvals(), nil
}

type grantSpec struct {
	Lifetime   types.GrantLifetime `json:"lifetime,omitzero"`
	RowCeiling int                 `json:"row_ceiling,omitzero"`
}

type decideRequest struct {
	Decision string     `json:"decision"`
	Actor    string     `json:"actor,omitzero"`
	Grant    *grantSpec `json:"grant,omitzero"`
}

func (s *Server) decide(ctx context.Context, rq *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	var req decideRequest
	if err := s.readJSON(w, r, &req); err != nil {
		return nil, err
	}
	dec := daemon.Decision{Decide: req.Decision, Actor: actorOr(req.Actor, rq.surface)}
	if req.Grant != nil {
		dec.Grant = &daemon.GrantRequest{Lifetime: req.Grant.Lifetime, RowCeiling: req.Grant.RowCeiling}
	}
	if err := s.d.Decide(ctx, r.PathValue("ticket"), dec); err != nil {
		return nil, err
	}
	return okResponse{OK: true}, nil
}

func actorOr(actor string, sf surface) string {
	if actor != "" {
		return actor
	}
	return defaultVia(sf)
}

// listGrants is the permission list. Session grants sort first (R9.3d).
func (s *Server) listGrants(_ context.Context, _ *reqInfo, _ http.ResponseWriter, _ *http.Request) (any, error) {
	return s.d.Grants(), nil
}

type grantRequest struct {
	Path       types.PathRef       `json:"path"`
	Lifetime   types.GrantLifetime `json:"lifetime,omitzero"`
	RowCeiling int                 `json:"row_ceiling"`
	SessionID  string              `json:"session_id,omitzero"`
	Actor      string              `json:"actor,omitzero"`
}

func (s *Server) createGrant(_ context.Context, rq *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	var req grantRequest
	if err := s.readJSON(w, r, &req); err != nil {
		return nil, err
	}
	return s.d.CreateGrant(req.SessionID, req.Path,
		daemon.GrantRequest{Lifetime: req.Lifetime, RowCeiling: req.RowCeiling},
		actorOr(req.Actor, rq.surface))
}

func (s *Server) revokeGrant(_ context.Context, _ *reqInfo, _ http.ResponseWriter, r *http.Request) (any, error) {
	if err := s.d.RevokeGrant(r.PathValue("id")); err != nil {
		return nil, err
	}
	return okResponse{OK: true}, nil
}

func (s *Server) getCatalog(ctx context.Context, _ *reqInfo, _ http.ResponseWriter, r *http.Request) (any, error) {
	return s.d.Catalog(ctx, r.PathValue("connection"))
}

type catalogColumnsRequest struct {
	Entries map[string]types.ColumnPolicy `json:"entries"`
}

func (s *Server) putCatalogColumns(ctx context.Context, _ *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	var req catalogColumnsRequest
	if err := s.readJSON(w, r, &req); err != nil {
		return nil, err
	}
	if err := s.d.PutColumns(ctx, r.PathValue("connection"), req.Entries); err != nil {
		return nil, err
	}
	return s.d.Catalog(ctx, r.PathValue("connection"))
}

type catalogInitRequest struct {
	Sample int `json:"sample,omitzero"`
}

func (s *Server) initCatalog(ctx context.Context, _ *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	var req catalogInitRequest
	if err := s.readJSON(w, r, &req); err != nil {
		return nil, err
	}
	return s.d.InitCatalog(ctx, r.PathValue("connection"), req.Sample)
}

type suggestedGrants struct {
	Statements []string `json:"statements"`
}

func (s *Server) suggestGrants(ctx context.Context, _ *reqInfo, _ http.ResponseWriter, r *http.Request) (any, error) {
	stmts, err := s.d.SuggestGrants(ctx, r.PathValue("connection"))
	if err != nil {
		return nil, err
	}
	return suggestedGrants{Statements: stmts}, nil
}

func (s *Server) activity(ctx context.Context, _ *reqInfo, _ http.ResponseWriter, r *http.Request) (any, error) {
	q := r.URL.Query()
	f := ports.AuditFilter{SessionID: q.Get("session"), ConnectionID: q.Get("connection")}
	if v := q.Get("tier"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 4 {
			return nil, &types.ValidationError{Field: "tier", Reason: "must be 0 to 4"}
		}
		t := types.Tier(n)
		f.Tier = &t
	}
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return nil, &types.ValidationError{Field: "since", Reason: "must be an RFC 3339 timestamp"}
		}
		f.Since = t
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, &types.ValidationError{Field: "limit", Reason: "must be a positive integer"}
		}
		f.Limit = n
	}
	return s.d.Activity(ctx, f)
}

func (s *Server) activityRecord(ctx context.Context, _ *reqInfo, _ http.ResponseWriter, r *http.Request) (any, error) {
	return s.d.ActivityRecord(ctx, r.PathValue("audit_id"))
}

func (s *Server) doctor(ctx context.Context, _ *reqInfo, _ http.ResponseWriter, _ *http.Request) (any, error) {
	return s.d.Doctor(ctx), nil
}

type unlockRequest struct {
	Passphrase string `json:"passphrase"`
}

// unlockVault is §4.3's interactive headless path. The passphrase is read once,
// passed straight to the vault and never logged.
func (s *Server) unlockVault(ctx context.Context, _ *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	var req unlockRequest
	if err := s.readJSON(w, r, &req); err != nil {
		return nil, err
	}
	if err := s.d.Unlock(ctx, req.Passphrase); err != nil {
		return nil, err
	}
	return okResponse{OK: true}, nil
}

// shutdown asks the daemon to stop. It is on the human surface only: SPEC §6.3
// keeps it off the MCP tool list, because a restart cancels every other
// session's pending tickets and permanently invalidates every token they hold
// (R3.4d), and no agent gets to decide that for the others.
func (s *Server) shutdown(_ context.Context, _ *reqInfo, _ http.ResponseWriter, _ *http.Request) (any, error) {
	s.d.RequestShutdown()
	return map[string]string{"state": "stopping"}, nil
}

// removeConnection deletes a connection and everything scoped to it. Human
// surface only: SPEC §6.3 keeps connection management off the MCP tool list, and
// an agent that could delete the connection it is being masked by would be
// removing its own supervision.
func (s *Server) removeConnection(ctx context.Context, _ *reqInfo, _ http.ResponseWriter, r *http.Request) (any, error) {
	if err := s.d.Remove(ctx, r.PathValue("id")); err != nil {
		return nil, err
	}
	return map[string]string{"state": "removed"}, nil
}
