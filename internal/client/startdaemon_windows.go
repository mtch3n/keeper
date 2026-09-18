//go:build windows

package client

import (
	"os/exec"
	"syscall"
)

// detachProcess starts keeperd in its own process group and hides its
// console so it survives this process exiting and does not flash a window.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
}
