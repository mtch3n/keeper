package daemon

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

type recordedRun struct {
	name string
	args []string
}

func TestDesktopNotifierPutsTheLinkOnTheCommandLine(t *testing.T) {
	for goos, want := range map[string]string{"linux": "notify-send", "darwin": "osascript"} {
		var got recordedRun
		n := desktopNotifier{goos: goos, run: func(_ context.Context, name string, args ...string) error {
			got = recordedRun{name, args}
			return nil
		}}
		n.Notify(t.Context(), "keeper is waiting", "http://127.0.0.1:1/r/abc")
		if got.name != want || !slices.ContainsFunc(got.args, func(a string) bool { return containsLink(a) }) {
			t.Errorf("%s ran %v", goos, got)
		}
	}
}

func containsLink(s string) bool { return strings.Contains(s, "http://127.0.0.1:1/r/abc") }

func TestDesktopNotifierIgnoresAFailureAndAnUnknownSystem(t *testing.T) {
	calls := 0
	failing := desktopNotifier{goos: "linux", run: func(context.Context, string, ...string) error { calls++; return errors.New("no display") }}
	failing.Notify(t.Context(), "t", "l")
	if calls != 1 {
		t.Errorf("ran %d times", calls)
	}
	other := desktopNotifier{goos: "plan9", run: func(context.Context, string, ...string) error { calls++; return nil }}
	other.Notify(t.Context(), "t", "l")
	if calls != 1 {
		t.Error("an unknown system ran a command")
	}
}

func TestDesktopNotifierWithNoLinkSendsOnlyTheTitle(t *testing.T) {
	var got recordedRun
	n := desktopNotifier{goos: "linux", run: func(_ context.Context, name string, args ...string) error {
		got = recordedRun{name, args}
		return nil
	}}
	n.Notify(t.Context(), "keeper is waiting", "")
	if got.name != "notify-send" || len(got.args) != 2 {
		t.Errorf("ran %v", got)
	}
}
