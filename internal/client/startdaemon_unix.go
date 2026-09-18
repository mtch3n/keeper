//go:build !windows

package client

import (
	"os/exec"
	"syscall"
)

// detachProcess puts keeperd in its own session so it survives this
// process exiting (the MCP process or CLI invocation that spawned it).
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
