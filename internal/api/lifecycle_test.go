package api_test

import (
	json "encoding/json/v2"
	"strings"
	"testing"
	"time"

	"github.com/mtchen/keeper/internal/daemon"
	"github.com/mtchen/keeper/internal/types"
)

func (a *fakeAuditLog) decided(d types.Decision) []types.AuditRecord {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []types.AuditRecord
	for _, r := range a.records {
		if r.Decision == d {
			out = append(out, r)
		}
	}
	return out
}

func TestARefusedStatementIsRecordedWithoutItsLiterals(t *testing.T) {
	for _, sql := range []string{
		`SELECT id AS "alice@example.com" FROM orders WHERE id = 123456789`,
		`DELETE FROM orders WHERE total = 4111111111111111`,
		`UPDATE orders SET note = 'ssn 078-05-1120' /* jane@example.com */`,
	} {
		r := escalatingRig(t)
		a := r.agent()
		var tk types.Ticket
		a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": sql}, &tk)
		r.socket().mustJSON("POST", "/v1/approvals/"+tk.ID+"/decide", map[string]any{"decision": "refuse", "actor": "ming"}, nil)

		recs := r.alog.decided(types.DecisionRefused)
		if len(recs) != 1 {
			t.Fatalf("%q: %d refused records", sql, len(recs))
		}
		for _, leak := range []string{"alice", "123456789", "4111", "078", "jane", "ssn"} {
			if strings.Contains(recs[0].Statement, leak) {
				t.Errorf("%q: the record keeps %q: %q", sql, leak, recs[0].Statement)
			}
		}
	}
}

func TestACancelledTicketAndAClosedSessionAreRecordedWithoutLiterals(t *testing.T) {
	r := escalatingRig(t)
	a := r.agent()
	var tk types.Ticket
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "DELETE FROM orders WHERE total = 4111111111111111"}, &tk)
	a.close()
	eventually(t, "the closed session's ticket to be recorded as cancelled", func() bool {
		return len(r.alog.decided(types.DecisionCancelled)) == 1
	})
	if got := r.alog.decided(types.DecisionCancelled)[0].Statement; strings.Contains(got, "4111") {
		t.Errorf("the record keeps a literal: %q", got)
	}
}

func TestAnExpiredTicketIsRecordedButARunningApprovedOneIsNotExpired(t *testing.T) {
	r := newRigWith(t, daemon.Config{Version: "test", TicketTTL: 40 * time.Millisecond, Sweep: 10 * time.Millisecond})
	r.pipe.escalate = true
	r.pipe.hold = make(chan struct{})
	r.pipe.ran = make(chan struct{}, 4)
	a := r.agent()

	var running, idle types.Ticket
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT 1"}, &running)
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT 2"}, &idle)
	r.socket().mustJSON("POST", "/v1/approvals/"+running.ID+"/decide", map[string]any{"decision": "approve", "actor": "ming"}, nil)
	<-r.pipe.ran

	eventually(t, "the idle ticket to expire", func() bool { return len(r.alog.decided(types.DecisionExpired)) == 1 })
	time.Sleep(60 * time.Millisecond)
	var got struct {
		State string `json:"state"`
	}
	a.mustJSON("GET", "/v1/tickets/"+running.ID, nil, &got)
	if got.State != string(types.TicketApproved) {
		t.Fatalf("a running approved statement is %q, want approved", got.State)
	}
	close(r.pipe.hold)
	a.mustJSON("GET", "/v1/tickets/"+running.ID+"?wait_ms=2000", nil, &got)
	if got.State != string(types.TicketReady) {
		t.Fatalf("the statement did not finish: %q", got.State)
	}
}

func TestAClosedSessionsApprovedStatementIsCancelled(t *testing.T) {
	r := escalatingRig(t)
	r.pipe.hold = make(chan struct{})
	r.pipe.ran = make(chan struct{}, 1)
	r.pipe.ctxErr = make(chan error, 1)
	a := r.agent()
	var tk types.Ticket
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT 1"}, &tk)
	r.socket().mustJSON("POST", "/v1/approvals/"+tk.ID+"/decide", map[string]any{"decision": "approve", "actor": "ming"}, nil)
	<-r.pipe.ran
	a.close()
	select {
	case <-r.pipe.ctxErr:
	case <-time.After(3 * time.Second):
		t.Fatal("the statement of a closed session kept running")
	}
}

