//go:build !windows

package workflow

import (
	"os/exec"
	"syscall"
)

// prepareProcess puts cmd in its own process group and makes cancelling it
// kill the whole group, so a command that spawned children (the fake guest, a
// real multipass client) leaves nothing behind.
func prepareProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
}

// killProcess kills cmd's whole process group.
func killProcess(cmd *exec.Cmd) { _ = killProcessGroup(cmd) }

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
