package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"pitago/src/pirpc"
)

// Sidebar panels mirroring pi-sidebar-tui (MCP Servers + Todos). Todos come
// from two sources: todo/task tool calls over pi's RPC (manage_todo_list,
// Task*), plus the pi-tasks store file on disk (covers /tasks-menu edits,
// which emit no RPC). MCP servers are read from the pi agent dir files.

// Sidebar section keys for the /pitago-setting Sidebar tab (persisted in
// prefs.json under "side"; MCP + Plugins start hidden, the rest show).
const (
	SidePet       = "pet"
	SideSession   = "session"
	SideModel     = "model"
	SideStats     = "stats"
	SideCost      = "cost"
	SideRecent    = "recent"
	SideCommands  = "commands"
	SidePlugins   = "plugins"
	SideMCP       = "mcp"
	SideTodos     = "todos"
	SideWorkspace = "workspace"
)

// sideOrder is the Sidebar tab row order (top-to-bottom like the sidebar).
var sideOrder = []string{
	SidePet, SideSession, SideModel, SideStats, SideCost, SideRecent,
	SideCommands, SidePlugins, SideMCP, SideTodos, SideWorkspace,
}

// sideLabel is the Sidebar tab display name per section key.
func sideLabel(key string) string {
	switch key {
	case SidePet:
		return "Pet"
	case SideSession:
		return "Session"
	case SideModel:
		return "Model & context"
	case SideStats:
		return "Stats"
	case SideCost:
		return "Cost"
	case SideRecent:
		return "Recent models"
	case SideCommands:
		return "Commands"
	case SidePlugins:
		return "Plugins"
	case SideMCP:
		return "MCP servers"
	case SideTodos:
		return "Todos"
	case SideWorkspace:
		return "Workspace"
	}
	return key
}

// SideVisible reports one sidebar section's visibility (explicit toggle or
// the default). Click mapping (recent.go) and rendering (view.go) share it,
// so a hidden section disappears from both.
func (m Model) SideVisible(key string) bool {
	if m.Side != nil {
		if v, ok := m.Side[key]; ok {
			return v
		}
	}
	return DefaultSideVisible(key)
}

// setSideVisible persists one section toggle (the Sidebar tab + /plugins).
func (m *Model) setSideVisible(key string, v bool) {
	if m.Side == nil {
		m.Side = map[string]bool{}
	}
	m.Side[key] = v
	prefs := LoadPrefs(m.prefsPath)
	if prefs.Side == nil {
		prefs.Side = map[string]bool{}
	}
	prefs.Side[key] = v
	_ = SavePrefs(m.prefsPath, prefs)
}

// ToggleSideSection flips one sidebar section (the /pitago-setting Sidebar
// tab): the hub stays open so several sections toggle in one visit.
func (m *Model) ToggleSideSection(key string) {
	m.setSideVisible(key, !m.SideVisible(key))
	if m.ready {
		m.Refresh()
	}
}

type TodoStatus string

const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
)

type TodoItem struct {
	ID       string
	Content  string
	Status   TodoStatus
	SubAct   string // optional sub-action shown after in-progress items
}

type McpServer struct {
	Name      string
	Direct    int
	Total     int
	Tokens    int
	Connected bool
	Disabled  bool
}

// Plugin is one installed pi package (sidebar PLUGINS section). Pi calls
// these "packages" (settings.json packages: npm:... / git:...), listed by
// `pi list`; the sidebar shows the short name.
type Plugin struct {
	Spec string // full spec as in settings.json, e.g. "npm:pi-lens"
	Name string // short display name, e.g. "pi-lens"
}

const todosShowMax = 10 // cap like pi-sidebar-tui's default todosMax

func isTodoTool(name string) bool {
	l := strings.ToLower(name)
	return strings.Contains(l, "todo") || strings.Contains(l, "task")
}

// isPiTaskStoreTool reports the Task* family whose state lives in the
// pi-tasks store file (manage_todo_list keeps message-only state, so live
// file refreshes apply to Task* events only).
func isPiTaskStoreTool(name string) bool {
	return strings.Contains(strings.ToLower(name), "task")
}

