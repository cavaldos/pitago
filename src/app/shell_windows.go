//go:build windows

package app

import "os/exec"

// Windows has no process groups to signal the way unix does: the shell is
// killed on its own, and an interrupt is not available.
func shellSetGroup(*exec.Cmd) {}

func shellSignalGroup(cmd *exec.Cmd, interrupt bool) {
	if interrupt || cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}

// shellCanInterrupt reports whether Ctrl+C can stop a running shell line.
const shellCanInterrupt = false
