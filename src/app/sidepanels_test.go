package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTodosShapes(t *testing.T) {
	// list-in-args with content+status
	in := `{"todos":[{"id":"1","content":"Write code","status":"in_progress"},{"id":"2","content":"Test","status":"pending"}]}`
	got, ok := parseTodos(json.RawMessage(in))
	if !ok || len(got) != 2 {
		t.Fatalf("parse list-in-args = %v,%v", got, ok)
	}
	if got[0].Status != TodoInProgress || got[1].Status != TodoPending {
		t.Fatalf("statuses = %v %v", got[0].Status, got[1].Status)
	}
	// pi-todo style: text+done boolean in details
	in = `{"todos":[{"text":"Done thing","done":true},{"text":"Next"}]}`
	got, ok = parseTodos(json.RawMessage(in))
	if !ok || len(got) != 2 || got[0].Status != TodoCompleted || got[1].Status != TodoPending {
		t.Fatalf("parse done-boolean = %v,%v", got, ok)
	}
	// bare array + items key
	got, ok = parseTodos(json.RawMessage(`[{"content":"a","status":"completed"}]`))
	if !ok || len(got) != 1 || got[0].Status != TodoCompleted {
		t.Fatalf("parse bare array = %v,%v", got, ok)
	}
	// action-only payload: no list → ok=false
	if _, ok = parseTodos(json.RawMessage(`{"action":"update","text":"x"}`)); ok {
		t.Fatal("action-only payload must not parse")
	}
	// empty list is a valid empty state
	got, ok = parseTodos(json.RawMessage(`{"todos":[]}`))
	if !ok || len(got) != 0 {
		t.Fatalf("empty list = %v,%v", got, ok)
	}
	// manage_todo_list: todoList key + title + dash statuses
	in = `{"operation":"write","todoList":[{"id":1,"title":"Design API","description":"d","status":"not-started"},{"id":2,"title":"Auth","description":"d","status":"in-progress"},{"id":3,"title":"Tests","description":"d","status":"completed"}]}`
	got, ok = parseTodos(json.RawMessage(in))
	if !ok || len(got) != 3 || got[0].Content != "Design API" {
		t.Fatalf("parse todoList+title = %v,%v", got, ok)
	}
	if got[0].Status != TodoPending || got[1].Status != TodoInProgress || got[2].Status != TodoCompleted {
		t.Fatalf("dash statuses = %v %v %v", got[0].Status, got[1].Status, got[2].Status)
	}
	// pi-tasks: subject + tasks key, TaskList text fallback
	got, ok = parseTodos(json.RawMessage(`{"tasks":[{"id":"1","subject":"Do thing","status":"pending"}]}`))
	if !ok || len(got) != 1 || got[0].Content != "Do thing" {
		t.Fatalf("parse tasks+subject = %v,%v", got, ok)
	}
	got, ok = parseTodos(json.RawMessage("#1 [pending] Fix bug\n#2 [in_progress] Write tests"))
	if !ok || len(got) != 2 || got[1].Status != TodoInProgress {
		t.Fatalf("parse TaskList text = %v,%v", got, ok)
	}
	// confirmations are not lists ("Task #1 created...", "Updated task #1 ...")
	for _, s := range []string{"Task #1 created successfully: Fix bug", "Updated task #1 status", "No tasks found"} {
		if _, ok = parseTodos(json.RawMessage(s)); ok {
			t.Fatalf("confirmation %q must not parse", s)
		}
	}
	// content-block envelopes are not todo lists either
	if _, ok = parseTodos(json.RawMessage(`[{"type":"text","text":"hello"}]`)); ok {
		t.Fatal("content blocks must not parse as todos")
	}
}

func TestPiTaskDeltas(t *testing.T) {
	m := New(nil, t.TempDir())
	m.applyPiTaskResult("TaskCreate", `{"subject":"Fix bug","description":"d"}`, "Task #1 created successfully: Fix bug")
	m.applyPiTaskResult("TaskCreate", `{"subject":"Write tests","description":"d"}`, "Task #2 created successfully: Write tests")
	if len(m.Todos) != 2 || m.Todos[0].Content != "Fix bug" {
		t.Fatalf("after creates = %+v", m.Todos)
	}
	m.applyPiTaskResult("TaskUpdate", `{"taskId":"1","status":"in_progress"}`, "Updated task #1 status")
	if m.Todos[0].Status != TodoInProgress {
		t.Fatalf("after update = %+v", m.Todos[0])
	}
	m.applyPiTaskResult("TaskUpdate", `{"taskId":"2","status":"deleted"}`, "Updated task #2 status")
	if len(m.Todos) != 1 || m.Todos[0].ID != "1" {
		t.Fatalf("after delete = %+v", m.Todos)
	}
}

func TestReadPiTasksFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".pi", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := `{"nextId":3,"tasks":[{"id":"1","subject":"Fix bug","status":"in_progress","activeForm":"Fixing bug"},{"id":"2","subject":"Docs","status":"completed"}]}`
	sess := "2026-09-23T01-38-19-902Z_abc123"
	if err := os.WriteFile(filepath.Join(dir, ".pi", "tasks", "tasks-abc123.json"), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := readPiTasks(dir, "/tmp/x_"+sess+".jsonl")
	if !ok || len(got) != 2 {
		t.Fatalf("read session file = %v,%v", got, ok)
	}
	if got[0].Content != "Fix bug" || got[0].Status != TodoInProgress || got[0].SubAct != "Fixing bug" {
		t.Fatalf("item0 = %+v", got[0])
	}
	if got[1].Status != TodoCompleted {
		t.Fatalf("item1 = %+v", got[1])
	}
	// missing store → ok=false (caller keeps RPC state)
	if _, ok := readPiTasks(t.TempDir(), ""); ok {
		t.Fatal("missing store must report ok=false")
	}
	// project-scope fallback
	dir2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir2, ".pi", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir2, ".pi", "tasks", "tasks.json"), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := readPiTasks(dir2, ""); !ok || len(got) != 2 {
		t.Fatalf("read project file = %v,%v", got, ok)
	}
	if id := piTaskSessionID("/tmp/x_" + sess + ".jsonl"); id != "abc123" {
		t.Fatalf("session id = %q", id)
	}
}

func TestReadMcpServers(t *testing.T) {
	dir := t.TempDir()
	cfg := `{"mcpServers":{"alpha":{"command":"x"},"beta":{"disabled":true}},"settings":{"directTools":true}}`
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := `{"version":1,"servers":{"alpha":{"tools":[{"name":"t1","description":"d","inputSchema":{"type":"object"}}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "mcp-cache.json"), []byte(cache), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readMcpServers(dir)
	if len(got) != 2 {
		t.Fatalf("servers = %+v", got)
	}
	// sorted by name: alpha first, connected via cache
	if got[0].Name != "alpha" || !got[0].Connected || got[0].Direct != 1 || got[0].Total != 1 {
		t.Fatalf("alpha = %+v", got[0])
	}
	if got[1].Name != "beta" || !got[1].Disabled {
		t.Fatalf("beta = %+v", got[1])
	}
	if n := readMcpServers(t.TempDir()); len(n) != 0 {
		t.Fatalf("empty dir must yield no servers, got %+v", n)
	}
}

// pi's mcp.json keeps an entry without connecting it via
// `"enabled": false` — that is what /mcp writes when a server is
// disabled, and what updateMcpServerConfig deletes again on enable. The
// panel used to check a `disabled` key pi never writes, so every server
// the user disabled through /mcp showed up as "not connected" here.
func TestReadMcpServersHonoursEnabledFalse(t *testing.T) {
	dir := t.TempDir()
	cfg := `{"mcpServers":{
		"off":{"command":"x","enabled":false},
		"on":{"command":"y"},
		"explicit":{"command":"z","enabled":true}
	}}`
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readMcpServers(dir)
	if len(got) != 3 {
		t.Fatalf("servers = %+v", got)
	}
	// sorted by name: explicit, off, on
	if got[0].Name != "explicit" || got[0].Disabled {
		t.Errorf("enabled:true is the default and must not read as disabled: %+v", got[0])
	}
	if got[1].Name != "off" || !got[1].Disabled {
		t.Errorf(`"enabled": false must read as disabled: %+v`, got[1])
	}
	if got[2].Name != "on" || got[2].Disabled {
		t.Errorf("a plain entry must not read as disabled: %+v", got[2])
	}
}

func TestReadPlugins(t *testing.T) {
	dir := t.TempDir()
	cfg := `{"packages":["npm:pi-lens","npm:@narumitw/pi-plan-mode","git:github.com/sting8k/pi-themes"]}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readPlugins(dir)
	if len(got) != 3 {
		t.Fatalf("plugins = %+v", got)
	}
	// order follows settings.json, names strip the source prefix
	if got[0].Name != "pi-lens" || got[0].Spec != "npm:pi-lens" {
		t.Fatalf("first = %+v", got[0])
	}
	if got[1].Name != "@narumitw/pi-plan-mode" {
		t.Fatalf("scoped = %+v", got[1])
	}
	if got[2].Name != "github.com/sting8k/pi-themes" {
		t.Fatalf("git = %+v", got[2])
	}
	if n := readPlugins(t.TempDir()); len(n) != 0 {
		t.Fatalf("empty dir must yield no plugins, got %+v", n)
	}
}

