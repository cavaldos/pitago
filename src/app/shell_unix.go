//go:build !windows

package app

import (
	"os/exec"
	"syscall"
)

// shellSetGroup starts the shell in a session (and so a process group) of
// its own:
//   - the whole tree — the shell plus whatever it is running — can be
//     signalled at once without touching pitago's own group;
//   - the tree has no controlling terminal, so a command that wants one
//     (sudo's password prompt, ssh) fails fast instead of reading keys out
//     from under the TUI or stopping on SIGTTIN and hanging the line.
func shellSetGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// shellSignalGroup sends sig to the shell's process group. The shell traps
// SIGINT (see shellInit), so an interrupt stops the running command and the
// shell itself — with its cwd and variables — survives.
func shellSignalGroup(cmd *exec.Cmd, interrupt bool) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pgid := -cmd.Process.Pid
	if !interrupt {
		_ = syscall.Kill(pgid, syscall.SIGKILL)
		return
	}
	_ = syscall.Kill(pgid, syscall.SIGINT)
	// A stopped job only sees the interrupt once it runs again.
	_ = syscall.Kill(pgid, syscall.SIGCONT)
}

// shellCanInterrupt reports whether Ctrl+C can stop a running shell line.
const shellCanInterrupt = true