func TestTheApprovedRunKnowsWhatTheWritePreviewCounted(t *testing.T) {
	r := escalatingRig(t)
	r.pipe.facts.StatementType = "UPDATE"
	r.pipe.preview = &types.WritePreview{Operation: "UPDATE", RowCount: 4182, PreviewedAt: time.Now()}
	a := r.agent()
	var tk types.Ticket
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "UPDATE users SET tier = 'x'"}, &tk)
	r.socket().mustJSON("POST", "/v1/approvals/"+tk.ID+"/decide", map[string]any{"decision": "approve", "actor": "ming"}, nil)
	a.mustJSON("GET", "/v1/tickets/"+tk.ID+"?wait_ms=2000", nil, nil)
	r.pipe.mu.Lock()
	defer r.pipe.mu.Unlock()
	if len(r.pipe.previewed) != 1 || r.pipe.previewed[0] != 4182 {
		t.Errorf("previewed rows seen by the run: %v", r.pipe.previewed)
	}
}

func TestAnUnpreviewedEstimateIsNotPassedOnAsAPreviewedCount(t *testing.T) {
	r := escalatingRig(t)
	r.pipe.facts.StatementType = "UPDATE"
	r.pipe.preview = &types.WritePreview{Operation: "UPDATE", RowCount: 99, Unpreviewed: true, PreviewedAt: time.Now()}
	a := r.agent()
	var tk types.Ticket
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "UPDATE users SET tier = 'x'"}, &tk)
	r.socket().mustJSON("POST", "/v1/approvals/"+tk.ID+"/decide", map[string]any{"decision": "approve", "actor": "ming"}, nil)
	a.mustJSON("GET", "/v1/tickets/"+tk.ID+"?wait_ms=2000", nil, nil)
	r.pipe.mu.Lock()
	defer r.pipe.mu.Unlock()
	if len(r.pipe.previewed) != 1 || r.pipe.previewed[0] != 0 {
		t.Errorf("previewed rows seen by the run: %v", r.pipe.previewed)
	}
}

func TestTheApprovalEventsCarryNoStatementAndNoRow(t *testing.T) {
	r := escalatingRig(t)
	r.pipe.facts.StatementType = "UPDATE"
	r.pipe.preview = &types.WritePreview{
		Operation: "UPDATE", RowCount: 1, PreviewedAt: time.Now(),
		Changes: &types.WriteChanges{Columns: []string{"email"}, Rows: []types.ChangedRow{{Old: []string{"old@example.com"}, New: []string{"new@example.com"}}}},
	}
	ch, release := r.d.Events().Subscribe()
	defer release()
	a := r.agent()
	var tk types.Ticket
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "UPDATE users SET email = 'new@example.com'"}, &tk)
	r.socket().mustJSON("POST", "/v1/approvals/"+tk.ID+"/decide", map[string]any{"decision": "refuse", "actor": "ming"}, nil)

	var seen []string
	deadline := time.After(3 * time.Second)
	for len(seen) < 2 {
		select {
		case ev := <-ch:
			if ev.Type != daemon.EventApproval {
				continue
			}
			b, err := json.Marshal(ev.Data)
			if err != nil {
				t.Fatal(err)
			}
			seen = append(seen, string(b))
		case <-deadline:
			t.Fatalf("saw only %v", seen)
		}
	}
	for _, e := range seen {
		for _, leak := range []string{"example.com", "UPDATE users", "sql", "changes"} {
			if strings.Contains(e, leak) {
				t.Errorf("event %s carries %q", e, leak)
			}
		}
		for _, want := range []string{tk.ID, `"connection":"c1"`} {
			if !strings.Contains(e, want) {
				t.Errorf("event %s lacks %s", e, want)
			}
		}
	}
}

func TestAVaultImportClosesEveryPoolAndCatalog(t *testing.T) {
	r := newRig(t)
	r.socket().mustJSON("POST", "/v1/vault/import", map[string]any{"data": []byte("x"), "passphrase": "p"}, nil)
	if r.replaced.Load() != 1 {
		t.Errorf("the import closed pools and catalogs %d times, want once", r.replaced.Load())
	}
}