// todoContent picks the display text across todo shapes:
// pi-sidebar-tui (content/text), manage_todo_list (title), pi-tasks (subject).
func todoContent(m map[string]any) string {
	for _, k := range []string{"content", "text", "title", "subject", "name", "label"} {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// parseTodos extracts a todo list from a todo tool's payload. Handles the
// full list in the tool input (bare array, or object with
// todos/todoList/items/list/tasks key) and the list in the tool result
// details (e.g. {todos:[{text,done}]} or manage_todo_list's
// {todos:[{title,status}]}). Returns ok=false when no todo array is present
// (action-only payloads), so callers can tell "no list" apart from an
// empty list.
func parseTodos(raw json.RawMessage) ([]TodoItem, bool) {
	t := strings.TrimSpace(string(raw))
	if t == "" || t == "null" {
		return nil, false
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		// pi-tasks TaskList returns plain text ("#1 [pending] subject"):
		// fall back to line parsing so the sidebar still shows something.
		if out, ok := parseTodoLines(t); ok {
			return out, true
		}
		return nil, false
	}
	var arr []any
	switch x := v.(type) {
	case []any:
		arr = x
	case map[string]any:
		for _, k := range []string{"todos", "todoList", "items", "list", "tasks"} {
			if a, ok := x[k].([]any); ok {
				arr = a
				break
			}
		}
		if arr == nil {
			return nil, false
		}
	default:
		return nil, false
	}
	out := make([]TodoItem, 0, len(arr))
	for i, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		// Content-block envelopes ({"type":"text","text":...}) share the
		// "text" key with todo items — never read them as todos.
		if t, _ := m["type"].(string); t != "" {
			switch strings.ToLower(strings.ReplaceAll(t, "_", "")) {
			case "text", "thinking", "image", "toolcall", "toolresult":
				continue
			}
		}
		content := todoContent(m)
		if content == "" {
			continue
		}
		id := fmt.Sprint(i)
		if s, ok := m["id"].(string); ok && s != "" {
			id = s
		} else if f, ok := m["id"].(float64); ok {
			id = strings.TrimSuffix(fmt.Sprintf("%v", f), ".0")
		}
		sub, _ := m["subAction"].(string)
		if sub == "" {
			sub, _ = m["activeForm"].(string)
		}
		out = append(out, TodoItem{ID: id, Content: content, Status: normTodoStatus(m), SubAct: sub})
	}
	// A non-empty array with zero todo-like entries is not a todo list
	// (e.g. a content-block envelope) — report "no list" so callers don't
	// wipe the sidebar. A truly empty array is a valid empty list.
	if len(arr) > 0 && len(out) == 0 {
		return nil, false
	}
	return out, true
}

// parseTodoLines parses pi-tasks TaskList text ("#1 [pending] subject",
// one per line) into items. ok=false when no line matches. Only lines
// starting with an id marker ("#1", "1.", "1:") count, so single-task
// confirmations ("Task #1 created...", "Updated task #1 ...") don't parse.
func parseTodoLines(t string) ([]TodoItem, bool) {
	var out []TodoItem
	for _, line := range strings.Split(t, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// strip leading "#1", "1.", "1:" markers (required: TaskList rows
		// always carry one; confirmations like "Task #1 created" don't
		// start with one and must not match)
		rest := line
		if strings.HasPrefix(rest, "#") {
			rest = strings.TrimSpace(rest[1:])
		}
		i := 0
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i++
		}
		if i == 0 {
			continue
		}
		id := rest[:i]
		rest = strings.TrimSpace(rest[i:])
		rest = strings.TrimLeft(rest, ".):")
		rest = strings.TrimSpace(rest)
		status := TodoPending
		content := rest
		if j := strings.Index(content, "["); j >= 0 {
			if k := strings.Index(content[j:], "]"); k >= 0 {
				status = normTodoStatusStr(content[j+1 : j+k])
				after := strings.TrimSpace(content[j+k+1:])
				if after != "" {
					content = after
				}
			}
		}
		// drop trailing "[blocked by ...]" notes, keep the subject
		if j := strings.Index(content, " [blocked by"); j >= 0 {
			content = strings.TrimSpace(content[:j])
		}
		if content == "" {
			continue
		}
		out = append(out, TodoItem{ID: id, Content: content, Status: status})
	}
	if out == nil {
		return nil, false
	}
	return out, true
}

