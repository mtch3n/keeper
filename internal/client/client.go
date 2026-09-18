// Package client is the daemon client: an HTTP client over the unix socket at
// $XDG_RUNTIME_DIR/keeper.sock that cmd/keeper and cmd/keeper-mcp both use to
// talk to keeperd. See CONTRACT.md §3 for the wire surface this mirrors.
//
// A session is one connection to the socket (SPEC R3.4e), so a Client holds
// exactly one persistent connection for its whole life: the underlying
// [http.Transport] is configured with MaxConnsPerHost=1, keep-alives enabled
// and no idle timeout, so http's own connection pool never has more than one
// unix conn open and never silently closes it between calls. Losing that
// connection loses the session (R3.4d): every ticket, every token in the
// session's reverse map, is gone with it.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/mtchen/keeper/internal/build"
	"io"
	"net"
	"net/http"
	"sync/atomic"

	jsonv2 "encoding/json/v2"

	"github.com/mtchen/keeper/internal/types"
)

// Version is the build/protocol version this client, and the keeper and
// keeper-mcp binaries built alongside it, report in the session handshake.
// keeperd compares it against its own build and refuses on mismatch (SPEC
// §3.4: "a handshake compares versions").
// Version is the client's build version, compared against the daemon's at the
// handshake. Both read the same variable: see internal/build.
var Version = build.Version

// durationAsNano is threaded through every Marshal/Unmarshal call in this
// package. encoding/json/v2 refuses to encode a time.Duration by default;
// v1's nanosecond representation is what the daemon (built to the same
// CONTRACT §2 conventions) uses on the wire.
var durationAsNano = json.FormatDurationAsNano(true)

func marshal(v any) ([]byte, error) {
	return jsonv2.Marshal(v, durationAsNano)
}

func unmarshal(data []byte, v any) error {
	return jsonv2.Unmarshal(data, v, durationAsNano)
}

// Client is a connection to keeperd. Create one with [Dial]; it is not safe
// for the zero value to be used.
type Client struct {
	daemonVersion string
	http          *http.Client
	socketPath    string
	sessionID     string
	generation    *atomic.Int64 // bumped each time the transport dials a new conn
	dialGen       int64         // generation observed at handshake time
	// ready becomes true once Dial's handshake has completed, so staleness
	// tracking never fires against the dial that establishes the session
	// in the first place.
	ready bool
}

// SocketPath is the unix socket this client dialed.
func (c *Client) SocketPath() string { return c.socketPath }

// SessionID is the id keeperd assigned this connection at handshake.
func (c *Client) SessionID() string { return c.sessionID }

// Dial connects to keeperd's unix socket, performs the POST /v1/session
// handshake with the given client identity, and returns a ready client. It
// does not start the daemon; call [StartDaemon] first if the socket may not
// exist yet.
func Dial(ctx context.Context, socketPath string, info types.ClientInfo) (*Client, error) {
	return dial(ctx, socketPath, &info)
}

// DialHuman connects without opening a session.
//
// The daemon tells an agent from a human by whether the socket connection
// carries a session, and that is what gates the human surface: registering a
// connection, accepting a G0 finding, approving, granting, editing the catalog
// and setting limits are CLI and UI actions only (SPEC §6.3, R4.1f). The CLI has
// no use for a session either — it holds no reverse map and mints no tokens —
// so not opening one is both the honest signal and the accurate one.
//
// This is not a security boundary and the CLI does not pretend otherwise: an
// agent with shell access runs as the same OS user and can make the same call.
// SPEC §3.4 states that limitation rather than papering over it.
func DialHuman(ctx context.Context, socketPath string) (*Client, error) {
	return dial(ctx, socketPath, nil)
}

