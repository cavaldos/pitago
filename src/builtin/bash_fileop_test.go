package builtin

import (
	"encoding/json"
	"strconv"
	"testing"
)

// File-mutating bash commands are labeled [edit:/write: file]; read-only
// commands keep [bash: cmd].
func TestBashFileOp(t *testing.T) {
	cases := []struct {
		cmd      string
		target   string
		edit     bool
		bashFULL bool // "" target → keep [bash: cmd]
	}{
		{cmd: `sed -i 's/a/b/' src/app/x.go`, target: "src/app/x.go", edit: true},
		{cmd: `cd /home/u && sed -i.bak -n '1,5d' file.txt`, target: "file.txt", edit: true},
		{cmd: `echo hi > out.txt`, target: "out.txt"},
		{cmd: `echo hi >> out.txt`, target: "out.txt", edit: true},
		{cmd: `cat list | tee copy.txt`, target: "copy.txt"},
		{cmd: `tail -f log | tee -a keep.log`, target: "keep.log", edit: true},
		{cmd: `cd /x; echo '{"a":1}' > conf.json`, target: "conf.json"},
		// read-only / non-target redirects must stay bash
		{cmd: `sed -n '40,80p' file.txt`, bashFULL: true},
		{cmd: `ps -ef 2>/dev/null | grep pi`, bashFULL: true},
		{cmd: `ls -la; grep -rn x .`, bashFULL: true},
		{cmd: `make build`, bashFULL: true},
	}
	for _, tc := range cases {
		target, edit := bashFileOp(tc.cmd)
		if tc.bashFULL {
			if target != "" {
				t.Errorf("bashFileOp(%q) = (%q,%v), want read-only", tc.cmd, target, edit)
			}
			continue
		}
		if target != tc.target || edit != tc.edit {
			t.Errorf("bashFileOp(%q) = (%q,%v), want (%q,%v)", tc.cmd, target, edit, tc.target, tc.edit)
		}
	}
}

func TestTreeToolBashFileOpLabels(t *testing.T) {
	args := func(cmd string) json.RawMessage {
		return json.RawMessage(`{"command":` + strconv.Quote(cmd) + `}`)
	}
	if got := treeTool("bash", args(`sed -i 's/a/b/' a.go`)); got != "[edit: a.go]" {
		t.Errorf("sed -i label = %q", got)
	}
	if got := treeTool("bash", args(`echo x > a.go`)); got != "[write: a.go]" {
		t.Errorf("> label = %q", got)
	}
	if got := treeTool("bash", args(`ls -la`)); got != "[bash: ls -la]" {
		t.Errorf("read-only label = %q", got)
	}
}