func normTodoStatus(m map[string]any) TodoStatus {
	if s, ok := m["status"].(string); ok {
		return normTodoStatusStr(s)
	}
	for _, k := range []string{"done", "completed"} {
		if b, ok := m[k].(bool); ok && b {
			return TodoCompleted
		}
	}
	for _, k := range []string{"in_progress", "in-progress", "active"} {
		if b, ok := m[k].(bool); ok && b {
			return TodoInProgress
		}
	}
	return TodoPending
}

// normTodoStatusStr unifies status spellings across extensions:
// in_progress/in-progress/active, completed/done, pending/not-started/...
func normTodoStatusStr(s string) TodoStatus {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "-", "_")) {
	case "in_progress", "active", "inprogress", "working", "started":
		return TodoInProgress
	case "completed", "done", "complete", "finished":
		return TodoCompleted
	default:
		return TodoPending
	}
}

// restoreTodos rebuilds the todo list from get_messages history.
// manage_todo_list carries a FULL snapshot in each result details (last one
// wins); pi-tasks TaskList carries a full list as text (last one wins);
// TaskCreate/TaskUpdate carry single-task deltas replayed in order on top.
func restoreTodos(msgs []pirpc.AgentMessage) []TodoItem {
	type callArgs struct {
		name string
		args string
	}
	argsByID := map[string]callArgs{}
	for _, msg := range msgs {
		if msg.Role != "assistant" {
			continue
		}
		for _, b := range pirpc.BlocksOf(msg.Content) {
			if b.Type == "toolCall" && b.ID != "" && isTodoTool(b.Name) {
				argsByID[b.ID] = callArgs{name: b.Name, args: string(b.Arguments)}
			}
		}
	}
	var cur []TodoItem
	found := false
	applyFull := func(t []TodoItem) {
		cur, found = t, true
	}
	for _, msg := range msgs {
		switch msg.Role {
		case "assistant":
			for _, b := range pirpc.BlocksOf(msg.Content) {
				if b.Type == "toolCall" && isTodoTool(b.Name) && len(b.Arguments) > 0 {
					if t, ok := parseTodos(b.Arguments); ok {
						applyFull(t)
					}
				}
			}
		case "toolResult":
			if !isTodoTool(msg.ToolName) {
				continue
			}
			if len(msg.Details) > 0 {
				if t, ok := parseTodos(msg.Details); ok {
					applyFull(t)
					continue
				}
			}
			if t, ok := parseTodos(msg.Content); ok {
				applyFull(t)
				continue
			}
			text := ""
			matchedBlock := false
			for _, b := range pirpc.BlocksOf(msg.Content) {
				if b.Type == "text" {
					if t, ok := parseTodos(json.RawMessage(strings.TrimSpace(b.Text))); ok {
						applyFull(t)
						matchedBlock = true
					}
					if text == "" {
						text = b.Text
					}
				}
			}
			if matchedBlock {
				continue
			}
			// TaskList-style plain text also parses via parseTodos fallback.
			if text == "" {
				text = strings.TrimSpace(pirpc.TextOf(msg.Content))
			}
			if text != "" {
				if t, ok := parseTodos(json.RawMessage(text)); ok {
					applyFull(t)
					continue
				}
			}
			// single-task delta (TaskCreate/TaskUpdate): needs call args
			ca := argsByID[msg.ToolCallID]
			name := msg.ToolName
			args := ""
			if ca.name != "" {
				name, args = ca.name, ca.args
			}
			if text == "" {
				text = strings.TrimSpace(pirpc.TextOf(msg.Content))
			}
			sm := &Model{Todos: cur}
			if sm.applyPiTaskResult(name, args, text) {
				cur, found = sm.Todos, true
			}
		}
	}
	if !found {
		return nil
	}
	return cur
}

// updateTodosFromRaw tries each candidate payload in order and keeps the
// first one that contains a todo list.
func (m *Model) updateTodosFromRaw(cands ...json.RawMessage) {
	for _, c := range cands {
		if len(c) == 0 {
			continue
		}
		if t, ok := parseTodos(c); ok {
			m.Todos = t
			return
		}
	}
}