func dial(ctx context.Context, socketPath string, info *types.ClientInfo) (*Client, error) {
	var gen atomic.Int64
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			gen.Add(1)
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
		MaxConnsPerHost:   1,
		DisableKeepAlives: false,
		// A session is one socket connection (R3.4e); the pool must never
		// reclaim it as idle and silently redial on the next call.
		IdleConnTimeout: 0,
	}
	c := &Client{
		http:       &http.Client{Transport: transport},
		socketPath: socketPath,
		generation: &gen,
	}

	if info == nil {
		// Still make one round trip, so a version mismatch and an unreachable
		// daemon are both discovered here rather than on the first real call.
		var health struct {
			Version string `json:"version"`
		}
		if err := c.do(ctx, http.MethodGet, "/v1/version", nil, &health); err != nil {
			return nil, err
		}
		c.daemonVersion = health.Version
		if health.Version != "" && health.Version != Version {
			return nil, &VersionMismatchError{ClientVersion: Version, DaemonVersion: health.Version}
		}
		c.dialGen = c.generation.Load()
		c.ready = true
		return c, nil
	}

	var resp sessionResponse
	if err := c.do(ctx, http.MethodPost, "/v1/session", sessionRequest{Client: *info}, &resp); err != nil {
		return nil, err
	}
	c.daemonVersion = resp.DaemonVersion
	if resp.DaemonVersion != "" && resp.DaemonVersion != Version {
		return nil, &VersionMismatchError{ClientVersion: Version, DaemonVersion: resp.DaemonVersion}
	}
	c.sessionID = resp.SessionID
	c.dialGen = c.generation.Load()
	c.ready = true
	return c, nil
}

// SessionAlive reports whether the transport is still using the connection
// established at Dial. false means keeperd was lost and reconnected
// underneath us — a new session, per R3.4e — and every token or ticket
// this Client's caller was holding is gone (R3.4d). It is always true before
// Dial has finished its own handshake.
func (c *Client) SessionAlive() bool { return !c.ready || c.generation.Load() == c.dialGen }

// Close releases the underlying connection.
func (c *Client) Close() error {
	c.http.CloseIdleConnections()
	return nil
}

type sessionRequest struct {
	Client types.ClientInfo `json:"client"`
}

type sessionResponse struct {
	SessionID string `json:"session_id"`
	// DaemonVersion is compared against Version. Its absence is treated as
	// "no mismatch reported" rather than an error, since the exact handshake
	// payload is keeperd's to define; see the client's package doc for what
	// is authoritative here (CONTRACT §3).
	DaemonVersion string `json:"daemon_version,omitzero"`
}

// do executes one request against keeperd and decodes its response into out.
// body may be nil for a bodyless request; out may be nil to discard the
// response body once the status has been checked.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	data, err := c.doRaw(ctx, method, path, body)
	if err != nil {
		return err
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := unmarshal(data, out); err != nil {
		return fmt.Errorf("keeper: decode response from %s %s: %w", method, path, err)
	}
	return nil
}

// doRaw is like do, but returns the response body verbatim instead of
// unmarshaling it into a caller-provided value. It exists for the few
// responses (query's result-or-ticket union) whose shape can't be known
// until the bytes are inspected.
func (c *Client) doRaw(ctx context.Context, method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		data, err := marshal(body)
		if err != nil {
			return nil, fmt.Errorf("keeper: encode %s %s: %w", method, path, err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://unix"+path, reader)
	if err != nil {
		return nil, fmt.Errorf("keeper: build request %s %s: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.sessionID != "" {
		req.Header.Set("X-Keeper-Session", c.sessionID)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if !c.SessionAlive() {
			return nil, &StaleSessionError{Cause: err}
		}
		return nil, fmt.Errorf("keeper: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, decodeError(resp)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("keeper: read response from %s %s: %w", method, path, err)
	}
	return data, nil
}

// waitMs caps a waiting call's wait_ms at 25000, per R8.7b and §3.2. A
// non-positive value means "don't wait" and is left alone.
func waitMs(ms int) int {
	const cap = 25000
	if ms > cap {
		return cap
	}
	return ms
}

// DaemonVersion is the version the daemon reported at the handshake.
//
// It is kept alongside the client's own so that anything reporting a version can
// report both. The two are compared and refused on a mismatch (SPEC §3.4), which
// makes "what version is keeper" two questions rather than one.
func (c *Client) DaemonVersion() string { return c.daemonVersion }
