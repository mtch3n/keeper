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

type credentialRequest struct {
	User     string `json:"user"`
	Password string `json:"password,omitzero"`
}

type registerRequest struct {
	Name        string            `json:"name"`
	HostID      string            `json:"host_id"`
	Database    string            `json:"database"`
	Credential  credentialRequest `json:"credential"`
	Writes      types.Writes      `json:"writes,omitzero"`
	CatalogPath string            `json:"catalog_path,omitzero"`
}

// registerConnection stores the connection and reports what G0 found about its
// role. The connection is usable either way, including when the audit could not
// run at all (R4.1). Nothing here logs or echoes a credential.
func (s *Server) registerConnection(ctx context.Context, _ *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	var req registerRequest
	if err := s.readJSON(w, r, &req); err != nil {
		return nil, err
	}
	spec := daemon.RegisterSpec{
		Name: req.Name, HostID: req.HostID, Database: req.Database,
		Credential:  ports.Credential(req.Credential),
		Writes:      req.Writes,
		CatalogPath: req.CatalogPath,
	}
	return s.d.Register(ctx, spec)
}

func (s *Server) listHosts(ctx context.Context, _ *reqInfo, _ http.ResponseWriter, _ *http.Request) (any, error) {
	return s.d.Hosts(ctx)
}

type hostRequest struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port,omitzero"`
	SSLMode string `json:"sslmode,omitzero"`
}

func (s *Server) registerHost(ctx context.Context, _ *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	var req hostRequest
	if err := s.readJSON(w, r, &req); err != nil {
		return nil, err
	}
	return s.d.RegisterHost(ctx, daemon.HostSpec(req))
}

func (s *Server) removeHost(ctx context.Context, _ *reqInfo, _ http.ResponseWriter, r *http.Request) (any, error) {
	if err := s.d.RemoveHost(ctx, r.PathValue("id")); err != nil {
		return nil, err
	}
	return map[string]string{"state": "removed"}, nil
}

// defaultVia names the surface a human action arrived on. It is never "mcp":
// R4.1f and §6.3.
func defaultVia(sf surface) string {
	if sf == surfaceLoopback {
		return "ui"
	}
	return "cli"
}

type patchRequest struct {
	Reads     *types.Reads     `json:"reads,omitzero"`
	Limits    *types.Limits    `json:"limits,omitzero"`
	Detection *types.Detection `json:"detection,omitzero"`
	Writes    *types.Writes    `json:"writes,omitzero"`
}

func (s *Server) patchConnection(ctx context.Context, _ *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	var req patchRequest
	if err := s.readJSON(w, r, &req); err != nil {
		return nil, err
	}
	return s.d.Update(ctx, r.PathValue("id"), daemon.Patch{Reads: req.Reads, Limits: req.Limits, Detection: req.Detection, Writes: req.Writes})
}

// listApprovals is the shared queue, oldest first, always (R9.1).
func (s *Server) listApprovals(_ context.Context, _ *reqInfo, _ http.ResponseWriter, _ *http.Request) (any, error) {
	return s.d.Approvals(), nil
}

// getApproval is one waiting ticket for its local page. A ticket that is settled
// or unknown is not in the queue, and both are reported as unknown.
func (s *Server) getApproval(_ context.Context, _ *reqInfo, _ http.ResponseWriter, r *http.Request) (any, error) {
	return s.d.Approval(r.PathValue("ticket"))
}

type decideRequest struct {
	Decision string `json:"decision"`
	Actor    string `json:"actor,omitzero"`
}

func (s *Server) decide(ctx context.Context, rq *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	var req decideRequest
	if err := s.readJSON(w, r, &req); err != nil {
		return nil, err
	}
	dec := daemon.Decision{Decide: req.Decision, Actor: actorOr(req.Actor, rq.surface)}
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

func (s *Server) activity(ctx context.Context, _ *reqInfo, _ http.ResponseWriter, r *http.Request) (any, error) {
	q := r.URL.Query()
	f := ports.AuditFilter{SessionID: q.Get("session"), ConnectionID: q.Get("connection"), Decision: types.Decision(q.Get("decision")), Before: q.Get("before")}
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

// exportResponse carries the export, sealed under the operator's passphrase.
// Anyone with the file and the passphrase holds every credential and token key
// in it, so where it is written is the operator's decision to make.
type exportResponse struct {
	Data []byte `json:"data"`
}

// exportVault is `keeper vault export`. Human surface only: an agent that could
// export the vault could carry off every connection keeper holds, which is the
// whole point of keeper holding them.
type vaultRequest struct {
	Data       []byte `json:"data,omitzero"`
	Passphrase string `json:"passphrase"`
}

func (s *Server) exportVault(ctx context.Context, _ *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	var req vaultRequest
	if err := s.readJSON(w, r, &req); err != nil {
		return nil, err
	}
	blob, err := s.d.ExportVault(ctx, req.Passphrase)
	if err != nil {
		return nil, err
	}
	return exportResponse{Data: blob}, nil
}

// importVault replaces the vault with an export sealed under req.Passphrase.
func (s *Server) importVault(ctx context.Context, _ *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	var req vaultRequest
	if err := s.readJSON(w, r, &req); err != nil {
		return nil, err
	}
	if err := s.d.ImportVault(ctx, req.Data, req.Passphrase); err != nil {
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

func (s *Server) getSettings(ctx context.Context, _ *reqInfo, _ http.ResponseWriter, _ *http.Request) (any, error) {
	return s.d.Settings(ctx)
}

// settingsRequest names only the settings to change; an absent field keeps its
// stored value.
type settingsRequest struct {
	LogRetentionDays      *int `json:"log_retention_days,omitzero"`
	ConnectionIdleMinutes *int `json:"connection_idle_minutes,omitzero"`
}

func (s *Server) putSettings(ctx context.Context, _ *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	var req settingsRequest
	if err := s.readJSON(w, r, &req); err != nil {
		return nil, err
	}
	return s.d.UpdateSettings(ctx, daemon.SettingsPatch{LogRetentionDays: req.LogRetentionDays, ConnectionIdleMinutes: req.ConnectionIdleMinutes})
}

// testConnection is Test connection: one login with the profile's credential.
func (s *Server) testConnection(ctx context.Context, _ *reqInfo, _ http.ResponseWriter, r *http.Request) (any, error) {
	return s.d.TestConnection(ctx, r.PathValue("id"))
}

func (s *Server) listRequests(_ context.Context, _ *reqInfo, _ http.ResponseWriter, _ *http.Request) (any, error) {
	return s.d.PendingRequests(), nil
}
