package api_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	json "encoding/json/v2"

	"github.com/mtchen/keeper/internal/daemon"
	"github.com/mtchen/keeper/internal/types"
)

func rigWithUIOff(t *testing.T) *rig {
	t.Helper()
	r := newRigSettings(t, daemon.Config{Version: "test"}, &types.Settings{LogRetentionDays: 30, ConnectionIdleMinutes: 5, UIOff: true})
	return escalate(r)
}

func TestUIIsOnByDefaultAndAVaultWithNoValueIsOn(t *testing.T) {
	var st types.Settings
	if err := json.Unmarshal([]byte(`{"log_retention_days":30,"connection_idle_minutes":5}`), &st); err != nil || st.UIOff {
		t.Fatalf("an old vault's settings read as %+v (%v)", st, err)
	}
	if types.DefaultSettings().UIOff {
		t.Fatal("a fresh vault has the dashboard off")
	}
	r := newRig(t)
	if starts, _ := r.uictl.counts(); starts != 1 {
		t.Fatalf("the listener was bound %d times at start", starts)
	}
	if base, _ := r.d.UISession(); base == "" {
		t.Fatal("no dashboard address while the setting is on")
	}
}

func TestUIOffBindsNoListenerAndDoctorSaysSo(t *testing.T) {
	r := rigWithUIOff(t)
	if starts, _ := r.uictl.counts(); starts != 0 {
		t.Fatalf("the listener was bound while the setting is off")
	}
	var rep struct {
		UIBase string `json:"ui_base"`
	}
	r.socket().mustJSON("GET", "/v1/doctor", nil, &rep)
	if rep.UIBase != "" {
		t.Fatalf("doctor names a dashboard that is off: %q", rep.UIBase)
	}
	resp, raw := r.socket().do("GET", "/v1/ui-session", nil)
	e := decodeError(t, raw)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(e.Action, "keeper settings set ui on") {
		t.Fatalf("keeper ui with the dashboard off: %d %+v", resp.StatusCode, e)
	}
}

func TestTheDashboardTurnsOffAndOnWithoutARestart(t *testing.T) {
	r := newRig(t)
	s := r.socket()
	s.mustJSON("PUT", "/v1/settings", map[string]any{"ui_off": true}, nil)
	if starts, stops := r.uictl.counts(); starts != 1 || stops != 1 {
		t.Fatalf("after off: %d starts, %d stops", starts, stops)
	}
	if base, _ := r.d.UISession(); base != "" {
		t.Fatalf("still an address after off: %q", base)
	}
	s.mustJSON("PUT", "/v1/settings", map[string]any{"ui_off": false}, nil)
	if starts, stops := r.uictl.counts(); starts != 2 || stops != 1 {
		t.Fatalf("after on: %d starts, %d stops", starts, stops)
	}
	var st types.Settings
	s.mustJSON("GET", "/v1/settings", nil, &st)
	if st.UIOff {
		t.Fatal("the setting did not come back on")
	}
	resp, _ := r.agent().do("PUT", "/v1/settings", map[string]any{"ui_off": true})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("an agent session changed the dashboard setting: %d", resp.StatusCode)
	}
}

func TestWithTheDashboardOffNoTextHandsOutAnAddress(t *testing.T) {
	r := rigWithUIOff(t)
	r.pipe.preview = &types.WritePreview{Operation: "UPDATE", RowCount: 3, PreviewedAt: time.Now()}
	a := r.agent()
	var tk types.Ticket
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "UPDATE users SET tier = 'x'"}, &tk)
	if tk.URL != "" || strings.Contains(tk.Prompt, "http") || !strings.Contains(tk.Prompt, "keeper approve") {
		t.Fatalf("the ticket names a page that is not served: url %q prompt %q", tk.URL, tk.Prompt)
	}
	eventually(t, "the notification", func() bool { return len(r.notes.seen()) == 1 })
	if l := r.notes.seen()[0]; l != "" {
		t.Fatalf("the notification carries a link: %q", l)
	}
}

func TestRequestInputNamesTheSettingWhenTheDashboardIsOff(t *testing.T) {
	r := rigWithUIOff(t)
	resp, raw := r.agent().do("POST", "/v1/requests", map[string]any{
		"kind": types.RequestInput, "connection_id": "c1", "namespace": "email",
	})
	e := decodeError(t, raw)
	if resp.StatusCode/100 == 2 || !strings.Contains(e.Action, "keeper settings set ui on") {
		t.Fatalf("request_input with the dashboard off: %d %s", resp.StatusCode, raw)
	}
}
