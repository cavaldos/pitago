package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/pirpc"
)

// First Ctrl+C arms (no quit, no status hijack); second press within 3s
// quits; the disarm tick resets; stale arms/gen re-arm instead of quitting.
func TestDoubleCtrlCQuit(t *testing.T) {
	ctrlC := tea.KeyMsg{Type: tea.KeyCtrlC}

	var m Model
	um, _ := m.Update(ctrlC) // arm (tick cmd ignored — never fires in test)
	m = um.(Model)
	if !m.quitArmed() {
		t.Fatal("first Ctrl+C must arm")
	}
	if m.Status == "press Ctrl+C again to quit" {
		t.Error("arm must not hijack the status line (lives in the sidebar corner)")
	}
	um, cmd := m.Update(ctrlC)
	if cmd == nil {
		t.Fatal("second Ctrl+C must quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("second Ctrl+C must return QuitMsg, got %T", cmd())
	}

	um, _ = m.Update(quitDisarmMsg{gen: m.quitGen})
	m = um.(Model)
	if m.quitArmed() {
		t.Error("disarm tick must reset the arm")
	}

	var m2 Model
	m2.quitArm = time.Now().Add(-10 * time.Second) // stale arm
	um, _ = m2.Update(ctrlC)
	m2 = um.(Model)
	if !m2.quitArmed() {
		t.Error("stale arm must re-arm, not quit")
	}
	um, _ = m2.Update(quitDisarmMsg{gen: m2.quitGen - 1}) // stale gen
	if !um.(Model).quitArmed() {
		t.Error("stale disarm gen must be ignored")
	}
}

// Ctrl+T rotates the thinking level directly (no picker).
func TestCtrlTOpensThinking(t *testing.T) {
	var m Model
	um, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	m = um.(Model)
	if cmd == nil {
		t.Fatal("Ctrl+T must return the thinking cycler")
	}
	if m.Status != "switching thinking…" {
		t.Fatalf("Ctrl+T status = %q", m.Status)
	}
}

func TestQuitArmed(t *testing.T) {
	var m Model
	if m.quitArmed() {
		t.Error("zero arm must not be armed")
	}
	m.quitArm = time.Now()
	if !m.quitArmed() {
		t.Error("fresh arm must be armed")
	}
	m.quitArm = time.Now().Add(-10 * time.Second)
	if m.quitArmed() {
		t.Error("stale arm must not be armed")
	}
}

// Regression: /login → Enter API key → typing + Enter must reach the
// secret confirmer. The secret dialog has no Options (FIdx empty) and
// confirmDialog used to drop Enter for any such dialog, so the typed
// key could never be saved.
func TestSecretEnterReachesConfirmer(t *testing.T) {
	var m Model
	called := false
	m.confirm = map[string]ConfirmFunc{
		"secret": func(mm *Model, d *Dialog, ri int) (tea.Model, tea.Cmd) {
			called = true
			return mm, nil
		},
	}
	m.Dialogs = []*Dialog{{
		Kind: "secret", Title: "API key — groq",
		Filter: "gsk-test-key", LoginProvider: "groq", LoginEnv: "GROQ_API_KEY",
	}}
	_, _ = m.updateDialog(tea.KeyMsg{Type: tea.KeyEnter})
	if !called {
		t.Fatal("Enter on secret dialog did not reach secret confirmer")
	}
}

// /new (SessionResetMsg) must drop the old session identity along with the
// todos: otherwise the next refreshPiTasks reads the OLD session's task
// file and the old list reappears on the new session's sidebar.
func TestNewSessionDropsTodoIdentity(t *testing.T) {
	dir := t.TempDir()
	m := New(nil, dir)
	m.sessionFile = "/tmp/x_2026-09-23T01-38-19-902Z_oldid123.jsonl"
	m.Todos = []TodoItem{{ID: "1", Content: "Old task", Status: TodoPending}}
	// stale store file of the old session
	if err := os.MkdirAll(filepath.Join(dir, ".pi", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := `{"tasks":[{"id":"1","subject":"Old task","status":"pending"}]}`
	if err := os.WriteFile(filepath.Join(dir, ".pi", "tasks", "tasks-oldid123.json"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	um, _ := m.Update(SessionResetMsg{})
	m = um.(Model)
	if len(m.Todos) != 0 {
		t.Fatalf("todos after /new = %+v", m.Todos)
	}
	if m.sessionFile != "" {
		t.Fatalf("sessionFile after /new = %q, want empty", m.sessionFile)
	}
	// with identity dropped, the stale file cannot leak back in
	m.refreshPiTasks()
	if len(m.Todos) != 0 {
		t.Fatalf("stale store leaked after /new: %+v", m.Todos)
	}
}

// get_state re-adopts the current session file (heals identity after /new).
func TestStateRefreshAdoptsSessionFile(t *testing.T) {
	m := New(nil, t.TempDir())
	um, _ := m.Update(stateRefreshMsg{state: pirpc.State{SessionFile: "/tmp/x_newid456.jsonl"}})
	m = um.(Model)
	if m.sessionFile != "/tmp/x_newid456.jsonl" {
		t.Fatalf("sessionFile = %q", m.sessionFile)
	}
}

// Adopting a new session identity must re-read the store at once: otherwise
// the sidebar keeps the old (or empty) list until the next tool event or
// menu step, which looks like a "delayed" create/clear/delete.
func TestStateRefreshAdoptsAndReadsTasks(t *testing.T) {
	dir := t.TempDir()
	m := New(nil, dir)
	m.sessionFile = "/tmp/x_2026-09-23T01-38-19-902Z_oldid123.jsonl"
	m.Todos = []TodoItem{{ID: "1", Content: "Old task", Status: TodoPending}}
	if err := os.MkdirAll(filepath.Join(dir, ".pi", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	fresh := `{"tasks":[{"id":"9","subject":"New task","status":"in_progress"}]}`
	if err := os.WriteFile(filepath.Join(dir, ".pi", "tasks", "tasks-newid456.json"), []byte(fresh), 0o644); err != nil {
		t.Fatal(err)
	}
	um, _ := m.Update(stateRefreshMsg{state: pirpc.State{SessionFile: "/tmp/x_newid456.jsonl"}})
	m = um.(Model)
	if len(m.Todos) != 1 || m.Todos[0].ID != "9" || m.Todos[0].Content != "New task" {
		t.Fatalf("todos after identity adopt = %+v, want the new session file", m.Todos)
	}
}

// Events stay swallowed while a dialog is open, so closing one must
// re-sync the sidebar: task writes that landed meanwhile (e.g. an
// auto-clear during a picked-over turn) surface on close, not "later".
func TestEscCloseDialogRefreshesTasks(t *testing.T) {
	dir := t.TempDir()
	m := New(nil, dir)
	m.sessionFile = "/tmp/x_sid999.jsonl"
	m.Todos = []TodoItem{{ID: "1", Content: "stale", Status: TodoPending}}
	if err := os.MkdirAll(filepath.Join(dir, ".pi", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	fresh := `{"tasks":[{"id":"7","subject":"fresh","status":"pending"}]}`
	if err := os.WriteFile(filepath.Join(dir, ".pi", "tasks", "tasks-sid999.json"), []byte(fresh), 0o644); err != nil {
		t.Fatal(err)
	}
	m.Dialogs = []*Dialog{{Kind: "palette"}}
	um, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m2 := um.(Model)
	if len(m2.Dialogs) != 0 {
		t.Fatalf("esc should close the picker, got %d dialogs", len(m2.Dialogs))
	}
	if len(m2.Todos) != 1 || m2.Todos[0].ID != "7" || m2.Todos[0].Content != "fresh" {
		t.Fatalf("close should re-read the store, got %+v", m2.Todos)
	}
}
