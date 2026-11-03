package plugintest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func readText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func docFiles(t *testing.T) []string {
	t.Helper()
	files := []string{"../../README.md", "../../.claude-plugin/marketplace.json"}
	err := filepath.WalkDir("../../plugin", func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		switch ext := filepath.Ext(p); {
		case strings.HasSuffix(p, "band.tsx"), strings.HasSuffix(p, "band.test.ts"), strings.HasSuffix(p, "index.d.ts"):
		case ext == ".md", ext == ".json", filepath.Base(filepath.Dir(p)) == "bin":
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

var codeSpan = regexp.MustCompile("`[^`\n]+`")
var keeperWords = regexp.MustCompile("(?:^|[\\s;|&(`$])keeper ([a-z][a-z-]*)(?: ([a-z][a-z-]*))?")

// codeOf returns the code fences and inline code spans of a Markdown file.
func codeOf(text string) []string {
	var out []string
	inFence := false
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			out = append(out, line)
		} else {
			out = append(out, codeSpan.FindAllString(line, -1)...)
		}
	}
	return out
}

func TestDocs_PLG_C14_EveryKeeperCommandInTheDocsExists(t *testing.T) {
	main := readText(t, "../../cmd/keeper/main.go")
	run := main[strings.Index(main, "func run("):strings.Index(main, "func usage()")]
	top := map[string]bool{"help": true}
	for _, m := range regexp.MustCompile(`case "([a-z-]+)"`).FindAllStringSubmatch(run, -1) {
		top[m[1]] = true
	}
	usage := main[strings.Index(main, "func usage()"):]
	verbs := map[string]map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^  keeper ([a-z]+)([^\n]*)$`).FindAllStringSubmatch(usage, -1) {
		if verbs[m[1]] == nil {
			verbs[m[1]] = map[string]bool{}
		}
		for _, w := range regexp.MustCompile(`[a-z][a-z-]*`).FindAllString(m[2], -1) {
			verbs[m[1]][w] = true
		}
	}
	for _, name := range []string{"hook", "mcp"} {
		if verbs[name] == nil {
			t.Errorf("usage text does not list keeper %s", name)
		}
	}

	seen := 0
	for _, f := range append(docFiles(t), "../../plugin/skills/keeper/SKILL.md") {
		if !strings.HasSuffix(f, ".md") {
			continue
		}
		for _, line := range codeOf(readText(t, f)) {
			for _, m := range keeperWords.FindAllStringSubmatch(line, -1) {
				seen++
				if !top[m[1]] {
					t.Errorf("%s: `keeper %s` is not a command in cmd/keeper/main.go", f, m[1])
					continue
				}
				if m[2] != "" && len(verbs[m[1]]) > 0 && !verbs[m[1]][m[2]] && !strings.HasPrefix(line, "keeper "+m[1]+" "+m[2]+" ") && strings.Count(line, " ") < 3 {
					t.Errorf("%s: `keeper %s %s` is not in the usage text", f, m[1], m[2])
				}
			}
		}
	}
	if seen < 8 {
		t.Errorf("found only %d keeper commands in the docs", seen)
	}
}

var removed = regexp.MustCompile(`(?i)\b(audits?|findings?|grants?|allow rules?|denylist(ed)?|modes|tiers?|intent|persistent tokens?|rotate-master|host add|connection add|vault unlock|stages|terms|partial|drop|hide_name|set_session_intent|request_authorization|get_authorization_result)\b|keeper_0\.0\.4|keeper-mcp([^-]|$)`)

func TestDocs_PLG_C14_NoRemovedThingIsMentioned(t *testing.T) {
	for _, f := range docFiles(t) {
		text := strings.ReplaceAll(readText(t, f), "database grants", "")
		for i, line := range strings.Split(text, "\n") {
			if m := removed.FindString(line); m != "" {
				t.Errorf("%s:%d mentions %q: %s", f, i+1, m, strings.TrimSpace(line))
			}
		}
	}
	for _, f := range []string{"../../README.md", "../../plugin/README.md"} {
		for _, claim := range []string{"MySQL", "SQLite"} {
			if strings.Contains(readText(t, f), claim) {
				t.Errorf("%s names %s, which keeper does not support", f, claim)
			}
		}
	}
}

func TestDocs_PLG_C14_ReadmeGoesFromInstallToConnectToFirstToken(t *testing.T) {
	text := readText(t, "../../README.md")
	install := strings.Index(text, "/plugin install keeper@keeper")
	connect := strings.Index(text, "keeper connect")
	token := strings.Index(text, "⟨email")
	column := strings.Index(text, "keeper catalog edit")
	if install < 0 || connect < install || token < connect || column < token {
		t.Errorf("order must be plugin install (%d), keeper connect (%d), first token (%d), catalog edit (%d)", install, connect, token, column)
	}
	for _, want := range []string{
		"reads=always", "pg_terminate_backend", "dblink_exec", "pg_read_file", "pg_stat_reset", "STABLE", "length(email)",
		"balance", "Elicitation", "checksum", "guidance, not enforcement", "approval_mode", "keeper catalog edit", "keeper vault export",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("README lacks %q", want)
		}
	}
}

func TestDocs_PLG_C14_SkillsAreCurrent(t *testing.T) {
	skill := readText(t, "../../plugin/skills/keeper/SKILL.md")
	for _, want := range []string{"request_input", "get_result", "params"} {
		if !strings.Contains(skill, want) {
			t.Errorf("keeper skill lacks %q", want)
		}
	}
	install := readText(t, "../../plugin/skills/keeper-install/SKILL.md")
	if !strings.Contains(install, "keeper connect") || !strings.Contains(install, ".local/bin") {
		t.Error("install skill must name keeper connect and ~/.local/bin")
	}
}
