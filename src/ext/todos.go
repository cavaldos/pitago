package ext

import (
	"encoding/json"
	"fmt"
	"strings"

	"pitago/src/pirpc"
)

// TodoStatus mirrors app's sidebar states (canonical def lives here).
type TodoStatus string

const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
)

// TodoItem is one sidebar todo row.
type TodoItem struct {
	ID      string
	Content string
	Status  TodoStatus
	SubAct  string // optional sub-action shown after in-progress items
}

// IsTodoTool reports todo/task tool names (sidebar tracking).
func IsTodoTool(name string) bool {
	l := strings.ToLower(name)
	return strings.Contains(l, "todo") || strings.Contains(l, "task")
}

// IsPiTaskStoreTool reports the Task* family whose state lives in the
// pi-tasks store file (manage_todo_list keeps message-only state).
func IsPiTaskStoreTool(name string) bool {
	return strings.Contains(strings.ToLower(name), "task")
}

// TodoContent picks display text across todo shapes:
// pi-sidebar-tui (content/text), manage_todo_list (title), pi-tasks (subject).
func TodoContent(m map[string]any) string {
	for _, k := range []string{"content", "text", "title", "subject", "name", "label"} {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// ParseTodos extracts a todo list from a todo tool's payload. Handles the
// full list in the tool input (bare array, or object with
// todos/todoList/items/list/tasks key) and the list in the tool result
// details. Returns ok=false when no todo array is present.
func ParseTodos(raw json.RawMessage) ([]TodoItem, bool) {
	t := strings.TrimSpace(string(raw))
	if t == "" || t == "null" {
		return nil, false
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		if out, ok := ParseTodoLines(t); ok {
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
		if t, _ := m["type"].(string); t != "" {
			switch strings.ToLower(strings.ReplaceAll(t, "_", "")) {
			case "text", "thinking", "image", "toolcall", "toolresult":
				continue
			}
		}
		content := TodoContent(m)
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
		out = append(out, TodoItem{ID: id, Content: content, Status: NormTodoStatus(m), SubAct: sub})
	}
	if len(arr) > 0 && len(out) == 0 {
		return nil, false
	}
	return out, true
}

// ParseTodoLines parses pi-tasks TaskList text ("#1 [pending] subject").
func ParseTodoLines(t string) ([]TodoItem, bool) {
	var out []TodoItem
	for _, line := range strings.Split(t, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
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
				status = NormTodoStatusStr(content[j+1 : j+k])
				after := strings.TrimSpace(content[j+k+1:])
				if after != "" {
					content = after
				}
			}
		}
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

// NormTodoStatus unifies bool/map status shapes.
func NormTodoStatus(m map[string]any) TodoStatus {
	if s, ok := m["status"].(string); ok {
		return NormTodoStatusStr(s)
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

// NormTodoStatusStr unifies status spellings across extensions.
func NormTodoStatusStr(s string) TodoStatus {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "-", "_")) {
	case "in_progress", "active", "inprogress", "working", "started":
		return TodoInProgress
	case "completed", "done", "complete", "finished":
		return TodoCompleted
	default:
		return TodoPending
	}
}

// RestoreTodos rebuilds the todo list from get_messages history.
// manage_todo_list carries a FULL snapshot in each result details (last one
// wins); pi-tasks TaskList carries a full list as text (last one wins).
func RestoreTodos(msgs []pirpc.AgentMessage) []TodoItem {
	type callArgs struct {
		name string
		args string
	}
	_ = callArgs{}
	var cur []TodoItem
	found := false
	applyFull := func(t []TodoItem) {
		cur, found = t, true
	}
	for _, msg := range msgs {
		switch msg.Role {
		case "assistant":
			for _, b := range pirpc.BlocksOf(msg.Content) {
				if b.Type == "toolCall" && IsTodoTool(b.Name) && len(b.Arguments) > 0 {
					if t, ok := ParseTodos(b.Arguments); ok {
						applyFull(t)
					}
				}
			}
		case "toolResult":
			if !IsTodoTool(msg.ToolName) {
				continue
			}
			if len(msg.Details) > 0 {
				if t, ok := ParseTodos(msg.Details); ok {
					applyFull(t)
				}
			}
			// TaskList-style plain text also parses via ParseTodos fallback.
			if t, ok := ParseTodos(msg.Content); ok {
				applyFull(t)
			}
		}
	}
	if !found {
		return nil
	}
	return cur
}

// SelectTodos caps the sidebar list: in-progress first, then most recent.
// Mirrors pi-sidebar-tui priority (canonical impl; app delegates here).
func SelectTodos(todos []TodoItem, max int) []TodoItem {
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