func TestPluginsSectionToggle(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Side = map[string]bool{SidePlugins: true} // hidden by default
	m.Plugins = []Plugin{{Spec: "npm:pi-lens", Name: "pi-lens"}, {Spec: "npm:pi-foo", Name: "pi-foo"}}
	if !m.showPlugins {
		t.Fatal("PLUGINS must start expanded")
	}
	out := m.buildSidebarContent()
	for _, want := range []string{"PLUGINS (2)", "pi-lens", "pi-foo"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expanded sidebar missing %q", want)
		}
	}
	m.TogglePlugins()
	out = m.buildSidebarContent()
	if !strings.Contains(out, "PLUGINS (2)") {
		t.Fatal("collapsed sidebar must keep the header")
	}
	for _, want := range []string{"pi-lens", "pi-foo"} {
		if strings.Contains(out, want) {
			t.Fatalf("collapsed sidebar must hide %q", want)
		}
	}
	m.TogglePlugins()
	if !strings.Contains(m.buildSidebarContent(), "pi-lens") {
		t.Fatal("second toggle must expand again")
	}
}

func TestSidebarHasMcpTodos(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Side = map[string]bool{SideMCP: true} // hidden by default
	m.MCP = []McpServer{{Name: "alpha", Direct: 1, Total: 2, Tokens: 1234, Connected: true}}
	m.Todos = []TodoItem{{ID: "1", Content: "Write code", Status: TodoInProgress}}
	out := m.buildSidebarContent()
	for _, want := range []string{"MCP Servers", "alpha", "Todos (0/1)", "Write code"} {
		if !strings.Contains(out, want) {
			t.Fatalf("sidebar missing %q", want)
		}
	}
	// empty state mirrors pi-sidebar-tui
	m.MCP, m.Todos = nil, nil
	out = m.buildSidebarContent()
	for _, want := range []string{"MCP Servers", "Todos (0/0)", "(no todos)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("empty sidebar missing %q", want)
		}
	}
}