// pi-tasks single-task ops carry no full list (TaskCreate returns
// "Task #1 created...", TaskUpdate returns "Updated task #1 ..."), so they
// are applied in place to keep the sidebar live between TaskList refreshes.

// piTaskName matches the @tintinweb/pi-tasks tool names (case-insensitive).
func piTaskName(name, want string) bool {
	return strings.EqualFold(strings.TrimSpace(name), want)
}

// parseTaskID extracts the numeric id from "Task #12 ..." / "#12 [...]".
func parseTaskID(text string) string {
	if i := strings.Index(text, "#"); i >= 0 {
		j := i + 1
		for j < len(text) && text[j] >= '0' && text[j] <= '9' {
			j++
		}
		if j > i+1 {
			return text[i+1 : j]
		}
	}
	return ""
}

// applyPiTaskResult folds one pi-tasks single-task result into m.Todos.
// argsRaw is the tool-call arguments JSON, resultText the tool result text.
// Returns true when the tool was a single-task op (handled or no-op).
func (m *Model) applyPiTaskResult(toolName, argsRaw, resultText string) bool {
	switch {
	case piTaskName(toolName, "TaskCreate"):
		var args struct {
			Subject     string `json:"subject"`
			Description string `json:"description"`
			ActiveForm  string `json:"activeForm"`
		}
		_ = json.Unmarshal([]byte(argsRaw), &args)
		subj := strings.TrimSpace(args.Subject)
		if subj == "" {
			// fall back to the result echo ("Task #1 created successfully: <subj>")
			if i := strings.Index(resultText, ":"); i >= 0 {
				subj = strings.TrimSpace(resultText[i+1:])
			}
		}
		if subj == "" {
			return true
		}
		id := parseTaskID(resultText)
		if id == "" {
			id = fmt.Sprint(len(m.Todos) + 1)
		}
		for _, t := range m.Todos {
			if t.ID == id {
				return true // already tracked (e.g. replayed)
			}
		}
		sub := strings.TrimSpace(args.ActiveForm)
		m.Todos = append(m.Todos, TodoItem{ID: id, Content: subj, Status: TodoPending, SubAct: sub})
		return true
	case piTaskName(toolName, "TaskUpdate"):
		var args struct {
			TaskID      string `json:"taskId"`
			Status      string `json:"status"`
			Subject     string `json:"subject"`
			Description string `json:"description"`
			ActiveForm  string `json:"activeForm"`
		}
		_ = json.Unmarshal([]byte(argsRaw), &args)
		id := strings.TrimSpace(args.TaskID)
		if id == "" {
			id = parseTaskID(resultText)
		}
		if id == "" {
			return true
		}
		if strings.EqualFold(strings.TrimSpace(args.Status), "deleted") {
			kept := m.Todos[:0]
			for _, t := range m.Todos {
				if t.ID != id {
					kept = append(kept, t)
				}
			}
			m.Todos = kept
			return true
		}
		for i, t := range m.Todos {
			if t.ID == id {
				if s := strings.TrimSpace(args.Status); s != "" {
					m.Todos[i].Status = normTodoStatusStr(s)
				}
				if s := strings.TrimSpace(args.Subject); s != "" {
					m.Todos[i].Content = s
				}
				if s := strings.TrimSpace(args.ActiveForm); s != "" {
					m.Todos[i].SubAct = s
				}
				return true
			}
		}
		// unknown id (e.g. created before sidebar tracked): add if we know enough
		if s := strings.TrimSpace(args.Subject); s != "" {
			st := TodoPending
			if a := strings.TrimSpace(args.Status); a != "" {
				st = normTodoStatusStr(a)
			}
			m.Todos = append(m.Todos, TodoItem{ID: id, Content: s, Status: st})
		}
		return true
	}
	return false
}

// rawField pulls one top-level field out of an event envelope
// (e.g. details/result/input that typed structs drop).
func rawField(raw json.RawMessage, key string) json.RawMessage {
	var env map[string]json.RawMessage
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil
	}
	return env[key]
}

