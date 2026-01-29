//go:build windows

package runner

import (
	"os/exec"
)

// setupProcessGroup configures the command for Windows.
// Windows doesn't support process groups the same way, so we just kill the process directly.
func setupProcessGroup(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		return cmd.Process.Kill()
	}
}
