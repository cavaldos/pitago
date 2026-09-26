//go:build linux

package live

import (
	"os"
	"strconv"
)

// resolveCWDsFn is the seam scanPiProcesses uses. It starts out pointing at
// the real platform probe; only tests replace it.
var resolveCWDsFn = resolveCWDsLinux

// resolveCWDsLinux maps pids to their working directory using /proc, which is
// exact and needs no subprocess at all.
//
// A pid we may not inspect (another user's process) fails the readlink and is
// simply absent from the result, which the caller treats as "unresolvable"
// and skips — never guesses.
func resolveCWDsLinux(pids []int) map[int]string {
	out := make(map[int]string, len(pids))
	for _, pid := range pids {
		if pid <= 0 {
			continue
		}
		// /proc/<pid>/cwd is a symlink to the process's working directory.
		target, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
		if err != nil {
			continue
		}
		out[pid] = target
	}
	return out
}