// pi-tasks file store --------------------------------------------------------
// @tintinweb/pi-tasks persists tasks to disk (see its task-paths.ts), and
// tasks made via the /tasks menu never emit toolCall messages — the RPC
// alone can't see them. So the sidebar reads the same files the extension
// reads (like MCP/plugins do), with RPC deltas as the live supplement.

// piTaskSessionID derives pi's session id from its session file basename
// (<timestamp>_<id>.jsonl). Empty when the name carries no id.
func piTaskSessionID(sessionFile string) string {
	base := filepath.Base(sessionFile)
	if i := strings.LastIndex(base, "."); i >= 0 {
		base = base[:i]
	}
	i := strings.LastIndex(base, "_")
	if i < 0 {
		return ""
	}
	if id := base[i+1:]; id != "" && id != "-" {
		return id
	}
	return ""
}

// piTasksProjectKey mirrors pi-tasks projectKey(cwd).
func piTasksProjectKey(cwd string) string {
	p := strings.TrimPrefix(filepath.Clean(cwd), "/")
	p = strings.TrimPrefix(p, "\\")
	var b strings.Builder
	b.WriteString("--")
	for _, r := range p {
		if r == '/' || r == '\\' || r == ':' {
			b.WriteByte('-')
		} else {
			b.WriteRune(r)
		}
	}
	b.WriteString("--")
	return b.String()
}

// piTasksScope merges the global + project tasks-config.json (default session).
func piTasksScope(cwd, agentDir string) string {
	scope := ""
	if agentDir != "" {
		if v, _ := readJSONFile(filepath.Join(agentDir, "tasks-config.json"))["taskScope"].(string); v != "" {
			scope = v
		}
	}
	if cwd != "" {
		if v, _ := readJSONFile(filepath.Join(cwd, ".pi", "tasks-config.json"))["taskScope"].(string); v != "" {
			scope = v
		}
	}
	if scope == "" {
		scope = "session"
	}
	return scope
}

// loadPiTaskFile parses one store file: ok=false when missing/unreadable,
// ok=true with the list (possibly empty) when it parses.
func loadPiTaskFile(path string) ([]TodoItem, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var data struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if err := json.Unmarshal(raw, &data); err != nil || data.Tasks == nil {
		return nil, false
	}
	out := make([]TodoItem, 0, len(data.Tasks))
	for i, m := range data.Tasks {
		subj, _ := m["subject"].(string)
		subj = strings.TrimSpace(subj)
		if subj == "" {
			if d, _ := m["description"].(string); strings.TrimSpace(d) != "" {
				subj = strings.TrimSpace(strings.SplitN(d, "\n", 2)[0])
			} else {
				subj = todoContent(m)
			}
		}
		if subj == "" {
			continue
		}
		id := fmt.Sprint(i + 1)
		if s, ok := m["id"].(string); ok && s != "" {
			id = s
		} else if f, ok := m["id"].(float64); ok {
			id = strings.TrimSuffix(fmt.Sprintf("%v", f), ".0")
		}
		sub, _ := m["activeForm"].(string)
		if sub == "" {
			sub, _ = m["subAction"].(string)
		}
		out = append(out, TodoItem{ID: id, Content: subj, Status: normTodoStatus(m), SubAct: strings.TrimSpace(sub)})
	}
	return out, true
}

