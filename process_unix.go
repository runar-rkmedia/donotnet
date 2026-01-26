//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// setupProcessGroup configures the command to run in its own process group
func setupProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// Kill the entire process group
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
