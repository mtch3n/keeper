package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/mtchen/keeper/internal/types"
)

// ListApprovals returns the shared approval queue, oldest first (SPEC R9.1).
// It is shared across every session on the machine: `keeper approve` and the
// UI both draw from it, which is why every item names its own session.
func (c *Client) ListApprovals(ctx context.Context) ([]types.ApprovalItem, error) {
	var out []types.ApprovalItem
	if err := c.do(ctx, http.MethodGet, "/v1/approvals", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ApprovalDecision is "approve" or "refuse", per CONTRACT §3.
type ApprovalDecision string

const (
	DecisionApprove ApprovalDecision = "approve"
	DecisionRefuse  ApprovalDecision = "refuse"
)

// GrantParams promotes an approval into a standing or session allow rule
// (SPEC §9.3). Nil means the approval is one-time only.
type GrantParams struct {
	Lifetime   types.GrantLifetime `json:"lifetime"`
	RowCeiling int                 `json:"row_ceiling"`
}

// DecideApproval approves or refuses one queued ticket. keeper approve
// requires an interactive TTY (SPEC §3.4's stated limitation); this method
// itself has no opinion about that — it is cmd/keeper's job to refuse to
// call it from a non-terminal.
func (c *Client) DecideApproval(ctx context.Context, ticket string, decision ApprovalDecision, grant *GrantParams) error {
	body := struct {
		Decision ApprovalDecision `json:"decision"`
		Grant    *GrantParams     `json:"grant,omitzero"`
	}{Decision: decision, Grant: grant}
	return c.do(ctx, http.MethodPost, "/v1/approvals/"+url.PathEscape(ticket)+"/decide", body, nil)
}
