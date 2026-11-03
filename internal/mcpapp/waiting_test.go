package mcpapp

import (
	"strings"
	"testing"
)

func TestWaitingNamesKeeperApproveWhenThereIsNoLink(t *testing.T) {
	out := waiting("t1", "pending", "", "", "")
	if out.Link != "" || !strings.Contains(out.Next, "keeper approve") || strings.Contains(out.Next, "the link") {
		t.Errorf("fallback with the dashboard off: %+v", out)
	}
	out = waiting("t1", "pending", "", "", "http://127.0.0.1:1/r/t1")
	if out.Link == "" || !strings.Contains(out.Next, "the link") {
		t.Errorf("fallback with the dashboard on: %+v", out)
	}
}
