package mcpapp

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mtchen/keeper/internal/types"
)

const (
	choiceRun    = "Run once"
	choiceRefuse = "Refuse"
	dialogKey    = "decision"
)

// elicitation is the dialog for a ticket. A read has no fields, so the client
// shows only Accept and Decline with Enter on Accept. A write has a required
// choice that nothing preselects, so a stray Enter cannot commit it.
func elicitation(t *types.Ticket) *mcp.ElicitParams {
	schema := map[string]any{"type": "object", "properties": map[string]any{}}
	if t.Write {
		schema["properties"] = map[string]any{
			dialogKey: map[string]any{"type": "string", "title": "Decision", "enum": []string{choiceRun, choiceRefuse}},
		}
		schema["required"] = []string{dialogKey}
	}
	return &mcp.ElicitParams{Message: t.Prompt, RequestedSchema: schema}
}

// runs says whether an answer lets the statement run: accept for a read, and
// accept with Run once for a write.
func runs(action, choice string, write bool) bool {
	return action == "accept" && (!write || choice == choiceRun)
}

// refuses says whether an answer refuses the statement. Anything else that does
// not run it, Esc included, leaves the ticket open.
func refuses(action, choice string) bool {
	return action == "decline" || (action == "accept" && choice == choiceRefuse)
}
