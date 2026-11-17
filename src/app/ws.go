package app

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "pitago/src/tui/ev"
)

type wsFile struct {
	path     string
	add, del int
}

// wsData is the polled workspace section (ok=false when not a git repo).

type wsData struct {
	ok        bool
	branch    string
	files     []wsFile
	more      int
	untracked int
}

// pollWs re-arms the 10s workspace refresh tick.

func (m Model) pollWs() tea.Cmd {
	return tea.Tick(10*time.Second, func(time.Time) tea.Msg { return wsTickMsg{} })
}

// wsRefresh runs git status/numstat off the UI thread.

func (m Model) wsRefresh() tea.Cmd {
	cwd := m.cwd
	return func() tea.Msg {
		return wsMsg{data: readWS(cwd)}
	}
}

func readWS(cwd string) wsData {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	run := func(args ...string) (string, bool) {
		full := append([]string{"-C", cwd}, args...)
		out, err := exec.CommandContext(ctx, "git", full...).Output()
		if err != nil {
			return "", false
		}
		return string(out), true
	}
	branch, ok := run("branch", "--show-current")
	if !ok {
		return wsData{}
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		if sha, ok := run("rev-parse", "--short", "HEAD"); ok {
			branch = "@" + strings.TrimSpace(sha)
		} else {
			branch = "detached"
		}
	}
	var d wsData
	d.ok = true
	d.branch = branch
	if st, ok := run("status", "--porcelain=v1", "--untracked-files=normal"); ok {
		for _, line := range strings.Split(st, "\n") {
			if strings.HasPrefix(line, "??") {
				d.untracked++
			}
		}
	}
	adds, dels := map[string]int{}, map[string]int{}
	for _, args := range [][]string{{"diff", "--numstat"}, {"diff", "--cached", "--numstat"}} {
		ns, ok := run(args...)
		if !ok {
			continue
		}
		for _, line := range strings.Split(ns, "\n") {
			f := strings.Split(line, "\t")
			if len(f) != 3 {
				continue
			}
			path := f[2]
			if i := strings.LastIndex(path, " => "); i >= 0 {
				path = strings.Trim(path[i+4:], "{}")
			}
			var a, dl int
			fmt.Sscanf(f[0], "%d", &a) // "-" (binary) scans as 0
			fmt.Sscanf(f[1], "%d", &dl)
			adds[path] += a
			dels[path] += dl
		}
	}
	for path, a := range adds {
		d.files = append(d.files, wsFile{path: path, add: a, del: dels[path]})
	}
	for path, dl := range dels {
		if _, seen := adds[path]; !seen {
			d.files = append(d.files, wsFile{path: path, del: dl})
		}
	}
	// most churn first, show top 5
	for i := 0; i < len(d.files); i++ {
		for j := i + 1; j < len(d.files); j++ {
			if d.files[j].add+d.files[j].del > d.files[i].add+d.files[i].del {
				d.files[i], d.files[j] = d.files[j], d.files[i]
			}
		}
	}
	if len(d.files) > 5 {
		d.more = len(d.files) - 5
		d.files = d.files[:5]
	}
	return d
}

// events --------------------------------------------------------------------
