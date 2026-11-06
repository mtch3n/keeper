package client

import (
	"fmt"
	"io"
	"net/http"

	"github.com/mtchen/keeper/internal/types"
)

// decodeError turns a non-2xx response into the *types.Error it carries.
// Every error that can reach an agent is a *types.Error at the API boundary
// (CONTRACT §2); the client's job is just to decode it, never to reach past
// it for a raw body.
func decodeError(resp *http.Response) error {
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("keeper: read error response (status %d): %w", resp.StatusCode, err)
	}
	var kerr types.Error
	if err := unmarshal(data, &kerr); err != nil || kerr.Code == "" {
		return fmt.Errorf("keeper: daemon returned status %d: %s", resp.StatusCode, string(data))
	}
	return &kerr
}

// VersionMismatchError is returned by [Dial] when the daemon's build does not
// match this client's. SPEC §3.4: the client must name both versions and
// point at `keeper daemon restart`. keeper mcp settles a mismatch itself when
// nothing waits on a person (UPG-D1, UPG-D2); Waiting says why it did not.
type VersionMismatchError struct {
	ClientVersion string
	DaemonVersion string
	// Waiting is how many approvals, tickets and local requests a restart
	// would cancel, when that is why the daemon was not restarted.
	Waiting int
}

func (e *VersionMismatchError) Error() string {
	msg := fmt.Sprintf("keeper: version mismatch — this client is %s, the running daemon is %s; run `keeper daemon restart`",
		e.ClientVersion, e.DaemonVersion)
	if e.Waiting > 0 {
		msg += fmt.Sprintf(". keeper did not restart it because %d approval(s), ticket(s) or local request(s) are waiting; a restart cancels them", e.Waiting)
	}
	return msg
}

// StaleSessionError means the socket connection this Client held died and
// the transport silently redialed. Per R3.4d/R3.4e a new connection is a new
// session with an empty reverse map: every token the caller was holding is
// now permanently unresolvable, and every query that minted one must be
// re-run. Cause is nil when the redial itself succeeded and only the
// generation counter revealed it.
type StaleSessionError struct {
	Cause error
}

func (e *StaleSessionError) Error() string {
	const msg = "keeper: the daemon connection was lost and reconnected; this is a new session " +
		"(SPEC R3.4d/R3.4e) — every token you were holding is now unresolvable, and every query " +
		"that produced one must be re-run"
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", msg, e.Cause)
	}
	return msg
}

func (e *StaleSessionError) Unwrap() error { return e.Cause }
