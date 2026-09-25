package ext

import "testing"

func TestContainsPlan(t *testing.T) {
	if !ContainsPlan("Plan mode") || ContainsPlan("chat") {
		t.Error("ContainsPlan mismatch")
	}
	if v, ok := ShouldLatchPlan("Start plan"); !ok || !v {
		t.Error("Start must latch on")
	}
	if v, ok := ShouldLatchPlan("Exit plan"); !ok || v {
		t.Error("Exit must latch off")
	}
}

func TestParseTodos(t *testing.T) {
	if !IsTodoTool("manage_todo_list") || !IsTodoTool("TaskCreate") {
		t.Error("IsTodoTool must match todo/task")
	}
	if NormTodoStatusStr("done") != TodoCompleted {
		t.Error("done -> completed")
	}
}

func TestParseAgentFrontmatter(t *testing.T) {
	n, _, _ := ParseAgentFrontmatter("name: code\n---\nbody")
	if n != "code" {
		t.Errorf("got %q", n)
	}
}