// readPiTasks loads the pi-tasks store for this session. ok=false when no
// store file exists (memory scope / extension absent) — callers keep the
// RPC-tracked list then. An existing file is authoritative, even when empty.
func readPiTasks(cwd, sessionFile string) ([]TodoItem, bool) {
	if v := strings.TrimSpace(os.Getenv("PI_TASKS")); v == "off" {
		return nil, false
	}
	agentDir := piAgentDir()
	switch piTasksScope(cwd, agentDir) {
	case "memory":
		return nil, false
	case "project":
		if cwd != "" {
			return loadPiTaskFile(filepath.Join(cwd, ".pi", "tasks", "tasks.json"))
		}
		return nil, false
	}
	var cands []string
	if v := strings.TrimSpace(os.Getenv("PI_TASKS")); v != "" {
		switch {
		case strings.HasPrefix(v, "/"):
			cands = append(cands, v)
		case strings.HasPrefix(v, "~/"):
			if h, err := os.UserHomeDir(); err == nil {
				cands = append(cands, filepath.Join(h, v[2:]))
			}
		case strings.HasPrefix(v, "."):
			if cwd != "" {
				cands = append(cands, filepath.Join(cwd, v))
			}
		default:
			cands = append(cands, v)
		}
	}
	if id := piTaskSessionID(sessionFile); id != "" {
		if cwd != "" {
			cands = append(cands, filepath.Join(cwd, ".pi", "tasks", "tasks-"+id+".json"))
		}
		if agentDir != "" && cwd != "" {
			cands = append(cands, filepath.Join(agentDir, "tasks", "sessions", piTasksProjectKey(cwd), "tasks-"+id+".json"))
		}
	}
	if cwd != "" {
		cands = append(cands, filepath.Join(cwd, ".pi", "tasks", "tasks.json"))
	}
	for _, p := range cands {
		if t, ok := loadPiTaskFile(p); ok {
			return t, true
		}
	}
	return nil, false
}

// refreshPiTasks syncs the sidebar from the pi-tasks store file (covers
// /tasks-menu edits that emit no RPC). No file → keeps live RPC state.
func (m *Model) refreshPiTasks() {
	if t, ok := readPiTasks(m.cwd, m.sessionFile); ok {
		m.Todos = t
	}
}

// MCP servers ---------------------------------------------------------------

func piAgentDir() string {
	for _, k := range []string{"PI_CODING_AGENT_DIR", "PI_AGENT_DIR"} {
		if d := os.Getenv(k); d != "" {
			if strings.HasPrefix(d, "~/") {
				if h, err := os.UserHomeDir(); err == nil {
					return filepath.Join(h, d[2:])
				}
			} else {
				return d
			}
		}
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".pi", "agent")
	}
	return ""
}

func readJSONFile(path string) map[string]any {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}

func estimateMcpTokens(name, desc string, schemaLen int) int {
	return (len(name) + len(desc) + schemaLen) / 4 + 10
}

