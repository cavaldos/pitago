package live

import (
	"errors"
	"os"
	"syscall"
)

// pidAlive reports whether pid is still a process we could signal.
//
// This needs two different probes, and neither alone is portable, so it
// combines them rather than guarding a file pair with build tags:
//
//   - Unix: os.FindProcess always succeeds, even for a pid that is long gone,
//     so the only usable evidence is signal 0 (kill(2) with a null signal),
//     which performs the existence and permission checks without delivering
//     anything. ESRCH means gone, EPERM means alive but owned by another user.
//   - Windows: os.FindProcess opens a real process handle and already fails
//     for a dead pid, and Process.Signal rejects every signal except Kill, so
//     signal 0 is not available at all.
//
// os.Process.Signal surfaces the Unix ESRCH as os.ErrProcessDone, which is
// what separates "dead" from "Signal is unsupported here". The handle opened
// by FindProcess is released immediately: the descriptor file is best-effort
// input and must never be the reason a window cannot follow a live session.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false // Windows: the handle could not be opened, so it is gone.
	}
	defer p.Release()
	// NOTE: sigErr must not shadow err here — the final check deliberately
	// inspects the signal result, not FindProcess's.
	sigErr := p.Signal(syscall.Signal(0))
	if sigErr == nil {
		return true // Unix: exists and signalable by us.
	}
	// Anything that is not "the process is done" is either a Windows
	// unsupported-signal error (process exists) or Unix EPERM (exists, owned
	// by someone else) — both mean alive.
	return !errors.Is(sigErr, os.ErrProcessDone)
}
