package daemon

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Notifier tells the person at the machine that a ticket is waiting. It is
// best effort: a ticket never depends on it, so it returns nothing.
type Notifier interface {
	Notify(ctx context.Context, title, link string)
}

// DesktopNotifier raises an operating system notification with the ticket's
// local link: notify-send on Linux, osascript on macOS. No binary, no display
// or a failure is ignored.
func DesktopNotifier() Notifier {
	return desktopNotifier{goos: runtime.GOOS, run: runCommand}
}

const notifyTimeout = 3 * time.Second

func runCommand(ctx context.Context, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Run()
}

type desktopNotifier struct {
	goos string
	run  func(ctx context.Context, name string, args ...string) error
}

func (n desktopNotifier) Notify(ctx context.Context, title, link string) {
	switch n.goos {
	case "linux":
		_ = n.run(ctx, "notify-send", "--app-name=keeper", title, link)
	case "darwin":
		quote := strings.NewReplacer(`\`, ``, `"`, ``).Replace
		_ = n.run(ctx, "osascript", "-e", `display notification "`+quote(link)+`" with title "`+quote(title)+`"`)
	}
}
