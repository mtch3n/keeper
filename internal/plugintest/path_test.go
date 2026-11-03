package plugintest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fakeKeeperInHome(t *testing.T, e *launcherEnv) {
	t.Helper()
	dir := filepath.Dir(e.installed())
	must(t, os.MkdirAll(dir, 0o755))
	must(t, os.WriteFile(e.installed(), []byte(fixtureBinary), 0o755))
}

func TestPLG_PathFallback_LauncherRunsHomeBinAndSaysHowToAddItToPath(t *testing.T) {
	e := newLauncherEnv(t)
	fakeKeeperInHome(t, e)
	out, errOut, err := e.run()
	if err != nil || !strings.Contains(out, "fixture keeper mcp version") {
		t.Fatalf("launcher did not run ~/.local/bin/keeper: %v %q", err, out)
	}
	if !strings.Contains(errOut, "PATH") || !strings.Contains(errOut, ".local/bin") {
		t.Errorf("stderr must say how to add ~/.local/bin to PATH: %q", errOut)
	}
}

func TestPLG_PathFallback_HooksRunHomeBinWhenItIsNotOnPath(t *testing.T) {
	e := newLauncherEnv(t)
	fakeKeeperInHome(t, e)
	raw, err := os.ReadFile("../../plugin/hooks/hooks.json")
	must(t, err)
	var cfg struct {
		Hooks map[string][]struct {
			Hooks []struct{ Command string }
		}
	}
	must(t, json.Unmarshal(raw, &cfg))
	n := 0
	for event, groups := range cfg.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				n++
				cmd := exec.Command("sh", "-c", h.Command)
				cmd.Env = e.env
				out, err := cmd.Output()
				if err != nil || !strings.Contains(string(out), "fixture keeper hook") {
					t.Errorf("%s hook did not run ~/.local/bin/keeper: %v %q", event, err, out)
				}
			}
		}
	}
	if n == 0 {
		t.Fatal("no hooks")
	}
}
