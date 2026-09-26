// Package wire pins pitago's JSONL contract with pi against a fake pi
// server (tests/fakepi), so the wire format is verified in CI even though
// the real pi binary is never installed there.
//
// The fake is wired in exactly like the real thing: it is put on PI_BIN and
// pirpc.Spawn launches it instead of pi. script/test-wire.sh builds it and
// runs this package with PI_BIN set.
//
// Each test points the fake at its own transcript file (FAKEPI_LOG) and then
// asserts on the bytes the fake actually received, with the request id
// replaced by "<id>". Comparing the whole line (not a decoded struct) is the
// point: it pins field names, the order-independent-but-exact JSON key set,
// and which fields are omitted when empty.
package wire

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"pitago/src/pirpc"
)

// mintedID matches the request ids pirpc.Client generates itself.
var mintedID = regexp.MustCompile(`^go-[0-9]+$`)

// record is one line of the fake's transcript.
type record struct {
	Kind string   `json:"kind"`
	Args []string `json:"args"`
	Type string   `json:"type"`
	ID   string   `json:"id"`
	Raw  string   `json:"raw"`
}

// fakeBin returns the fake pi binary, skipping when it was not built.
// PI_BIN is the wiring under test (the same env var pitago honours in
// production), so its presence means "a pi is available for this run".
func fakeBin(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("PI_BIN")
	if bin == "" {
		t.Skip("no PI_BIN: run this package via script/test-wire.sh (builds tests/fakepi)")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("PI_BIN=%s is not executable: %v", bin, err)
	}
	return bin
}

// transcript is one fake-pi run: its log file plus the spawn it recorded.
type transcript struct {
	t   *testing.T
	bin string
	log string
}

func start(t *testing.T) *transcript {
	t.Helper()
	tr := &transcript{t: t, bin: fakeBin(t), log: filepath.Join(t.TempDir(), "wire.jsonl")}
	t.Setenv("FAKEPI_LOG", tr.log)
	return tr
}

// sub returns a fresh transcript (own log file) for one subtest.
func (tr *transcript) sub(t *testing.T) *transcript {
	t.Helper()
	fresh := &transcript{t: t, bin: tr.bin, log: filepath.Join(t.TempDir(), "wire.jsonl")}
	t.Setenv("FAKEPI_LOG", fresh.log)
	return fresh
}

// spawn launches the fake pi through the public client API.
func (tr *transcript) spawn(opt pirpc.Options) *pirpc.Client {
	tr.t.Helper()
	c, err := pirpc.Spawn(opt)
	if err != nil {
		tr.t.Fatalf("spawn: %v", err)
	}
	tr.t.Cleanup(c.Close)
	return c
}

// records reads the transcript written so far.
func (tr *transcript) records() []record {
	tr.t.Helper()
	raw, err := os.ReadFile(tr.log)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		tr.t.Fatalf("read transcript: %v", err)
	}
	var out []record
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var r record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			tr.t.Fatalf("transcript line %q: %v", line, err)
		}
		out = append(out, r)
	}
	return out
}

// argv returns the arguments the fake was launched with.
func (tr *transcript) argv() []string {
	tr.t.Helper()
	for _, r := range tr.records() {
		if r.Kind == "spawn" {
			return r.Args
		}
	}
	tr.t.Fatal("no spawn record in transcript")
	return nil
}

// requests returns every received command line, verbatim, with the
// auto-assigned request id replaced by "<id>" so goldens stay stable.
func (tr *transcript) requests() []string {
	tr.t.Helper()
	var out []string
	for _, r := range tr.records() {
		switch r.Kind {
		case "request":
			out = append(out, maskID(r.Raw))
		case "unparsed":
			tr.t.Errorf("fake pi received a line that is not a JSON object: %s", r.Raw)
		}
	}
	return out
}

// lastRequest is the single command line this transcript must contain.
func (tr *transcript) lastRequest() string {
	tr.t.Helper()
	got := tr.requests()
	if len(got) != 1 {
		tr.t.Fatalf("want exactly 1 request, got %d: %v", len(got), got)
	}
	return got[0]
}

