package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/mtchen/keeper/internal/types"
)

// ListApprovals returns the shared approval queue, oldest first (SPEC R9.1).
// It is shared across every session on the machine, which is why every item
// names its own session.
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

// DecideApproval approves or refuses one queued ticket.
func (c *Client) DecideApproval(ctx context.Context, ticket string, decision ApprovalDecision) error {
	body := struct {
		Decision ApprovalDecision `json:"decision"`
	}{Decision: decision}
	return c.do(ctx, http.MethodPost, "/v1/approvals/"+url.PathEscape(ticket)+"/decide", body, nil)
}
