package api

import (
	"bytes"
	json "encoding/json/v2"
	"fmt"
	"net/http"
	"time"
)

const heartbeat = 20 * time.Second

// events is CONTRACT §3's SSE stream: approval, request, connection, catalog and
// session. Each subscriber has its own buffered channel and a slow one is
// dropped from rather than allowed to stall the daemon, so a browser tab that
// stopped reading cannot hold up a query.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.fail(w, r, errInternalStream)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, release := s.d.Events().Subscribe()
	defer release()

	ping := time.NewTicker(heartbeat)
	defer ping.Stop()

	var buf bytes.Buffer
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case ev, open := <-ch:
			if !open {
				return
			}
			buf.Reset()
			if err := json.MarshalWrite(&buf, ev.Data, durationAsNano); err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, buf.Bytes()); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
