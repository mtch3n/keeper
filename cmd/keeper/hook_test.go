package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mtchen/keeper/internal/client"
)

var registeredHosts = []string{"db.prod.example.com", "10.1.2.3"}

func TestPlg_PLG_C7_RawClientAtRegisteredHostIsDenied(t *testing.T) {
	cases := []string{
		`psql -h db.prod.example.com -U app app`,
		`psql -hdb.prod.example.com app`,
		`psql --host db.prod.example.com app`,
		`psql --host=db.prod.example.com app`,
		`psql postgres://app:pw@db.prod.example.com:5432/app`,
		`psql postgresql://db.prod.example.com/app -c 'select 1'`,
		`psql "host=db.prod.example.com dbname=app"`,
		`PGHOST=db.prod.example.com psql app`,
		`export PGHOST=10.1.2.3; psql app`,
		`pg_dump -h 10.1.2.3 app > out.sql`,
		`pg_dumpall --host=10.1.2.3`,
		`pgcli -h DB.PROD.EXAMPLE.COM app`,
		`/usr/bin/psql -h 10.1.2.3 app`,
		`sudo -u postgres psql -h 10.1.2.3`,
		`cd /tmp && psql -h 10.1.2.3 app`,
		`echo x | psql -h 10.1.2.3`,
		`bash -c "psql -h 10.1.2.3 app"`,
	}
	for _, c := range cases {
		reason := deniedCommand(c, registeredHosts)
		if !strings.Contains(reason, "keeper") {
			t.Errorf("%q: want a denial that points to keeper, got %q", c, reason)
		}
	}
}

func TestPlg_PLG_C8_UnregisteredHostOrNoDaemonPasses(t *testing.T) {
	cases := []struct {
		command string
		hosts   []string
	}{
		{`psql -h localhost app`, registeredHosts},
		{`psql postgres://u@127.0.0.1/app`, registeredHosts},
		{`psql app`, registeredHosts},
		{`PGHOST=other.example.com psql app`, registeredHosts},
		{`pg_dump -h other.example.com app`, registeredHosts},
		{`echo db.prod.example.com`, registeredHosts},
		{`grep -h 10.1.2.3 file`, registeredHosts},
		{`psql -h db.prod.example.com app`, nil},
		{`psql -h 10.1.2.3 app`, []string{}},
	}
	for _, c := range cases {
		if reason := deniedCommand(c.command, c.hosts); reason != "" {
			t.Errorf("%q with %v: want pass, got %q", c.command, c.hosts, reason)
		}
	}
	var asked bool
	in := hookInput{ToolName: "Bash"}
	in.ToolInput.Command = `psql -h db.prod.example.com app`
	if out := preToolUseHook(in, func() []string { asked = true; return nil }); out != nil || !asked {
		t.Errorf("daemon unreachable: want pass after asking, got %v asked=%v", out, asked)
	}
	in.ToolInput.Command = `ls`
	preToolUseHook(in, func() []string { t.Error("daemon asked for a command that runs no database client"); return nil })
}

func TestPlg_PLG_C9_PersonOnlyKeeperCommandsAreDenied(t *testing.T) {
	cases := []string{
		`keeper approve`,
		`keeper approve 3f2a`,
		`keeper connect`,
		`keeper connection set prod --max-rows 5`,
		`keeper connection rm prod`,
		`keeper host add --name h`,
		`keeper host rm h`,
		`keeper catalog edit prod public.users.email --policy hide`,
		`keeper catalog init prod`,
		`keeper vault export`,
		`keeper update`,
		`./bin/keeper approve`,
		`keeper --json approve`,
		`cd /tmp && keeper approve`,
		`yes | keeper connect`,
		`sh -c "keeper approve"`,
		`env X=1 keeper update --force`,
	}
	for _, c := range cases {
		if reason := deniedCommand(c, nil); reason == "" {
			t.Errorf("%q: want denied", c)
		}
	}
}

func TestPlg_PLG_C10_ReadOnlyKeeperCommandsPass(t *testing.T) {
	cases := []string{
		`keeper version`,
		`keeper doctor`,
		`keeper settings`,
		`keeper activity --limit 5`,
		`keeper connection ls`,
		`keeper catalog ls prod`,
		`keeper daemon status`,
		`echo keeper approve`,
		`grep approve keeper.go`,
		`ls keeper`,
	}
	for _, c := range cases {
		if reason := deniedCommand(c, registeredHosts); reason != "" {
			t.Errorf("%q: want pass, got %q", c, reason)
		}
	}
}

