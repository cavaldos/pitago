package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	"pitago/src/pirpc"
)

func TestLoadPiTaskFilePreservesWidgetFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	payload := `{"tasks":[{"id":"1","subject":"Done","status":"completed","blockedBy":[],"createdAt":1790339666573,"updatedAt":1790328774268},{"id":"2","subject":"Run","status":"in_progress","activeForm":"Running","blockedBy":["1"],"createdAt":1790339666573,"updatedAt":1790328774268}]}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := loadPiTaskFile(path)
	if !ok || len(got) != 2 {
		t.Fatalf("loadPiTaskFile = %+v, %v", got, ok)
	}
	if got[0].CreatedAt != 1790339666573 || got[0].UpdatedAt != 1790328774268 {
		t.Fatalf("timestamps not preserved: %+v", got[0])
	}
	if len(got[1].BlockedBy) != 1 || got[1].BlockedBy[0] != "1" || got[1].SubAct != "Running" {
		t.Fatalf("task widget fields not preserved: %+v", got[1])
	}
}

func TestRenderTaskWidgetParity(t *testing.T) {
	m := New(nil, t.TempDir())
	m.winW = 100
	m.Todos = []TodoItem{
		{ID: "1", Content: "Finish design", Status: TodoCompleted},
		{ID: "2", Content: "Implement widget", Status: TodoInProgress, SubAct: "Implementing widget"},
		{ID: "3", Content: "Ship it", Status: TodoPending, BlockedBy: []string{"1", "2"}},
	}
	m.task = taskRuntime{activeID: "2", startedAt: time.Now().Add(-14*time.Minute - 8*time.Second), inputTokens: 8300, outputTokens: 2800}
	panel := stripANSI(m.renderTaskWidget())
	want := []string{
		"● 3 tasks (1 done, 1 in progress, 1 open)",
		"  ✔ #1 Finish design",
		"  ✳ #2 Implementing widget… (14m 8s · ↑ 8.3k ↓ 2.8k)",
		"  ◻ #3 Ship it › blocked by #2",
	}
	rows := strings.Split(panel, "\n")
	if len(rows) != 4 {
		t.Fatalf("task widget rows = %#v", rows)
	}
	for i := range want {
		if got := strings.TrimRight(rows[i], " "); got != want[i] {
			t.Fatalf("task widget row %d = %q, want %q", i, got, want[i])
		}
	}
	if strings.Contains(panel, "╭") || strings.Contains(panel, "╰") {
		t.Fatalf("task widget must be borderless: %q", panel)
	}
}

func TestTaskUsageUsesFinalMessageEnd(t *testing.T) {
	m := New(nil, t.TempDir())
	// pi deliberately drops usage received before a task becomes active.
	m.applyMessageEnd([]byte(`{"message":{"role":"assistant","usage":{"input":100,"output":50},"content":[]}}`))
	m.Todos = []TodoItem{{ID: "2", Content: "Run", Status: TodoInProgress, UpdatedAt: time.Now().UnixMilli()}}
	m.syncTaskRuntime()
	if m.task.inputTokens != 0 || m.task.outputTokens != 0 {
		t.Fatalf("pre-active usage must be dropped: %d in / %d out", m.task.inputTokens, m.task.outputTokens)
	}

	// A text-bearing message is counted once from its authoritative end event.
	m.applyMessageEnd([]byte(`{"message":{"role":"assistant","stopReason":"stop","usage":{"input":8300,"output":2800,"cacheRead":999,"cacheWrite":7},"content":[{"type":"text","text":"done"}]}}`))
	// A tool-only assistant message has no text_start, but still counts.
	m.applyMessageEnd([]byte(`{"message":{"role":"assistant","stopReason":"toolUse","usage":{"input":120,"output":40},"content":[{"type":"toolCall","id":"t1","name":"TaskUpdate","arguments":{}}]}}`))
	// Streaming snapshots are ignored, including a nonzero text_start usage.
	m.applyDelta([]byte(`{"usage":{"input":999999,"output":999999},"assistantMessageEvent":{"type":"text_start"}}`))
	if m.task.inputTokens != 8420 || m.task.outputTokens != 2840 {
		t.Fatalf("finalized usage totals = %d in / %d out", m.task.inputTokens, m.task.outputTokens)
	}
	if !strings.Contains(stripANSI(m.renderTaskWidget()), "↑ 8.4k ↓ 2.8k") {
		t.Fatalf("finalized usage did not render: %q", stripANSI(m.renderTaskWidget()))
	}
}

func TestActiveTaskStartsSharedPetTick(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Todos = []TodoItem{{ID: "1", Content: "Run", Status: TodoInProgress}}
	m.syncTaskRuntime()
	if cmd := m.ensureTaskTick(); cmd == nil || !m.pet.ticking {
		t.Fatal("active task must start the existing 500ms tick loop")
	}
	m.pet.tick = 1
	tm, next := m.Update(petTickMsg{})
	m = tm.(Model)
	if next == nil || m.pet.tick != 2 {
		t.Fatalf("active task tick = %d, command nil=%v", m.pet.tick, next == nil)
	}
}

func TestTaskWidgetStaysAboveInputAndFitsBudget(t *testing.T) {
	m := New(nil, t.TempDir())
	m.ready, m.winW, m.winH = true, 80, 24
	m.baseVpH, m.vp = 17, viewport.New(78, 17)
	m.Todos = []TodoItem{{ID: "1", Content: "Active", Status: TodoInProgress}}
	m.syncTaskRuntime()
	view := stripANSI(m.View())
	taskAt, inputAt := strings.Index(view, "1 tasks"), strings.Index(view, "ready ·")
	if taskAt < 0 || inputAt < 0 || taskAt > inputAt {
		t.Fatalf("task block must sit above input: task=%d input=%d\n%s", taskAt, inputAt, view)
	}
	if h := lipgloss.Height(m.View()); h > m.winH {
		t.Fatalf("task block overflowed terminal: %d > %d", h, m.winH)
	}
}

func TestTaskWidgetOverflow(t *testing.T) {
	m := New(nil, t.TempDir())
	m.winW = 80
	for i := 1; i <= 12; i++ {
		m.Todos = append(m.Todos, TodoItem{ID: string(rune('0' + i)), Content: "Task", Status: TodoPending})
	}
	panel := stripANSI(m.renderTaskWidget())
	if !strings.Contains(panel, "… and 2 more") {
		t.Fatalf("missing task overflow row: %q", panel)
	}
}

// The above-editor task widget duplicates the sidebar Todos panel and claims
// chat rows right above the input, so it needs a real off switch. The field is
// stored inverted so a zero Model{} keeps upstream behaviour (widget shown).
func TestTaskWidgetPrefHidesWidget(t *testing.T) {
	m := New(nil, t.TempDir())
	m.prefsPath = filepath.Join(t.TempDir(), "prefs.json")
	m.Todos = []TodoItem{{ID: "1", Content: "a task", Status: TodoPending}}

	if got := m.renderTaskWidget(); got == "" {
		t.Fatal("widget should render by default (upstream behaviour)")
	}

	m.SetTaskWidget(false)
	if got := m.renderTaskWidget(); got != "" {
		t.Fatalf("widget should be hidden, got %d rows", len(strings.Split(got, "\n")))
	}

	// Turning it off must persist, and turning it back on must too.
	prefs := LoadPrefs(m.prefsPath)
	if !prefs.TaskWidgetOff {
		t.Error("prefs should record taskWidgetOff")
	}
	if LoadPrefs(m.prefsPath).TaskWidgetVisible() {
		t.Error("TaskWidgetVisible should report off")
	}
	m.SetTaskWidget(true)
	if LoadPrefs(m.prefsPath).TaskWidgetOff {
		t.Error("prefs should clear taskWidgetOff")
	}
	if m.renderTaskWidget() == "" {
		t.Error("widget should be back")
	}

	// An empty list still hides it regardless of the pref.
	m.Todos = nil
	if got := m.renderTaskWidget(); got != "" {
		t.Error("no todos means no widget")
	}
}

// The promise the toggle makes is "it stays off next run", which is the
// startup path (Configure) reading taskWidgetOff — not the setter. A prefs
// file written by a previous process must silence a fresh Model.
func TestTaskWidgetPrefSurvivesRestart(t *testing.T) {
	// Configure assigns prefsPath = PrefsPath(), which resolves through
	// os.UserHomeDir, so scoping HOME is what keeps this test off the real
	// ~/.config/pitago/prefs.json.
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := SavePrefs(filepath.Join(home, ".config", "pitago", "prefs.json"), Prefs{TaskWidgetOff: true}); err != nil {
		t.Fatal(err)
	}

	m := New(nil, t.TempDir())
	m.Configure(pirpc.Options{}, "")
	m.Todos = []TodoItem{{ID: "1", Content: "a task", Status: TodoPending}}
	if m.TaskWidgetVisible() {
		t.Error("Configure should have loaded taskWidgetOff")
	}
	if got := m.renderTaskWidget(); got != "" {
		t.Errorf("widget should be hidden after restart, got %d rows", len(strings.Split(got, "\n")))
	}

	// The default (no key in the file) must still show it, or the toggle
	// would silently flip upstream behaviour for existing users.
	home2 := t.TempDir()
	t.Setenv("HOME", home2)
	m2 := New(nil, t.TempDir())
	m2.Configure(pirpc.Options{}, "")
	m2.Todos = []TodoItem{{ID: "1", Content: "a task", Status: TodoPending}}
	if !m2.TaskWidgetVisible() {
		t.Error("absent key must default to the widget shown")
	}
}
