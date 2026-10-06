package daemon

import (
	"sync"

	"github.com/mtchen/keeper/internal/types"
)

// keptResults is how many of the most recent results the Activity screen can
// show. A result is bounded by the operator's row ceiling, so this bounds the
// daemon's memory.
const keptResults = 200

// results holds the masked results agents received, keyed by audit id, so a
// human reviewing the Activity screen sees exactly what the agent saw.
//
// Memory only. R10b keeps result rows out of the audit log: masking is a
// decision, not a proof, and a value a detector missed must not land on disk.
// A restart loses them, and loses nothing a human needs: every token in them
// stopped meaning anything at the same moment (R3.4d).
type results struct {
	mu    sync.Mutex
	order []string
	byID  map[string]*types.QueryResult
}

func (r *results) keep(res *types.QueryResult) {
	if res == nil || res.AuditID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byID == nil {
		r.byID = map[string]*types.QueryResult{}
	}
	if _, ok := r.byID[res.AuditID]; !ok {
		r.order = append(r.order, res.AuditID)
		if len(r.order) > keptResults {
			delete(r.byID, r.order[0])
			r.order = r.order[1:]
		}
	}
	r.byID[res.AuditID] = res
}

func (r *results) get(auditID string) *types.QueryResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byID[auditID]
}
