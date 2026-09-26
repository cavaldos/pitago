package app

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"pitago/src/pirpc"
)

func testSessions() []pirpc.SessionInfo {
	return []pirpc.SessionInfo{
		{Path: "/s/aaa.jsonl", Name: "demo", Cwd: "/proj", FirstMessage: "hello world", MessageCount: 10, Modified: time.Now()},
		{Path: "/s/bbb.jsonl", Cwd: "/proj", FirstMessage: "", MessageCount: 0, Modified: time.Now()},
	}
}

// Rows + descs + paths + payload stay parallel; detail carries file,
// activity and the first message.
func TestResumePickerPayload(t *testing.T) {
	msg := resumePickerMsg("current", testSessions(), "/s/aaa.jsonl", "", false)
	if len(msg.Options) != 2 || len(msg.Payload) != 2 || len(msg.Paths) != 2 {
		t.Fatalf("rows out of parallel: %+v", msg)
	}
	if !strings.HasPrefix(msg.Descs[0], "current · ") {
		t.Errorf("current session should be marked: %q", msg.Descs[0])
	}
	for _, want := range []string{"10 msgs", "/s/aaa.jsonl", "hello world"} {
		if !strings.Contains(msg.Payload[0], want) {
			t.Errorf("detail missing %q:\n%s", want, msg.Payload[0])
		}
	}
	if !strings.Contains(msg.Payload[1], "(no messages)") {
		t.Errorf("empty session should note it:\n%s", msg.Payload[1])
	}
}

// Deleting a session splices every parallel slice, payload included.
func TestDeleteResumeSplicesPayload(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", dir) // DeleteSession is confined to the session root
	mk := func(name string) string {
		p := dir + "/" + name
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	pa, pb := mk("aaa.jsonl"), mk("bbb.jsonl")
	list := []pirpc.SessionInfo{
		{Path: pa, Cwd: dir, FirstMessage: "hello world", MessageCount: 10, Modified: time.Now()},
		{Path: pb, Cwd: dir, MessageCount: 0, Modified: time.Now()},
	}
	m := Model{}
	msg := resumePickerMsg("current", list, "/other.jsonl", "", false)
	d := &Dialog{Kind: "sessions", Options: msg.Options, Descs: msg.Descs, Paths: msg.Paths, Payload: msg.Payload}
	d.Reindex()
	m.Dialogs = append(m.Dialogs, d)
	nm, _ := m.DeleteResumeSession(d)
	_ = nm.(*Model)
	if len(d.Options) != 1 || len(d.Payload) != 1 || len(d.Paths) != 1 {
		t.Fatalf("slices out of parallel after delete: %d/%d/%d", len(d.Options), len(d.Payload), len(d.Paths))
	}
	if strings.Contains(d.Payload[0], "hello world") {
		t.Errorf("payload of the deleted session lingered: %q", d.Payload[0])
	}
	if _, err := os.Stat(pa); !os.IsNotExist(err) {
		t.Error("session file should be removed from disk")
	}
}

// Tab scope swap (Replace) reloads the detail column too.
func TestReplaceReloadsPayload(t *testing.T) {
	m := Model{}
	d := &Dialog{Kind: "sessions", Title: "old", Options: []string{"a"}, Descs: []string{"x"}, Paths: []string{"p"}, Payload: []string{"old-detail"}}
	d.Reindex()
	m.Dialogs = append(m.Dialogs, d)
	msg := resumePickerMsg("all", testSessions(), "", "", true)
	nm, _ := m.Update(msg)
	mm := nm.(Model)
	dd := mm.Dialogs[0]
	if len(dd.Payload) != 2 || !strings.Contains(dd.Payload[0], "hello world") {
		t.Errorf("replace should reload payload: %v", dd.Payload)
	}
}

// Two columns: left = names, right = selected detail; footer keeps Tab/Del.
func TestRenderResumeDialog(t *testing.T) {
	m := Model{winW: 120, winH: 40}
	msg := resumePickerMsg("current", testSessions(), "/s/aaa.jsonl", "", false)
	d := &Dialog{Kind: "sessions", Title: "Resume session (current)", Scope: "current",
		Options: msg.Options, Descs: msg.Descs, Paths: msg.Paths, Payload: msg.Payload}
	d.Reindex()
	got := stripANSI(m.renderResumeDialog(d))
	for _, want := range []string{"SESSIONS", "DETAIL", "demo", "10 msgs", "/s/aaa.jsonl", "hello world", "Tab scope", "Del delete", "(1/2 · current)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// the two columns are evenly split: in a data row (border │ left,
	// divider │ middle, border │ right) the left cell is dialog padding
	// (3) + column, the right cell is column + padding (3) + divider gap
	// (1) — strip the chrome and compare the columns themselves
	found := false
	for _, ln := range strings.Split(got, "\n") {
		parts := strings.Split(ln, "│")
		if len(parts) != 4 || !strings.Contains(parts[1], "demo") {
			continue
		}
		found = true
		left, right := lipgloss.Width(parts[1])-4, lipgloss.Width(parts[2])-4
		if diff := left - right; diff < -1 || diff > 1 {
			t.Errorf("columns uneven: left=%d right=%d in %q", left, right, ln)
		}
	}
	if !found {
		t.Errorf("no data row found in:\n%s", got)
	}
}

// Long titles + long filters render the same box as short content.
func TestRenderResumeFixedSize(t *testing.T) {
	m := Model{winW: 100, winH: 40}
	nasty := strings.Repeat("z", 200)
	d := &Dialog{Kind: "sessions", Title: "Resume session (all)", Scope: "all",
		Options: []string{nasty}, Descs: []string{"x"},
		Paths: []string{"p"}, Payload: []string{nasty + "\n" + nasty},
		Filter: strings.Repeat("f", 120)}
	d.Reindex()
	wNasty := trajBoxWidth(t, m.renderResumeDialog(d))
	p := &Dialog{Kind: "sessions", Title: "Resume session (all)", Scope: "all",
		Options: []string{"hi"}, Descs: []string{"1 msgs · now"},
		Paths: []string{"p"}, Payload: []string{"hi"}}
	p.Reindex()
	if wPlain := trajBoxWidth(t, m.renderResumeDialog(p)); wNasty != wPlain {
		t.Errorf("box resizes with content: nasty=%d plain=%d", wNasty, wPlain)
	}
}