// readMcpServers lists configured MCP servers with tool counts, same source
// files as pi-sidebar-tui: <agentDir>/{mcp.json,.mcp.json} for config +
// mcp-cache.json for the live tool snapshot.
func readMcpServers(dir string) []McpServer {
	if dir == "" {
		return nil
	}
	configured := map[string]any{}
	for _, f := range []string{"mcp.json", ".mcp.json"} {
		if cfg := readJSONFile(filepath.Join(dir, f)); cfg != nil {
			if ms, ok := cfg["mcpServers"].(map[string]any); ok {
				for k, v := range ms {
					if _, seen := configured[k]; !seen {
						configured[k] = v
					}
				}
			}
		}
	}
	// global directTools filter (same precedence as pi-sidebar-tui)
	var globalDirect any
	for _, f := range []string{"mcp.json", ".mcp.json"} {
		if cfg := readJSONFile(filepath.Join(dir, f)); cfg != nil {
			if s, ok := cfg["settings"].(map[string]any); ok && s["directTools"] != nil {
				globalDirect = s["directTools"]
				break
			}
		}
	}
	cached := map[string]any{}
	if c := readJSONFile(filepath.Join(dir, "mcp-cache.json")); c != nil {
		if s, ok := c["servers"].(map[string]any); ok {
			cached = s
		}
	}
	var names []string
	if len(configured) > 0 {
		for n := range configured {
			names = append(names, n)
		}
	} else {
		for n := range cached {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return nil
	}
	// stable order (TS uses config insertion order; maps need sorting)
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	out := make([]McpServer, 0, len(names))
	for _, n := range names {
		def, _ := configured[n].(map[string]any)
		if def == nil {
			def = map[string]any{}
		}
		if dis, ok := def["disabled"].(bool); ok && dis {
			out = append(out, McpServer{Name: n, Disabled: true})
			continue
		}
		var tools []any
		if srv, ok := cached[n].(map[string]any); ok {
			tools, _ = srv["tools"].([]any)
		}
		var filter any
		if def["directTools"] != nil {
			filter = def["directTools"]
		} else if globalDirect != nil {
			filter = globalDirect
		}
		excl := map[string]bool{}
		if e, ok := def["excludeTools"].([]any); ok {
			for _, x := range e {
				if s, ok := x.(string); ok {
					excl[s] = true
				}
			}
		}
		srv := McpServer{Name: n, Connected: len(tools) > 0}
		for _, t := range tools {
			tm, ok := t.(map[string]any)
			if !ok {
				continue
			}
			tn, _ := tm["name"].(string)
			if excl[tn] {
				continue
			}
			srv.Total++
			direct := false
			switch f := filter.(type) {
			case bool:
				direct = f
			case []any:
				for _, x := range f {
					if s, ok := x.(string); ok && s == tn {
						direct = true
						break
					}
				}
			}
			if direct {
				srv.Direct++
				desc, _ := tm["description"].(string)
				schLen := 2
				if sch, ok := tm["inputSchema"]; ok {
					if b, err := json.Marshal(sch); err == nil {
						schLen = len(b)
					}
				}
				srv.Tokens += estimateMcpTokens(tn, desc, schLen)
			}
		}
		out = append(out, srv)
	}
	return out
}

var (
	mcpCacheData []McpServer
	mcpCacheAt   time.Time
)

const mcpCacheTTL = 1500 * time.Millisecond

// getMcpServers returns the cached server list (1.5s TTL, like pi-sidebar-tui).
func getMcpServers() []McpServer {
	if time.Since(mcpCacheAt) < mcpCacheTTL {
		return mcpCacheData
	}
	mcpCacheData = readMcpServers(piAgentDir())
	mcpCacheAt = time.Now()
	return mcpCacheData
}

// pluginName strips the source prefix: "npm:pi-lens" → "pi-lens",
// "git:github.com/sting8k/pi-themes" → "github.com/sting8k/pi-themes".
func pluginName(spec string) string {
	if i := strings.Index(spec, ":"); i >= 0 {
		return spec[i+1:]
	}
	return spec
}

// readPlugins lists installed pi packages from <agentDir>/settings.json
// (same source `pi list` reads). Order follows settings.json.
func readPlugins(dir string) []Plugin {
	if dir == "" {
		return nil
	}
	cfg := readJSONFile(filepath.Join(dir, "settings.json"))
	if cfg == nil {
		return nil
	}
	raw, ok := cfg["packages"].([]any)
	if !ok {
		return nil
	}
	var out []Plugin
	for _, p := range raw {
		spec, ok := p.(string)
		if !ok || strings.TrimSpace(spec) == "" {
			continue
		}
		out = append(out, Plugin{Spec: spec, Name: pluginName(spec)})
	}
	return out
}

var (
	pluginCacheData []Plugin
	pluginCacheAt   time.Time
)

// getPlugins returns the cached plugin list (same 1.5s TTL as MCP).
func getPlugins() []Plugin {
	if time.Since(pluginCacheAt) < mcpCacheTTL {
		return pluginCacheData
	}
	pluginCacheData = readPlugins(piAgentDir())
	pluginCacheAt = time.Now()
	return pluginCacheData
}

// rendering (same monochrome sidebar language as renderSidebar) -------------

// renderPluginsSection draws the collapsible PLUGINS toggle: header only
// when collapsed (▸), header + one row per plugin when expanded (▾).
// It lives inside the sidebar viewport, so a long list scrolls with the
// rest of the sidebar (Ctrl/Alt+↑↓ PgUp PgDn Home End, wheel over it).
func (m Model) renderPluginsSection(inner int) string {
	var b strings.Builder
	mark := "▸"
	if m.showPlugins {
		mark = "▾"
	}
	b.WriteString(sideTitleStyle.Render(fmt.Sprintf("PLUGINS (%d) %s", len(m.Plugins), mark)) + "\n")
	if m.showPlugins {
		if len(m.Plugins) == 0 {
			b.WriteString(toolStyle.Render("—") + "\n")
		} else {
			for _, p := range m.Plugins {
				b.WriteString(statusBarStyle.Render(" • ") +
					lipgloss.NewStyle().Foreground(cText).Render(Short(p.Name, inner-3)) + "\n")
			}
		}
	}
	b.WriteString(sep() + "\n")
	return b.String()
}

func (m Model) renderMcpSection(inner int) string {
	var b strings.Builder
	b.WriteString(sideTitleStyle.Render("MCP Servers") + "\n")
	for _, s := range m.MCP {
		var dot string
		switch {
		case s.Disabled:
			dot = statusBarStyle.Render("⊘")
		case s.Connected:
			if s.Total > 0 && s.Direct == s.Total {
				dot = okStyle.Render("●")
			} else if s.Connected {
				dot = lipgloss.NewStyle().Foreground(cText).Render("◐")
			} else {
				dot = statusBarStyle.Render("○")
			}
		default:
			dot = statusBarStyle.Render("○")
		}
		count := ""
		if s.Total > 0 {
			count = fmt.Sprintf("%d/%d", s.Direct, s.Total)
		}
		tok := ""
		if s.Direct > 0 {
			tok = "  ~" + fmtComma(s.Tokens)
		}
		suffix := statusBarStyle.Render("  " + count + tok)
		suffixLen := 2 + len(count)
		if s.Direct > 0 {
			suffixLen += 2 + len(fmtComma(s.Tokens)) + 1
		}
		nameMax := inner - 3 - suffixLen
		if nameMax < 0 {
			nameMax = 0
		}
		name := Short(s.Name, nameMax)
		b.WriteString(statusBarStyle.Render(" ") + dot + " " +
			lipgloss.NewStyle().Foreground(cText).Render(name) + suffix + "\n")
	}
	b.WriteString(sep() + "\n")
	return b.String()
}

func selectTodos(todos []TodoItem, max int) []TodoItem {
	if max <= 0 || len(todos) <= max {
		return todos
	}
	var chosen []TodoItem
	for _, t := range todos {
		if t.Status == TodoInProgress {
			chosen = append(chosen, t)
			if len(chosen) >= max {
				return chosen
			}
		}
	}
	// most recent (highest list position) fill the rest
	need := max - len(chosen)
	if need > 0 {
		var rest []TodoItem
		for _, t := range todos {
			if t.Status != TodoInProgress {
				rest = append(rest, t)
			}
		}
		if len(rest) > need {
			rest = rest[len(rest)-need:]
		}
		chosen = append(chosen, rest...)
	}
	return chosen
}

func (m Model) renderTodosSection(inner int) string {
	var b strings.Builder
	done := 0
	for _, t := range m.Todos {
		if t.Status == TodoCompleted {
			done++
		}
	}
	b.WriteString(sideTitleStyle.Render(fmt.Sprintf("Todos (%d/%d)", done, len(m.Todos))) + "\n")
	if len(m.Todos) == 0 {
		b.WriteString(toolStyle.Render(" (no todos)") + "\n")
	} else {
		shown := selectTodos(m.Todos, todosShowMax)
		for _, t := range shown {
			var glyph string
			switch t.Status {
			case TodoCompleted:
				glyph = okStyle.Render("✓")
			case TodoInProgress:
				glyph = lipgloss.NewStyle().Foreground(cText).Render("◐")
			default:
				glyph = statusBarStyle.Render("○")
			}
			line := t.Content
			if t.Status == TodoInProgress && t.SubAct != "" {
				full := t.Content + " (" + t.SubAct + ")"
				if lipgloss.Width(full) <= inner-3 {
					b.WriteString(statusBarStyle.Render(" ")+glyph+" "+
						lipgloss.NewStyle().Foreground(cText).Render(t.Content)+
						toolStyle.Render(" ("+Short(t.SubAct, inner)+")")+"\n")
					continue
				}
				line = Short(t.Content, inner-7)
			} else {
				line = Short(line, inner-3)
			}
			b.WriteString(statusBarStyle.Render(" ") + glyph + " " +
				lipgloss.NewStyle().Foreground(cText).Render(line) + "\n")
		}
		if hidden := len(m.Todos) - len(shown); hidden > 0 {
			b.WriteString(toolStyle.Render(fmt.Sprintf(" … +%d more", hidden)) + "\n")
		}
	}
	b.WriteString(sep() + "\n")
	return b.String()
}
