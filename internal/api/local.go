package api

import (
	"context"
	"net/http"

	"github.com/mtchen/keeper/internal/daemon"
	"github.com/mtchen/keeper/internal/types"
)

// The browser's half of R8.7g. One mechanism, two kinds, and the kind is fixed
// when the request is minted: submit answers an input request, decide answers an
// authorization request, and calling either on the other is a 400.
//
// All three are loopback only. The request id in the path is the capability, so
// it is never logged — the access log records the matched route pattern and
// never the path — and never echoed into an event.

type stateResponse struct {
	Kind types.RequestKind `json:"kind"`
	// Delivered is set only by submit. The token it produced goes to the
	// requesting session's reverse map; this caller never sees it.
	Delivered bool          `json:"delivered,omitzero"`
	Granted   []types.Grant `json:"granted,omitzero"`
	State     string        `json:"state"`
}

type submitBody struct {
	// ConnectionID, when present, must match the request. R8.7c: never silently
	// retarget a token.
	ConnectionID string `json:"connection_id,omitzero"`
	Namespace    string `json:"namespace,omitzero"`
	// Value is the one field in keeper that carries a secret. It is read once,
	// handed to the redactor and never logged, stored in an event, returned in a
	// response or written to the audit log (R8.7a).
	Value string `json:"value"`
}

func (s *Server) submitRequest(ctx context.Context, _ *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	id := r.PathValue("id")
	view, err := s.d.LocalRequest(ctx, id)
	if err != nil {
		return nil, err
	}
	if view.Kind != types.RequestInput {
		return nil, &types.ValidationError{Field: "kind", Reason: "this request takes a decision, not a value"}
	}
	var body submitBody
	if err := s.readJSON(w, r, &body); err != nil {
		return nil, err
	}
	if err := s.d.AnswerInput(ctx, id, body.ConnectionID, body.Namespace, body.Value); err != nil {
		return nil, err
	}
	return stateResponse{Kind: types.RequestInput, Delivered: true, State: "ready"}, nil
}

type decideRequestBody struct {
	// Decision is "grant" or "cancel".
	Decision   string              `json:"decision"`
	Lifetime   types.GrantLifetime `json:"lifetime,omitzero"`
	RowCeiling int                 `json:"row_ceiling,omitzero"`
	Actor      string              `json:"actor,omitzero"`
}

// decideRequest answers an authorization request. Nothing is returned to the
// waiting session: no token is minted and the session gains no value it did not
// have. The reply says what was granted, to whom and for how long, because UI
// §6.2 needs that and "delivered to the session" describes something that did
// not happen here.
func (s *Server) decideRequest(ctx context.Context, rq *reqInfo, w http.ResponseWriter, r *http.Request) (any, error) {
	id := r.PathValue("id")
	view, err := s.d.LocalRequest(ctx, id)
	if err != nil {
		return nil, err
	}
	if view.Kind != types.RequestAuthorization {
		return nil, &types.ValidationError{Field: "kind", Reason: "this request takes a value, not a decision"}
	}
	var body decideRequestBody
	if err := s.readJSON(w, r, &body); err != nil {
		return nil, err
	}
	switch body.Decision {
	case "grant":
		granted, err := s.d.AnswerAuthorization(ctx, id,
			daemon.GrantRequest{Lifetime: body.Lifetime, RowCeiling: body.RowCeiling},
			actorOr(body.Actor, rq.surface))
		if err != nil {
			return nil, err
		}
		return stateResponse{Kind: types.RequestAuthorization, Granted: granted, State: "ready"}, nil
	case "cancel":
		if err := s.d.CancelRequest(id); err != nil {
			return nil, err
		}
		return stateResponse{Kind: types.RequestAuthorization, State: "cancelled"}, nil
	default:
		return nil, &types.ValidationError{Field: "decision", Reason: "must be grant or cancel"}
	}
}

func (s *Server) cancelRequest(ctx context.Context, _ *reqInfo, _ http.ResponseWriter, r *http.Request) (any, error) {
	id := r.PathValue("id")
	view, err := s.d.LocalRequest(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.d.CancelRequest(id); err != nil {
		return nil, err
	}
	return stateResponse{Kind: view.Kind, State: "cancelled"}, nil
}

// requestState serves both halves of one path. An agent polling over the socket
// gets its own request's state and, for a ready input request, its token: the
// session binding is checked and another session is refused (R8.7c). The page
// over loopback gets what it must render — kind, connection, the requesting
// session, the purpose, the path and §9.2's facts — and never a value or a token.
func (s *Server) requestState(ctx context.Context, rq *reqInfo, _ http.ResponseWriter, r *http.Request) (any, error) {
	id := r.PathValue("id")
	if rq.session != nil {
		return s.d.WaitRequest(ctx, rq.session, id, s.waitFor(r))
	}
	if rq.surface != surfaceLoopback {
		return nil, errSessionNeeded
	}
	return s.d.LocalRequest(ctx, id)
}
