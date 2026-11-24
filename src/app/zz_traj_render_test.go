package app

import (
	"fmt"
	"testing"
)

func TestDumpTraj(t *testing.T) {
	m := Model{winW: 120, winH: 40}
	d := &Dialog{Kind: "trajectory", Title: "Trajectory (all)", Scope: "all",
		Options: []string{"#01 assistant: edit file.go", "#02 tool: edit result"},
		Descs:   []string{"10:00:01 · assistant", "10:00:02 · tool"},
		Payload: []string{"#01 assistant: edit file.go\nassistant · id abc · 10:00:01\n\ntool: edit file.go\ndiff:\n- old line one\n- old line two\n+ new line one\n+ new line two",
			"#02 tool: edit result\ntool · id def · 10:00:02\n\ncall: edit file.go\ndiff:\n- removed\n+ added"}}
	d.Reindex()
	fmt.Println(m.renderTrajectoryDialog(d))
}