// piTaskStoreFixture points the model at an isolated session store: the
// agent dir is redirected so a real ~/.pi/agent can never win the lookup.
func piTaskStoreFixture(t *testing.T) (*Model, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(dir, "agent"))
	t.Setenv("PI_AGENT_DIR", filepath.Join(dir, "agent"))
	if err := os.MkdirAll(filepath.Join(dir, ".pi", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := New(nil, dir)
	m.cwd = dir
	m.sessionFile = filepath.Join(dir, "2026-09-23T01-38-19-902Z_abc123.jsonl")
	m.Side = map[string]bool{SideMCP: true} // hidden by default
	m.ready, m.winW, m.winH = true, 100, 24
	return &m, filepath.Join(dir, ".pi", "tasks", "tasks-abc123.json")
}

const piTaskStorePayload = `{"nextId":3,"tasks":[{"id":"1","subject":"Ship fix","status":"in_progress","activeForm":"Shipping"},{"id":"2","subject":"Write docs","status":"pending"}]}`

// pi-tasks 'Clear all' unlinks the session store file instead of leaving an
// empty one. Both renderers are pure functions of m.Todos, so a stale slice
// kept them showing deleted tasks; the refresh must drop the slice itself.
func TestPiTaskClearAllClearsTodosAndWidget(t *testing.T) {
	m, store := piTaskStoreFixture(t)
	if err := os.WriteFile(store, []byte(piTaskStorePayload), 0o644); err != nil {
		t.Fatal(err)
	}
	m.refreshPiTasks()
	if len(m.Todos) != 2 {
		t.Fatalf("store not adopted: %+v", m.Todos)
	}
	if !strings.Contains(stripANSI(m.renderTaskWidget()), "Write docs") {
		t.Fatalf("widget missing task before clear-all: %q", m.renderTaskWidget())
	}
	if !strings.Contains(stripANSI(m.buildSidebarContent()), "Todos (0/2)") {
		t.Fatal("sidebar missing todo rows before clear-all")
	}

	// 'Clear all' → store.clearAll() + deleteSessionFileIfEmpty() → unlink.
	if err := os.Remove(store); err != nil {
		t.Fatal(err)
	}
	m.refreshPiTasks()
	if len(m.Todos) != 0 {
		t.Fatalf("stale todos after clear-all: %+v", m.Todos)
	}
	if m.task.activeID != "" {
		t.Fatalf("runtime task state survived clear-all: %+v", m.task)
	}
	if w := stripANSI(m.renderTaskWidget()); w != "" {
		t.Fatalf("widget survived clear-all: %q", w)
	}
	sidebar := stripANSI(m.buildSidebarContent())
	if strings.Contains(sidebar, "Ship fix") || !strings.Contains(sidebar, "Todos (0/0)") {
		t.Fatalf("sidebar survived clear-all: %q", sidebar)
	}
	view := stripANSI(m.View())
	if strings.Contains(view, "Ship fix") {
		t.Fatalf("view still shows the cleared task:\n%s", view)
	}
}

// The seen flag is the safety rail: a session that never had a store file
// (extension absent/unloaded, PI_TASKS=off, memory scope) keeps its
// RPC-tracked todos, and a store seen in ANOTHER session does not arm it.
func TestPiTaskMissingStoreKeepsTodosWhenNeverSeen(t *testing.T) {
	seed := []TodoItem{{ID: "1", Content: "RPC task", Status: TodoInProgress}}

	t.Run("no store file at all", func(t *testing.T) {
		m, _ := piTaskStoreFixture(t)
		m.Todos = seed
		m.syncTaskRuntime()
		m.refreshPiTasks()
		if len(m.Todos) != 1 || m.Todos[0].Content != "RPC task" {
			t.Fatalf("todos dropped without a store: %+v", m.Todos)
		}
	})

	t.Run("PI_TASKS=off", func(t *testing.T) {
		m, store := piTaskStoreFixture(t)
		if err := os.WriteFile(store, []byte(piTaskStorePayload), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PI_TASKS", "off")
		m.Todos = seed
		m.syncTaskRuntime()
		m.refreshPiTasks()
		if len(m.Todos) != 1 || m.Todos[0].Content != "RPC task" {
			t.Fatalf("PI_TASKS=off wiped RPC todos: %+v", m.Todos)
		}
	})

	t.Run("memory scope", func(t *testing.T) {
		m, _ := piTaskStoreFixture(t)
		cfg := filepath.Join(m.cwd, ".pi", "tasks-config.json")
		if err := os.WriteFile(cfg, []byte(`{"taskScope":"memory"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		m.Todos = seed
		m.syncTaskRuntime()
		m.refreshPiTasks()
		if len(m.Todos) != 1 || m.Todos[0].Content != "RPC task" {
			t.Fatalf("memory scope wiped RPC todos: %+v", m.Todos)
		}
	})

	t.Run("store seen in another session", func(t *testing.T) {
		m, store := piTaskStoreFixture(t)
		if err := os.WriteFile(store, []byte(piTaskStorePayload), 0o644); err != nil {
			t.Fatal(err)
		}
		m.refreshPiTasks() // arms the flag for THIS session
		if len(m.Todos) != 2 {
			t.Fatalf("store not adopted: %+v", m.Todos)
		}
		if err := os.Remove(store); err != nil {
			t.Fatal(err)
		}
		m.sessionFile = filepath.Join(m.cwd, "2026-09-23T01-38-19-902Z_other99.jsonl")
		m.Todos = seed
		m.syncTaskRuntime()
		m.refreshPiTasks()
		if len(m.Todos) != 1 || m.Todos[0].Content != "RPC task" {
			t.Fatalf("session switch inherited the seen flag: %+v", m.Todos)
		}
	})
}

// Project scope leaves an EMPTY file behind on clear-all; that path already
// worked and must not regress with the session-file unlink case.
func TestPiTaskProjectScopeEmptyStoreStillClears(t *testing.T) {
	m, _ := piTaskStoreFixture(t)
	project := filepath.Join(m.cwd, ".pi", "tasks", "tasks.json")
	if err := os.WriteFile(project, []byte(piTaskStorePayload), 0o644); err != nil {
		t.Fatal(err)
	}
	m.sessionFile = "" // project scope has no per-session file
	m.refreshPiTasks()
	if len(m.Todos) != 2 {
		t.Fatalf("project store not adopted: %+v", m.Todos)
	}
	if err := os.WriteFile(project, []byte(`{"tasks":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m.refreshPiTasks()
	if len(m.Todos) != 0 {
		t.Fatalf("empty project store must clear todos: %+v", m.Todos)
	}
	if w := stripANSI(m.renderTaskWidget()); w != "" {
		t.Fatalf("widget survived project clear-all: %q", w)
	}
}

// The extension menu answers through answerDialog, which used to skip the
// refresh entirely — the store write was the only signal of a clear-all.
func TestPiTaskAnswerDialogRefreshesAfterClearAll(t *testing.T) {
	m, store := piTaskStoreFixture(t)
	if err := os.WriteFile(store, []byte(piTaskStorePayload), 0o644); err != nil {
		t.Fatal(err)
	}
	m.refreshPiTasks()
	if len(m.Todos) != 2 {
		t.Fatalf("store not adopted: %+v", m.Todos)
	}
	if err := os.Remove(store); err != nil {
		t.Fatal(err)
	}
	m.Dialogs = []*Dialog{{ID: "d1", Kind: "ui", Method: "menu", Options: []string{"Clear all"}}}
	m.answerDialog(m.Dialogs[0], 0)
	if len(m.Todos) != 0 {
		t.Fatalf("answerDialog left stale todos: %+v", m.Todos)
	}
	if len(m.Dialogs) != 0 {
		t.Fatalf("dialog not popped: %+v", m.Dialogs)
	}
}
