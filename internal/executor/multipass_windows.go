//go:build windows

package executor

import "os/exec"

// prepareProcess does nothing on Windows: cancelling a command kills its process.
func prepareProcess(cmd *exec.Cmd) {}

// killProcess kills cmd's process.
func killProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
