package daemon

import (
	"slices"
	"sync"
	"time"

	"github.com/mtchen/keeper/internal/types"
)

// Event topics. CONTRACT §3: approval, request, connection, catalog, session.
const (
	EventApproval   = "approval"
	EventRequest    = "request"
	EventConnection = "connection"
	EventCatalog    = "catalog"
	EventSession    = "session"
)

// Event is one server-sent event.
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data,omitzero"`
}

// ApprovalEvent narrates the shared queue. An item that leaves because its
// session disconnected is announced as cancelled rather than simply vanishing
// (UI §2.5); types.ApprovalItem has nowhere to carry a state, so the state
// travels here.
type ApprovalEvent struct {
	Action   string              `json:"action"` // queued, decided, cancelled, expired
	TicketID string              `json:"ticket_id"`
	State    types.TicketState   `json:"state,omitzero"`
	Item     *types.ApprovalItem `json:"item,omitzero"`
}

// GrantEvent narrates the permission list. An allow rule is a standing approval
// (§9.3), so it travels on the approval topic.
type GrantEvent struct {
	Action   string              `json:"action"` // granted, revoked, suspended, expired
	GrantID  string              `json:"grant_id"`
	Path     types.PathRef       `json:"path,omitzero"`
	Lifetime types.GrantLifetime `json:"lifetime,omitzero"`
	Reason   string              `json:"reason,omitzero"`
}

// RequestEvent narrates a local request. It deliberately carries no request id:
// the id is a capability (R8.7c) and the page that needs it already has it in
// its URL.
type RequestEvent struct {
	Action string            `json:"action"` // opened, answered, cancelled, expired
	Kind   types.RequestKind `json:"kind"`
	State  string            `json:"state"`
}

// SessionEvent narrates the session list the approval screen attributes to.
type SessionEvent struct {
	Action  string        `json:"action"` // opened, intent, closed
	Session types.Session `json:"session"`
}

// Hub fans events out to subscribers. Each subscriber has its own buffered
// channel and a slow one is dropped from rather than allowed to stall the
// daemon: an SSE client that stopped reading must not be able to block a query.
type Hub struct {
	mu     sync.Mutex
	next   int
	subs   map[int]chan Event
	closed bool
}

const subscriberBuffer = 64

func newHub() *Hub { return &Hub{subs: map[int]chan Event{}} }

// Subscribe returns a channel of events and the function that releases it.
func (h *Hub) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, subscriberBuffer)
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	id := h.next
	h.next++
	h.subs[id] = ch
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if c, ok := h.subs[id]; ok {
			delete(h.subs, id)
			close(c)
		}
		h.mu.Unlock()
	}
}

// Publish delivers to every subscriber that can take it now and drops for those
// that cannot.
func (h *Hub) Publish(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

func (h *Hub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for id, ch := range h.subs {
		delete(h.subs, id)
		close(ch)
	}
}

func sortByTime[T any](s []T, at func(T) time.Time) {
	slices.SortStableFunc(s, func(a, b T) int { return at(a).Compare(at(b)) })
}

// Subscribers is how many event streams are open. An open dashboard holds one,
// which is how the daemon tells "nobody is here" from "nobody has clicked".
func (h *Hub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}
