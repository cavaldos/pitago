package pirpc

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every command pi declares in dist/modes/rpc/rpc-types.d.ts that pitago
// used not to send at all, with the exact bytes the wrapper puts on the
// wire. Comparing the whole line (not a decoded struct) is the point: it
// pins the field names, the omitted-when-empty set, and the key that MUST be
// present ("message" is never omitempty, see Command).
func TestCommandWrappersWirePayloads(t *testing.T) {
	cases := []struct {
		name string
		call func(*Client) error
		want string
	}{
		{"compact", func(c *Client) error { _, err := c.Compact(""); return err },
			`{"id":"<id>","type":"compact","message":""}`},
		{"compact with instructions", func(c *Client) error { _, err := c.Compact("keep the tests"); return err },
			`{"id":"<id>","type":"compact","message":"","customInstructions":"keep the tests"}`},
		{"new_session", func(c *Client) error { return c.NewSession() },
			`{"id":"<id>","type":"new_session","message":""}`},
		{"new_session with parent", func(c *Client) error { _, err := c.NewSessionResult("/tmp/parent.jsonl"); return err },
			`{"id":"<id>","type":"new_session","message":"","parentSession":"/tmp/parent.jsonl"}`},
		{"switch_session", func(c *Client) error { _, err := c.SwitchSession("/tmp/s.jsonl"); return err },
			`{"id":"<id>","type":"switch_session","message":"","sessionPath":"/tmp/s.jsonl"}`},
		{"fork", func(c *Client) error { _, err := c.Fork("leaf-1"); return err },
			`{"id":"<id>","type":"fork","message":"","entryId":"leaf-1"}`},
		{"clone", func(c *Client) error { _, err := c.Clone(); return err },
			`{"id":"<id>","type":"clone","message":""}`},
		{"set_session_name", func(c *Client) error { return c.SetSessionName("refactor") },
			`{"id":"<id>","type":"set_session_name","message":"","name":"refactor"}`},
		{"bash", func(c *Client) error { _, err := c.Bash("ls -l", false); return err },
			`{"id":"<id>","type":"bash","message":"","command":"ls -l","excludeFromContext":false}`},
		{"bash hidden from context", func(c *Client) error { _, err := c.Bash("ls -l", true); return err },
			`{"id":"<id>","type":"bash","message":"","command":"ls -l","excludeFromContext":true}`},
		{"abort_bash", func(c *Client) error { return c.AbortBash() },
			`{"id":"<id>","type":"abort_bash","message":""}`},
		{"abort_retry", func(c *Client) error { return c.AbortRetry() },
			`{"id":"<id>","type":"abort_retry","message":""}`},
		{"cycle_thinking_level", func(c *Client) error { _, err := c.CycleThinkingLevel(); return err },
			`{"id":"<id>","type":"cycle_thinking_level","message":""}`},
		{"follow_up", func(c *Client) error { _, err := c.FollowUp("then run the tests"); return err },
			`{"id":"<id>","type":"follow_up","message":"then run the tests"}`},
		{"get_fork_messages", func(c *Client) error { _, err := c.GetForkMessages(); return err },
			`{"id":"<id>","type":"get_fork_messages","message":""}`},
		{"export_html", func(c *Client) error { _, err := c.ExportHTML(""); return err },
			`{"id":"<id>","type":"export_html","message":""}`},
		{"export_html with path", func(c *Client) error { _, err := c.ExportHTML("/tmp/out.html"); return err },
			`{"id":"<id>","type":"export_html","message":"","outputPath":"/tmp/out.html"}`},
		{"get_last_assistant_text", func(c *Client) error { _, err := c.GetLastAssistantText(); return err },
			`{"id":"<id>","type":"get_last_assistant_text","message":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, log := startFakePi(t, nil)
			if err := tc.call(c); err != nil {
				t.Fatalf("call: %v", err)
			}
			if got := lastRequest(t, log); got != tc.want {
				t.Fatalf("wire payload:\n got %s\nwant %s", got, tc.want)
			}
		})
	}
}

// The decoders must fill what pi actually sends, not leave zeros.
func TestCommandWrapperPayloadDecoding(t *testing.T) {
	c, _ := startFakePi(t, nil)

	if res, err := c.Compact(""); err != nil {
		t.Fatalf("compact: %v", err)
	} else if res.Summary == "" || res.FirstKeptEntryID != "leaf-2" || res.TokensBefore != 15000 || res.EstimatedTokensAfter != 3000 {
		t.Errorf("compact decoded as %+v", res)
	}

	if res, err := c.Bash("ls", false); err != nil {
		t.Fatalf("bash: %v", err)
	} else if res.Output != "ok\n" || res.ExitCodeOr(-1) != 0 || res.Cancelled || res.Truncated {
		t.Errorf("bash decoded as %+v", res)
	}
	// pi sends no exit code at all when it killed the command.
	if got := (BashResult{}).ExitCodeOr(-1); got != -1 {
		t.Errorf("missing exit code should fall back to the caller's default, got %d", got)
	}

	msgs, err := c.GetForkMessages()
	if err != nil {
		t.Fatalf("get_fork_messages: %v", err)
	}
	if len(msgs) != 2 || msgs[0].EntryID != "leaf-1" || msgs[0].Text != "hi" || msgs[1].Text != "hello" {
		t.Fatalf("fork messages = %+v", msgs)
	}

	if level, err := c.CycleThinkingLevel(); err != nil {
		t.Fatalf("cycle_thinking_level: %v", err)
	} else if level != "high" {
		t.Errorf("level = %q", level)
	}
	if text, err := c.GetLastAssistantText(); err != nil {
		t.Fatalf("get_last_assistant_text: %v", err)
	} else if text != "hello from fake pi" {
		t.Errorf("text = %q", text)
	}
	if path, err := c.ExportHTML(""); err != nil {
		t.Fatalf("export_html: %v", err)
	} else if path != "/tmp/fakepi-export.html" {
		t.Errorf("path = %q", path)
	}
	if fork, err := c.Fork("leaf-1"); err != nil {
		t.Fatalf("fork: %v", err)
	} else if fork.Cancelled {
		t.Errorf("fork reported a veto")
	}
}

// pi answers set_model with the FLAT pi-ai Model object — verified on
// pi 0.87.1: success(id, "set_model", model), so data is
// {id,name,api,provider,baseUrl,reasoning,input,cost,contextWindow,
// maxTokens} and there is no {model:{…}} wrapper anywhere. The label must
// come from the top-level id, and the nested branch may only ever be
// tolerance for a wrapping build.
func TestSetModelReadsFlatPiModel(t *testing.T) {
	c, log := startFakePi(t, nil)
	label, err := c.SetModelByID("fake-provider", "fake-model")
	if err != nil {
		t.Fatalf("set_model: %v", err)
	}
	if label != "fake-model" {
		t.Errorf("label = %q, want the flat model id", label)
	}
	if got, want := lastRequest(t, log),
		`{"id":"<id>","type":"set_model","message":"","provider":"fake-provider","modelId":"fake-model"}`; got != want {
		t.Errorf("request payload:\n got %s\nwant %s", got, want)
	}
}

// A model id is the display label pi itself keys on; when a build answers a
// name only (or wraps the model), the label must still resolve rather than
// coming back empty.
func TestSetModelLabelFallbacks(t *testing.T) {
	send := func(data string) string {
		echo := filepath.Join(t.TempDir(), "echo.sh")
		script := "#!/bin/sh\nwhile read -r line; do echo " +
			`'{"id":"go-1","type":"response","command":"set_model","success":true,"data":` + data + `}'` + "\ndone\n"
		if err := os.WriteFile(echo, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		c, err := Spawn(Options{Bin: echo, NoSession: true})
		if err != nil {
			t.Skipf("no /bin/sh: %v", err)
		}
		defer c.Close()
		label, err := c.SetModelByID("p", "m")
		if err != nil {
			t.Fatalf("set_model(%s): %v", data, err)
		}
		return label
	}
	if got := send(`{"id":"flat-id","name":"Flat"}`); got != "flat-id" {
		t.Errorf("flat id preferred, got %q", got)
	}
	if got := send(`{"name":"Name Only"}`); got != "Name Only" {
		t.Errorf("name-only fallback = %q", got)
	}
	if got := send(`{"model":{"id":"nested-id"}}`); got != "nested-id" {
		t.Errorf("nested tolerance = %q", got)
	}
	if got := send(`{}`); got != "" {
		t.Errorf("empty answer should give an empty label, got %q", got)
	}
}

// An extension veto arrives as success:true with data {cancelled:true}: the
// command worked, the session did NOT change. It has to reach the host as a
// distinct outcome, never as "done".
func TestSessionSwitchVetoIsSurfaced(t *testing.T) {
	for _, cmdType := range []string{"new_session", "switch_session", "fork", "clone"} {
		t.Run(cmdType, func(t *testing.T) {
			c, _ := startFakePi(t, map[string]string{"FAKEPI_VETO": cmdType})
			var res SwitchResult
			var err error
			switch cmdType {
			case "new_session":
				res, err = c.NewSessionResult("")
			case "switch_session":
				res, err = c.SwitchSession("/tmp/s.jsonl")
			case "fork":
				var fr ForkResult
				fr, err = c.Fork("leaf-1")
				res = fr.SwitchResult
			case "clone":
				res, err = c.Clone()
			}
			if err != nil {
				t.Fatalf("%s: transport error, not a veto: %v", cmdType, err)
			}
			if !res.Cancelled {
				t.Fatalf("%s: veto not reported", cmdType)
			}
			if verr := res.VetoErr(cmdType, nil); verr == nil || !IsVeto(verr) {
				t.Fatalf("%s: VetoErr = %v", cmdType, verr)
			} else if !strings.Contains(verr.Error(), cmdType) {
				t.Errorf("veto error should name the command: %v", verr)
			}
		})
	}
	// A real transport error is passed through, not masked as a veto.
	if err := (SwitchResult{Cancelled: true}).VetoErr("fork", errors.New("boom")); err == nil || IsVeto(err) {
		t.Errorf("transport error was swallowed: %v", err)
	}
	// No veto, no error.
	if err := (SwitchResult{}).VetoErr("fork", nil); err != nil {
		t.Errorf("clean switch produced %v", err)
	}
}

// pi refuses a prompt sent while the agent streams, with success:false and
// a message naming both queueing modes. That must be a typed, matchable
// error and not an opaque "prompt failed".
func TestPromptWhileStreamingIsTypedBusyError(t *testing.T) {
	c, _ := startFakePi(t, map[string]string{"FAKEPI_BUSY": "prompt"})
	_, err := c.Prompt("hi")
	if err == nil {
		t.Fatal("expected pi to refuse the prompt")
	}
	if !IsAgentBusy(err) {
		t.Fatalf("error is not ErrAgentBusy: %v", err)
	}
	var busy *AgentBusyError
	if !errors.As(err, &busy) {
		t.Fatalf("error is not an *AgentBusyError: %v", err)
	}
	if busy.Command != "prompt" {
		t.Errorf("command = %q", busy.Command)
	}
	if !strings.Contains(busy.Error(), "streamingBehavior") {
		t.Errorf("pi's own wording should be preserved: %q", busy.Error())
	}
	// A steer/follow-up is the documented way through: it carries the
	// streamingBehavior pi asks for, so the same fake accepts it.
	if _, err := c.FollowUp("later"); err != nil {
		t.Fatalf("follow_up should not be refused: %v", err)
	}
	// An unrelated failure keeps the plain error shape.
	c2, _ := startFakePi(t, map[string]string{"FAKEPI_FAIL": "compact"})
	if _, err := c2.Compact(""); err == nil || IsAgentBusy(err) {
		t.Errorf("compact failure = %v", err)
	}
}

// pi requires these fields; sending an empty one would act on an arbitrary
// session/entry, so the wrapper refuses before touching the wire.
func TestWrappersRefuseEmptyRequiredFields(t *testing.T) {
	c, log := startFakePi(t, nil)
	if _, err := c.SwitchSession("  "); err == nil {
		t.Error("switch_session accepted an empty path")
	}
	if _, err := c.Fork(""); err == nil {
		t.Error("fork accepted an empty entry id")
	}
	if _, err := c.Bash("   ", false); err == nil {
		t.Error("bash accepted an empty command")
	}
	if reqs := rawRequests(t, log); len(reqs) != 0 {
		t.Errorf("refused calls still hit the wire: %v", reqs)
	}
}

// The wrappers wait for pi's answer, not forever. A child that echoes the
// request back (it never sends a response) must not hang the UI thread.
func TestCommandWrappersTimeOut(t *testing.T) {
	echo := filepath.Join(t.TempDir(), "echo.sh")
	script := "#!/bin/sh\nwhile read -r line; do echo \"$line\"; done\n"
	if err := os.WriteFile(echo, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	c, err := Spawn(Options{Bin: echo, NoSession: true})
	if err != nil {
		t.Skipf("no /bin/cat: %v", err)
	}
	defer c.Close()
	start := time.Now()
	if _, err := c.Send(Command{Type: "clone"}, 100*time.Millisecond); err == nil ||
		!strings.Contains(err.Error(), "timed out") {
		t.Fatalf("clone = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("timeout took %s", elapsed)
	}
}