func decode(t *testing.T, out any) map[string]any {
	t.Helper()
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPlg_PLG_C11_KeeperToolsAreAllowedInClaudeCode(t *testing.T) {
	for _, tool := range []string{"query", "explain", "list_connections", "request_input", "get_result"} {
		out := preToolUseHook(hookInput{ToolName: keeperToolPrefix + tool}, nil)
		spec := decode(t, out)["hookSpecificOutput"].(map[string]any)
		if spec["permissionDecision"] != "allow" || spec["hookEventName"] != "PreToolUse" {
			t.Errorf("%s: want allow, got %v", tool, spec)
		}
	}
	codex := hookInput{ToolName: "mcp__keeper__query", TurnID: "t1"}
	if out := preToolUseHook(codex, nil); out != nil {
		t.Errorf("codex: want nothing, got %v", out)
	}
	codex.ToolName = keeperToolPrefix + "query"
	if out := preToolUseHook(codex, nil); out != nil {
		t.Errorf("codex: want nothing, got %v", out)
	}
	if out := preToolUseHook(hookInput{ToolName: "mcp__plugin_other_other__query"}, nil); out != nil {
		t.Errorf("another server: want nothing, got %v", out)
	}
}

func TestPlg_PLG_C12_HookNeverOverridesAUserRule(t *testing.T) {
	inputs := []hookInput{{ToolName: keeperToolPrefix + "query"}, {ToolName: keeperToolPrefix + "describe_connection"}}
	bash := hookInput{ToolName: "Bash"}
	for _, c := range []string{`ls`, `psql -h 10.1.2.3`, `keeper approve`} {
		bash.ToolInput.Command = c
		inputs = append(inputs, bash)
	}
	for _, in := range inputs {
		out := preToolUseHook(in, func() []string { return registeredHosts })
		if out == nil {
			continue
		}
		raw, _ := json.Marshal(out)
		if strings.Contains(string(raw), "updatedPermissions") || strings.Contains(string(raw), `"ask"`) {
			t.Errorf("%v: %s carries more than allow or deny", in, raw)
		}
		decision := decode(t, out)["hookSpecificOutput"].(map[string]any)["permissionDecision"]
		if strings.HasPrefix(in.ToolName, keeperToolPrefix) {
			if decision != "allow" {
				t.Errorf("%s: keeper tools get only allow, got %v", in.ToolName, decision)
			}
		} else if decision != "deny" {
			t.Errorf("%s: a Bash call gets only deny, got %v", in.ToolInput.Command, decision)
		}
	}
}

type fakeDaemon struct {
	connections string
	hosts       string
	approvals   string
}

func serveFake(t *testing.T, d fakeDaemon) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "k.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	reply := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }
	}
	mux.HandleFunc("/v1/version", reply(`{"version":"`+client.Version+`"}`))
	mux.HandleFunc("/v1/connections", reply(d.connections))
	mux.HandleFunc("/v1/hosts", reply(d.hosts))
	mux.HandleFunc("/v1/approvals", reply(d.approvals))
	mux.HandleFunc("/v1/doctor", reply(`{"ui_base":"http://127.0.0.1:7773/?t=x"}`))
	srv := &http.Server{Handler: mux}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	t.Setenv("KEEPER_SOCKET", sock)
}

func TestPlg_PLG_C3_SessionStartNamesProfiles(t *testing.T) {
	serveFake(t, fakeDaemon{connections: `[{"id":"1","name":"app_ro"},{"id":"2","name":"reports"}]`})
	spec := decode(t, sessionStartHook(context.Background()))["hookSpecificOutput"].(map[string]any)
	text, _ := spec["additionalContext"].(string)
	if spec["hookEventName"] != "SessionStart" || !strings.Contains(text, "keeper") ||
		!strings.Contains(text, "app_ro") || !strings.Contains(text, "reports") {
		t.Errorf("context does not name keeper and both profiles: %v", spec)
	}
}

func TestPlg_PLG_C19_SessionStartAddsNothingWithoutDaemon(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "absent.sock")
	t.Setenv("KEEPER_SOCKET", sock)
	if out := sessionStartHook(context.Background()); out != nil {
		t.Errorf("want nothing, got %v", out)
	}
	if _, err := os.Stat(sock); err == nil {
		t.Error("the hook started a daemon")
	}
	serveFake(t, fakeDaemon{connections: `[]`})
	if out := sessionStartHook(context.Background()); out != nil {
		t.Errorf("no profiles: want nothing, got %v", out)
	}
}

func TestPlg_PLG_C15_WaitingReadsTheDaemonAndNeverStartsIt(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "absent.sock")
	t.Setenv("KEEPER_SOCKET", sock)
	if out := waitingHook(context.Background()); out != nil {
		t.Errorf("daemon down: want nothing, got %v", out)
	}
	if _, err := os.Stat(sock); err == nil {
		t.Error("the hook started a daemon")
	}

	two := `[{"ticket_id":"a","session":{},"connection":"c","tier":1,"facts":{},"sql":"select 1","created_at":"2026-01-01T00:00:00Z"},
		{"ticket_id":"b","session":{},"connection":"c","tier":1,"facts":{},"sql":"select 2","created_at":"2026-01-01T00:00:00Z"}]`
	serveFake(t, fakeDaemon{approvals: two})
	got := decode(t, waitingHook(context.Background()))
	if got["waiting"] != float64(2) || got["url"] != "http://127.0.0.1:7773/?t=x" {
		t.Errorf("got %v", got)
	}
}
