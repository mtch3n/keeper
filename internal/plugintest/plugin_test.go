package plugintest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return m
}

func TestPlg_PLG_C1_BothClientsStartTheSameServerAndHooks(t *testing.T) {
	root := "../../plugin"
	claude := readJSON(t, filepath.Join(root, ".claude-plugin/plugin.json"))
	codex := readJSON(t, filepath.Join(root, ".codex-plugin/plugin.json"))
	// Measured with Codex 0.166: it does not expand ${CLAUDE_PLUGIN_ROOT} and
	// rejects the hooks file's "modules", so the two manifests cannot share
	// mcpServers and hooks files. Only name, version and skills are shared.
	for _, key := range []string{"name", "version", "skills"} {
		if claude[key] == nil || claude[key] != codex[key] {
			t.Errorf("manifests disagree on %q: claude %v, codex %v", key, claude[key], codex[key])
		}
	}
	for _, manifest := range []map[string]any{claude, codex} {
		for _, key := range []string{"mcpServers", "hooks"} {
			path := filepath.Join(root, manifest[key].(string))
			if _, err := os.Stat(path); err != nil {
				t.Errorf("%s points at %s: %v", key, path, err)
			}
		}
	}
	if info, err := os.Stat(filepath.Join(root, claude["skills"].(string))); err != nil || !info.IsDir() {
		t.Errorf("skills does not point at a folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "codex/keeper.md")); err == nil {
		t.Error("plugin/codex/keeper.md must be deleted: the shared skill carries it")
	}

	servers := readJSON(t, filepath.Join(root, ".mcp.json"))["mcpServers"].(map[string]any)
	server := servers["keeper"].(map[string]any)
	if server["command"] != "${CLAUDE_PLUGIN_ROOT}/bin/keeper-mcp-launch" {
		t.Errorf("mcp command %v", server["command"])
	}

	hooks := readJSON(t, filepath.Join(root, "hooks/hooks.json"))
	events := hooks["hooks"].(map[string]any)
	for _, event := range []string{"SessionStart", "PreToolUse"} {
		raw, _ := json.Marshal(events[event])
		if !strings.Contains(string(raw), "keeper hook ") {
			t.Errorf("%s hook does not run `keeper hook`: %s", event, raw)
		}
	}
	if mods := hooks["modules"].([]any); len(mods) != 1 || mods[0] != "./band.tsx" {
		t.Errorf("modules %v", mods)
	}

	if _, err := exec.LookPath("claude"); err != nil {
		t.Log("claude is not on PATH: plugin validate skipped")
		return
	}
	cmd := exec.Command("claude", "plugin", "validate", root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("claude plugin validate: %v\n%s", err, out)
	}
}

func TestPlg_CodexFilesAvoidWhatCodexRejects(t *testing.T) {
	root := "../../plugin"
	codex := readJSON(t, filepath.Join(root, ".codex-plugin/plugin.json"))
	server := readJSON(t, filepath.Join(root, codex["mcpServers"].(string)))["mcpServers"].(map[string]any)["keeper"].(map[string]any)
	if server["command"] != "./bin/keeper-mcp-launch" || server["cwd"] != "." {
		t.Errorf("codex mcp server %v: it must run the launcher relative to the plugin root, Codex does not expand ${CLAUDE_PLUGIN_ROOT}", server)
	}
	hooks := readJSON(t, filepath.Join(root, codex["hooks"].(string)))
	if _, ok := hooks["modules"]; ok {
		t.Error("codex hooks file has modules: Codex rejects the whole file for an unknown field")
	}
	if hooks["hooks"] == nil {
		t.Error("codex hooks file has no hooks")
	}
}

// TestPlg_PLG_C13_PLG_C15_BandTestsPass runs the band's own tests: the count and link
// with no button that clear when decided (C13), and no band and no daemon
// start when the daemon is down (C15).
func TestPlg_PLG_C13_PLG_C15_BandTestsPass(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude is not on PATH")
	}
	out, err := exec.Command("claude", "plugin", "test", "../../plugin").CombinedOutput()
	if err != nil {
		t.Fatalf("claude plugin test: %v\n%s", err, out)
	}
	for _, id := range []string{"PLG-C13", "PLG-C15"} {
		if !strings.Contains(string(out), "(pass) "+id) {
			t.Errorf("%s did not run and pass:\n%s", id, out)
		}
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestPlg_PLG_C2_PluginChangeWithoutVersionBumpFails(t *testing.T) {
	script, _ := filepath.Abs("../../scripts/check-plugin-version.sh")
	dir := t.TempDir()
	write := func(path, content string) {
		full := filepath.Join(dir, path)
		must(t, os.MkdirAll(filepath.Dir(full), 0o755))
		must(t, os.WriteFile(full, []byte(content), 0o644))
	}
	manifests := func(version string) {
		m := `{"name":"keeper","version":"` + version + `"}`
		write("plugin/.claude-plugin/plugin.json", m)
		write("plugin/.codex-plugin/plugin.json", m)
	}
	check := func() error {
		cmd := exec.Command("sh", script, "main")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("%s", out)
		}
		return err
	}

	git(t, dir, "init", "-q", "-b", "main")
	manifests("1.0.0")
	write("plugin/skills/a.md", "one")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "base")
	git(t, dir, "checkout", "-qb", "feature")

	if err := check(); err != nil {
		t.Errorf("no change: want pass, got %v", err)
	}
	write("plugin/skills/a.md", "two")
	git(t, dir, "commit", "-qam", "edit")
	if err := check(); err == nil {
		t.Error("a plugin change with the same version: want fail")
	}
	manifests("1.0.1")
	git(t, dir, "commit", "-qam", "bump")
	if err := check(); err != nil {
		t.Errorf("bumped: want pass, got %v", err)
	}
	write("plugin/.codex-plugin/plugin.json", `{"name":"keeper","version":"1.0.0"}`)
	if err := check(); err == nil {
		t.Error("manifests with different versions: want fail")
	}
	git(t, dir, "checkout", "-q", "--", ".")
	write("README.md", "outside plugin")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "docs")
	if err := check(); err != nil {
		t.Errorf("a change outside plugin: want pass, got %v", err)
	}
}

func TestPlg_ManifestHasNoFieldsClaudeRejects(t *testing.T) {
	m := readJSON(t, "../../plugin/.claude-plugin/plugin.json")
	if _, ok := m["keeper_min_version"]; ok {
		t.Error("keeper_min_version is not a manifest field: claude plugin validate warns; the launcher holds the minimum")
	}
}

func TestPlg_LauncherRefusesAnOldKeeper(t *testing.T) {
	bin := t.TempDir()
	fake := "#!/bin/sh\necho 'client 0.0.1'\n"
	if err := os.WriteFile(filepath.Join(bin, "keeper"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "../../plugin/bin/keeper-mcp-launch")
	cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=" + bin + ":/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "older than") {
		t.Errorf("launcher ran an old keeper: err %v, output %s", err, out)
	}
}
