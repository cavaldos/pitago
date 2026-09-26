//go:build !linux

package live

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// lsofTimeout bounds the cwd probe. The probe feeds a picker list, so it must
// never be the reason /live feels broken: a slow or hung lsof degrades to
// fewer non-streamable rows, not to an error.
const lsofTimeout = 2 * time.Second

// resolveCWDsFn is the seam scanPiProcesses uses. It starts out pointing at
// the real platform probe; only tests replace it.
var resolveCWDsFn = resolveCWDsOther

// resolveCWDsOther maps pids to their working directory using one batched lsof.
//
// macOS has no /proc and no ps cwd column, so lsof is the only practical
// source. All pids go into a SINGLE -p list: one call per pid would spawn a
// subprocess per candidate and make the picker crawl.
//
// Failure is not an error. lsof is missing on some minimal installs and
// refuses to inspect other users' processes; either way the caller keeps the
// sessions it can resolve and skips the rest.
func resolveCWDsOther(pids []int) map[int]string {
	out := make(map[int]string, len(pids))
	parts := make([]string, 0, len(pids))
	for _, pid := range pids {
		if pid > 0 {
			parts = append(parts, strconv.Itoa(pid))
		}
	}
	if len(parts) == 0 {
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), lsofTimeout)
	defer cancel()
	// -F selects machine-readable field output, -n suppresses the host column
	// so the path is never mangled into an lsof host:path pair.
	raw, err := exec.CommandContext(ctx, "lsof",
		"-a", "-d", "cwd", "-Fn", "-p", strings.Join(parts, ",")).Output()
	if err != nil {
		return out
	}
	return parseLsofCWDs(string(raw))
}

// parseLsofCWDs reads the p<pid> / fcwd / n<path> triples that
// `lsof -a -d cwd -Fn` emits.
//
// Only a path that directly follows an fcwd marker is taken, so the other
// descriptors lsof may report for the same process cannot be mistaken for a
// working directory. A pid with no fcwd triple is absent from the result and
// the caller skips it.
func parseLsofCWDs(raw string) map[int]string {
	out := make(map[int]string)
	cur := 0
	wantCwd := false
	for _, line := range strings.Split(raw, "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			cur, _ = strconv.Atoi(strings.TrimPrefix(line, "p"))
			wantCwd = false
		case line == "fcwd":
			wantCwd = cur > 0
		case strings.HasPrefix(line, "n") && wantCwd:
			if _, seen := out[cur]; !seen {
				out[cur] = strings.TrimPrefix(line, "n")
			}
			wantCwd = false
		}
	}
	return out
}