// maskID rewrites a client-minted "id" (go-N) to the <id> placeholder,
// keeping every other byte of the line exactly as pitago wrote it. Ids the
// caller supplies itself (an extension_ui_response answers pi's request id)
// are left alone: they are part of the contract, not a test artefact.
func maskID(line string) string {
	var envelope struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		return line
	}
	if !mintedID.MatchString(envelope.ID) {
		return line
	}
	// Marshal the id alone to get its exact JSON encoding, then swap it.
	enc, _ := json.Marshal(envelope.ID)
	return strings.Replace(line, string(enc), `"<id>"`, 1)
}

// TestSpawnArgvContract pins how pitago launches pi: always `--mode rpc`,
// then the session/provider/model flags in a fixed order.
func TestSpawnArgvContract(t *testing.T) {
	tr := start(t)
	cases := []struct {
		name string
		opt  pirpc.Options
		want []string
	}{
		{"defaults", pirpc.Options{}, []string{"fakepi", "--mode", "rpc"}}, {"no session", pirpc.Options{NoSession: true},
			[]string{"fakepi", "--mode", "rpc", "--no-session"}},
		{"continue", pirpc.Options{Continue: true},
			[]string{"fakepi", "--mode", "rpc", "-c"}},
		{"exact session", pirpc.Options{Session: "/tmp/s.jsonl"},
			[]string{"fakepi", "--mode", "rpc", "--session", "/tmp/s.jsonl"}},
		{"provider and model", pirpc.Options{Provider: "fake-provider", Model: "fake-model"},
			[]string{"fakepi", "--mode", "rpc", "--provider", "fake-provider", "--model", "fake-model"}},
		{"everything", pirpc.Options{Continue: true, NoSession: true, Session: "s1",
			Provider: "p", Model: "m"},
			[]string{"fakepi", "--mode", "rpc", "-c", "--no-session", "--session", "s1",
				"--provider", "p", "--model", "m"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := tr.sub(t)
			c := tr.spawn(tc.opt)
			// Sync: the fake records its argv at startup, so wait for a
			// response to be sure it is already running.
			if _, err := c.GetState(); err != nil {
				t.Fatalf("sync ping: %v", err)
			}
			if got := tr.argv(); !equalStrings(got, tc.want) {
				t.Errorf("argv mismatch\n got: %v\nwant: %v", got, tc.want)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestCommandPayloads pins the exact JSONL line every command sender in
// pirpc puts on the wire — the 19 senders that were previously only
// reachable through a real pi. The response decoding side is covered by
// TestResponseDecoding in this same file.
func TestCommandPayloads(t *testing.T) {
	cases := []struct {
		name string
		call func(c *pirpc.Client)
		want string
	}{
		{"prompt", func(c *pirpc.Client) {
			c.Prompt("hello")
		}, `{"id":"<id>","type":"prompt","message":"hello"}`},

		{"prompt with image", func(c *pirpc.Client) {
			c.Prompt("look", pirpc.ImageContent{Type: "image", Data: "AAA", MimeType: "image/png"})
		}, `{"id":"<id>","type":"prompt","message":"look","images":[{"type":"image","data":"AAA","mimeType":"image/png"}]}`},

		{"prompt empty message still transmits it", func(c *pirpc.Client) {
			// pi calls command.message.startsWith() unguarded, so the field
			// must be present even when only an image is sent.
			c.Prompt("", pirpc.ImageContent{Type: "image", Data: "AAA", MimeType: "image/png"})
		}, `{"id":"<id>","type":"prompt","message":"","images":[{"type":"image","data":"AAA","mimeType":"image/png"}]}`},

		{"steer", func(c *pirpc.Client) {
			c.Steer("actually, use gofmt")
		}, `{"id":"<id>","type":"prompt","message":"actually, use gofmt","streamingBehavior":"steer"}`},

		{"abort", func(c *pirpc.Client) { c.Abort() },
			`{"id":"<id>","type":"abort","message":""}`},

		{"clear_queue", func(c *pirpc.Client) { c.ClearQueue() },
			`{"id":"<id>","type":"clear_queue","message":""}`},

		{"new_session", func(c *pirpc.Client) { c.NewSession() },
			`{"id":"<id>","type":"new_session","message":""}`},

		{"set_model", func(c *pirpc.Client) { c.SetModelByID("fake-provider", "fake-model") },
			`{"id":"<id>","type":"set_model","message":"","provider":"fake-provider","modelId":"fake-model"}`},

		{"set_thinking_level", func(c *pirpc.Client) { c.SetLevel("high") },
			`{"id":"<id>","type":"set_thinking_level","message":"","level":"high"}`},

		{"set_steering_mode", func(c *pirpc.Client) { c.SetSteering("one-at-a-time") },
			`{"id":"<id>","type":"set_steering_mode","message":"","mode":"one-at-a-time"}`},

		{"set_follow_up_mode", func(c *pirpc.Client) { c.SetFollowUp("all") },
			`{"id":"<id>","type":"set_follow_up_mode","message":"","mode":"all"}`},

		{"set_auto_compaction", func(c *pirpc.Client) { c.SetAutoCompact(true) },
			`{"id":"<id>","type":"set_auto_compaction","message":"","enabled":true}`},

		{"set_auto_retry keeps an explicit false", func(c *pirpc.Client) { c.SetAutoRetry(false) },
			`{"id":"<id>","type":"set_auto_retry","message":"","enabled":false}`},

		{"get_tree", func(c *pirpc.Client) { c.GetTree() },
			`{"id":"<id>","type":"get_tree","message":""}`},

		{"get_entries", func(c *pirpc.Client) { c.GetEntries() },
			`{"id":"<id>","type":"get_entries","message":""}`},

		{"get_messages", func(c *pirpc.Client) { c.GetMessages() },
			`{"id":"<id>","type":"get_messages","message":""}`},

		{"get_state", func(c *pirpc.Client) { c.GetState() },
			`{"id":"<id>","type":"get_state","message":""}`},

		{"get_available_models", func(c *pirpc.Client) { c.GetModels() },
			`{"id":"<id>","type":"get_available_models","message":""}`},

		{"get_available_thinking_levels", func(c *pirpc.Client) { c.GetLevels() },
			`{"id":"<id>","type":"get_available_thinking_levels","message":""}`},

		{"get_commands", func(c *pirpc.Client) { c.GetCommands() },
			`{"id":"<id>","type":"get_commands","message":""}`},

		{"get_session_stats", func(c *pirpc.Client) { c.GetStats() },
			`{"id":"<id>","type":"get_session_stats","message":""}`},

		{"cycle_model", func(c *pirpc.Client) { c.CycleModel() },
			`{"id":"<id>","type":"cycle_model","message":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := start(t)
			c := tr.spawn(pirpc.Options{})
			tc.call(c)
			if got := tr.lastRequest(); got != tc.want {
				t.Errorf("wire payload mismatch\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

// TestFireExtensionUIResponse pins the fire-and-forget direction: every
// extension_ui_response variant reaches the wire verbatim, carries the
// extension's request id, and is never answered.
//
// pi's answer is a 3-variant union (verified on pi 0.87.1): {id,value},
// {id,confirmed} and {id,cancelled:true} — where `cancelled` is a LITERAL
// true, not a boolean flag. All three variants are pinned byte-for-byte, so
// a builder that starts emitting `cancelled:false` (a variant pi does not
// declare) or drops the value field fails here.
func TestFireExtensionUIResponse(t *testing.T) {
	value, yes, no, cancelled := "yes", true, false, true
	cases := []struct {
		name string
		cmd  pirpc.Command
		want string
	}{
		{"value", pirpc.Command{Type: "extension_ui_response", ID: "ext-7", Value: &value},
			`{"id":"ext-7","type":"extension_ui_response","message":"","value":"yes"}`},
		{"confirmed true", pirpc.Command{Type: "extension_ui_response", ID: "ext-8", Confirmed: &yes},
			`{"id":"ext-8","type":"extension_ui_response","message":"","confirmed":true}`},
		{"confirmed false", pirpc.Command{Type: "extension_ui_response", ID: "ext-9", Confirmed: &no},
			`{"id":"ext-9","type":"extension_ui_response","message":"","confirmed":false}`},
		{"cancelled", pirpc.Command{Type: "extension_ui_response", ID: "ext-10", Cancelled: &cancelled},
			`{"id":"ext-10","type":"extension_ui_response","message":"","cancelled":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := start(t)
			c := tr.spawn(pirpc.Options{})
			if err := c.Fire(tc.cmd); err != nil {
				t.Fatalf("fire: %v", err)
			}
			// No response comes back, so sync on a normal request: the fake
			// handles stdin lines in order, so once get_state answers, the
			// Fire line is on disk.
			if _, err := c.Send(pirpc.Command{Type: "get_state"}, 10*time.Second); err != nil {
				t.Fatalf("sync ping: %v", err)
			}
			got := tr.requests()
			want := []string{tc.want, `{"id":"<id>","type":"get_state","message":""}`}
			if !equalStrings(got, want) {
				t.Errorf("wire payloads mismatch\n got: %v\nwant: %v", got, want)
			}
		})
	}
}

// TestResponseDecoding pins the other half of the contract: the fixtures the
// fake answers with are decoded into the exported structs the UI reads.
func TestResponseDecoding(t *testing.T) {
	tr := start(t)
	c := tr.spawn(pirpc.Options{NoSession: true})

	state, err := c.GetState()
	if err != nil {
		t.Fatalf("get_state: %v", err)
	}
	if state.Model.ID != "fake-model" || state.Model.Provider != "fake-provider" ||
		state.ThinkingLevel != "medium" || state.SessionID != "sess-1" ||
		state.MessageCount != 2 {
		t.Errorf("get_state decoded wrong: %+v", state)
	}

	models, err := c.GetModels()
	if err != nil || len(models) != 1 {
		t.Fatalf("get_models: %v (%d models)", err, len(models))
	}
	if models[0].ID != "fake-model" || models[0].ContextWindow != 200000 || models[0].Cost.Input != 1.5 {
		t.Errorf("get_models decoded wrong: %+v", models[0])
	}

	levels, err := c.GetLevels()
	if err != nil || !equalStrings(levels, []string{"off", "low", "medium", "high"}) {
		t.Errorf("get_levels: %v %v", err, levels)
	}

	cmds, err := c.GetCommands()
	if err != nil || len(cmds) != 2 {
		t.Fatalf("get_commands: %v (%d commands)", err, len(cmds))
	}
	if cmds[0].Name != "team" || cmds[0].Source != "extension" || cmds[1].Source != "skill" {
		t.Errorf("get_commands decoded wrong: %+v", cmds)
	}

	msgs, err := c.GetMessages()
	if err != nil || len(msgs) != 2 {
		t.Fatalf("get_messages: %v (%d messages)", err, len(msgs))
	}
	if msgs[0].Role != "user" || pirpc.TextOf(msgs[1].Content) != "hello" {
		t.Errorf("get_messages decoded wrong: %+v", msgs)
	}
	if msgs[1].Usage == nil || msgs[1].Usage.Input != 10 || msgs[1].Usage.Cost.Total != 0.01 {
		t.Errorf("message usage decoded wrong: %+v", msgs[1].Usage)
	}

	stats, err := c.GetStats()
	if err != nil {
		t.Fatalf("get_session_stats: %v", err)
	}
	if stats.SessionID != "sess-1" || stats.TotalMessages != 2 || stats.UserMsgs != 1 ||
		stats.TokensTotal != 15 || stats.ContextPct != 0.01 {
		t.Errorf("get_session_stats decoded wrong: %+v", stats)
	}

	tree, leaf, err := c.GetTree()
	if err != nil || leaf != "leaf-2" || len(tree) != 2 {
		t.Fatalf("get_tree: %v (leaf=%q, %d nodes)", err, leaf, len(tree))
	}
	if tree[1].Entry.ID != "leaf-2" || tree[1].Entry.ParentID == nil {
		t.Errorf("get_tree entry decoded wrong: %+v", tree[1].Entry)
	}

	entries, err := c.GetEntries()
	if err != nil || len(entries) != 3 {
		t.Fatalf("get_entries: %v (%d entries)", err, len(entries))
	}
	// A message entry is {type,id,parentId,timestamp,message}: provider and
	// model live on the MESSAGE, and the entry has no top-level pair.
	if entries[0].Type != "message" || entries[0].Provider != "" || entries[0].Model != "" {
		t.Errorf("message entry must have no top-level provider/model: %+v", entries[0])
	}
	if entries[0].Message == nil || entries[0].Message.ResponseModel != "fake-model" ||
		entries[0].Message.Usage == nil || entries[0].Message.Usage.Cost.Total != 0.01 {
		t.Errorf("get_entries decoded wrong: %+v", entries[0])
	}
	// A usage entry is the one that DOES carry them, at the top level.
	if entries[1].Type != "usage" || entries[1].Provider != "second-provider" ||
		entries[1].Model != "second-model" || entries[1].Usage == nil ||
		entries[1].Usage.Cost.Total != 0.02 {
		t.Errorf("usage entry decoded wrong: %+v", entries[1])
	}
	// A model_change entry carries a single modelId; the breakdown must
	// ignore it rather than inventing a row for it.
	if entries[2].Type != "model_change" {
		t.Errorf("third entry = %+v, want a model_change", entries[2])
	}
	// The /session cost breakdown is built from these entries, so pin the
	// provider/model key the UI shows: one row from the assistant message,
	// one from the usage entry, cost desc.
	breakdown := pirpc.UsageBreakdown(entries)
	if len(breakdown) != 2 ||
		breakdown[0].Key != "second-provider/second-model" ||
		breakdown[0].Tokens != 10 || breakdown[0].Cost != 0.02 ||
		breakdown[1].Key != "fake-provider/fake-model" ||
		breakdown[1].Tokens != 15 || breakdown[1].Cost != 0.01 {
		t.Errorf("usage breakdown from get_entries wrong: %+v", breakdown)
	}

	steer, follow, err := c.ClearQueue()
	if err != nil || !equalStrings(steer, []string{"s1"}) || !equalStrings(follow, []string{"f1"}) {
		t.Errorf("clear_queue: %v steer=%v follow=%v", err, steer, follow)
	}

	label, err := c.SetModelByID("fake-provider", "other")
	if err != nil || label != "fake-model" {
		t.Errorf("set_model label: %v %q", err, label)
	}
	label, err = c.CycleModel()
	if err != nil || label != "fake-model-2" {
		t.Errorf("cycle_model label: %v %q", err, label)
	}
}

// TestSetModelAnswersFlatModelObject pins the set_model answer shape. pi
// answers success(id,"set_model", model) with the FLAT pi-ai Model object
// (verified on pi 0.87.1) — never a nested {model:{…}} wrapper — so the
// label must come from the top-level id. A fixture that wrapped the model
// would let a decoder that only understood the wrapper pass.
func TestSetModelAnswersFlatModelObject(t *testing.T) {
	tr := start(t)
	c := tr.spawn(pirpc.Options{})
	label, err := c.SetModelByID("fake-provider", "fake-model")
	if err != nil {
		t.Fatalf("set_model: %v", err)
	}
	if label != "fake-model" {
		t.Errorf("set_model label = %q, want fake-model (flat model.id)", label)
	}
	// The request side is unchanged by any of this: provider + modelId.
	if got, want := tr.lastRequest(),
		`{"id":"<id>","type":"set_model","message":"","provider":"fake-provider","modelId":"fake-model"}`; got != want {
		t.Errorf("set_model request mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestErrorResponseSurfacesError pins the failure path: a success:false
// response becomes a Go error on the sender, not a silent zero value.
func TestErrorResponseSurfacesError(t *testing.T) {
	t.Setenv("FAKEPI_FAIL", "get_state,set_thinking_level")
	tr := start(t)
	c := tr.spawn(pirpc.Options{})

	if _, err := c.GetState(); err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Errorf("get_state: want injected failure error, got %v", err)
	}
	if err := c.SetLevel("high"); err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Errorf("set_thinking_level: want injected failure error, got %v", err)
	}
}

// TestPromptStreamEvents pins the event side of the contract: a prompt is
// answered with a streaming burst, consecutive text_delta chunks of the same
// content block are merged into one, and the terminal event still arrives.
func TestPromptStreamEvents(t *testing.T) {
	tr := start(t)
	c := tr.spawn(pirpc.Options{})

	events := &eventLog{}
	c.SetOnEvent(events.add)
	if _, err := c.Prompt("hello"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	got := events.wait(t, 2*time.Second)
	if len(got) == 0 {
		t.Fatal("no events received for a prompt")
	}
	var deltas []string
	var ends int
	for _, ev := range got {
		switch ev.Type {
		case "message_update":
			var mu2 pirpc.MessageUpdate
			if err := json.Unmarshal(ev.Raw, &mu2); err != nil {
				t.Fatalf("message_update: %v", err)
			}
			if mu2.Event.Type == "text_delta" {
				deltas = append(deltas, mu2.Event.Delta)
			}
		case "message_end":
			ends++
		}
	}
	if len(deltas) != 1 || deltas[0] != "Hello from fake pi" {
		t.Errorf("want the two chunks merged into %q, got %v", "Hello from fake pi", deltas)
	}
	if ends != 1 {
		t.Errorf("want 1 message_end, got %d", ends)
	}
}

// eventLog collects events with a mutex, so the callback can be installed on
// the client and read from the test goroutine without a data race.
type eventLog struct {
	mu     sync.Mutex
	events []pirpc.Event
}

func (c *eventLog) add(ev pirpc.Event) {
	c.mu.Lock()
	c.events = append(c.events, ev)
	c.mu.Unlock()
}

func (c *eventLog) wait(t *testing.T, d time.Duration) []pirpc.Event {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		n := len(c.events)
		c.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]pirpc.Event(nil), c.events...)
}

// TestSpawnUsesEnvBin proves the harness is wired the way production is:
// with no Options.Bin, Spawn picks the binary up from PI_BIN.
func TestSpawnUsesEnvBin(t *testing.T) {
	tr := start(t)
	t.Setenv("PI_BIN", tr.bin)
	c := tr.spawn(pirpc.Options{})
	if _, err := c.GetState(); err != nil {
		t.Fatalf("get_state through PI_BIN: %v", err)
	}
	if got := tr.argv(); len(got) == 0 || got[0] != "fakepi" {
		t.Errorf("PI_BIN was not used to launch pi: %v", got)
	}
}

// TestFakeBinIsTheOneUnderTest guards the wiring itself: the CI job must not
// silently pass by running against the real pi or nothing at all.
func TestFakeBinIsTheOneUnderTest(t *testing.T) {
	bin := fakeBin(t)
	out, err := exec.Command(bin, "--mode", "rpc", "--probe").CombinedOutput()
	if err != nil {
		t.Fatalf("probe %s: %v", bin, err)
	}
	// The fake exits on EOF without answering an unknown arg, so an empty
	// output with a nil error is the expected probe result. Anything else
	// means PI_BIN points at something that is not the fake.
	if len(out) != 0 {
		t.Errorf("unexpected probe output from %s: %q", bin, out)
	}
}
