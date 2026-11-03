package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mtchen/keeper/internal/client"
)

func TestHookDeniesTheDashboardLinkAndTheSettingsChangeToAnAgent(t *testing.T) {
	for _, c := range []string{`keeper ui`, `~/.local/bin/keeper ui /r/abc`, `keeper settings set ui off`, `keeper settings set ui on`} {
		if deniedCommand(c, nil) == "" {
			t.Errorf("%q: want denied", c)
		}
	}
	if reason := deniedCommand(`keeper settings`, nil); reason != "" {
		t.Errorf("reading the settings is denied: %q", reason)
	}
}

func TestTheDashboardLinkKeepsTheSecretInTheFragment(t *testing.T) {
	s := &client.UISession{Base: "http://127.0.0.1:7773", Secret: "sec"}
	if got := uiURL(s, ""); got != "http://127.0.0.1:7773/auth#sec" {
		t.Errorf("link %q", got)
	}
	if got := uiURL(s, "/r/abc"); got != "http://127.0.0.1:7773/auth?next=%2Fr%2Fabc#sec" {
		t.Errorf("link with a page %q", got)
	}
	if got := uiURL(s, "http://evil.example/"); strings.Contains(got, "evil") {
		t.Errorf("a foreign page in the link: %q", got)
	}
}

// keeper prints a link and never opens one: a program that opens a page by
// itself puts the secret in a process list, and a page the person did not ask for.
func TestNothingInKeeperOpensABrowser(t *testing.T) {
	root := "../.."
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git" || d.Name() == ".claude" || d.Name() == "web") {
			return fs.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, bad := range []string{`"xdg-open"`, `"sensible-browser"`, `"open", `, `browser.Open`, `"gio"`, `"rundll32"`} {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s mentions %s", p, bad)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
