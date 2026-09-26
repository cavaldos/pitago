// Command fakepi is a fake `pi --mode rpc` server used to pin pitago's
// wire contract in CI, where the real pi binary never exists.
//
// It speaks the same JSONL protocol as pi (one JSON object per line on
// stdin, one per line on stdout), answers every command pitago sends with a
// fixture response, and appends every line it receives to a log file so a
// test can assert the exact bytes pitago put on the wire.
//
// Standard library only: it must build with no module downloads so the
// contract test runs on a cold CI runner.
//
// Wiring:
//
//	PI_BIN=/path/to/fakepi   pitago (pirpc.Spawn) launches this binary.
//	FAKEPI_LOG=<path>        JSONL transcript of argv + every received line.
//	FAKEPI_FAIL=<type[,type]> answer those command types with success:false.
//	FAKEPI_BUSY=<type[,type]> answer those with pi's real "Agent is already
//	                          processing" refusal (success:false + its text).
//	FAKEPI_VETO=<type[,type]> answer those session switches with
//	                          data {"cancelled":true} (an extension veto).
//
// Protocol notes honoured on purpose (they are what pirpc.Client relies on):
//   - responses carry the request id back: {"type":"response","id":...}
//   - events are any other {"type":"event"} line, id-less
//   - extension_ui_response is fire-and-forget: it gets no response
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// fixtures answers each command type pitago sends with a fixed `data`
// payload, shaped like pi's real one so the client-side decoders are
// exercised for real (not just "unmarshal into a struct that stays zero").
var fixtures = map[string]string{
	"get_state": `{"model":{"id":"fake-model","name":"Fake Model",` +
		`"provider":"fake-provider","contextWindow":200000},` +
		`"thinkingLevel":"medium","isStreaming":false,` +
		`"steeringMode":"all","followUpMode":"all",` +
		`"autoCompactionEnabled":true,` +
		`"sessionFile":"/tmp/fake-session.jsonl","sessionId":"sess-1",` +
		`"sessionName":"fake","messageCount":2}`,
	"get_available_models": `{"models":[{"id":"fake-model","name":"Fake Model",` +
		`"provider":"fake-provider","api":"anthropic","baseUrl":"",` +
		`"reasoning":true,"input":["text"],"cost":{"input":1.5,"output":6,` +
		`"cacheRead":0.1,"cacheWrite":1.5},"contextWindow":200000,` +
		`"maxTokens":64000,"thinkingLevelMap":{}}]}`,
	"get_available_thinking_levels": `{"levels":["off","low","medium","high"]}`,
	"get_commands": `{"commands":[{"name":"team","description":"spawn a team",` +
		`"source":"extension","location":"/tmp/ext/team.ts"},` +
		`{"name":"review","description":"review the diff","source":"skill"}]}`,
	"get_messages": `{"messages":[{"role":"user","content":"hi"},` +
		`{"role":"assistant","content":[{"type":"text","text":"hello"}],` +
		`"stopReason":"stop","usage":{"input":10,"output":5,"cost":{"total":0.01}}}]}`,
	"get_session_stats": `{"sessionId":"sess-1",` +
		`"sessionFile":"/tmp/fake-session.jsonl","totalMessages":2,` +
		`"userMessages":1,"assistantMessages":1,"toolCalls":0,` +
		`"toolResults":0,"cost":0.01,` +
		`"tokens":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"total":15},` +
		`"contextUsage":{"tokens":15,"contextWindow":200000,"percent":0.01}}`,
	"get_tree": `{"leafId":"leaf-2","tree":[{"entry":{"type":"message",` +
		`"id":"leaf-1","timestamp":"2024-01-01T00:00:00Z",` +
		`"message":{"role":"user","content":"hi"}},"children":[]},` +
		`{"entry":{"type":"message","id":"leaf-2",` +
		`"parentId":"leaf-1","timestamp":"2024-01-01T00:00:01Z",` +
		`"message":{"role":"assistant","content":"hello"}},"children":[]}]}`,
	// get_entries carries pi's three session-entry shapes. A MESSAGE entry
	// is {type,id,parentId,timestamp,message} and has NO top-level
	// provider/model — those two live on the message itself. Top-level
	// provider/model exist only on a USAGE entry ({type:'usage',kind,
	// provider,model,usage}), and a model change is a modelId. Pinning a
	// message entry with top-level provider/model (as this fixture used
	// to) teaches the decoders a shape pi never sends.
	"get_entries": `{"entries":[{"type":"message","id":"leaf-1",` +
		`"parentId":"root-1","timestamp":"2024-01-01T00:00:00Z",` +
		`"message":{"role":"assistant","provider":"fake-provider",` +
		`"model":"fake-model","responseModel":"fake-model",` +
		`"usage":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,` +
		`"cost":{"total":0.01}}}},` +
		`{"type":"usage","id":"u-1","parentId":"leaf-1",` +
		`"timestamp":"2024-01-01T00:00:01Z","kind":"message",` +
		`"provider":"second-provider","model":"second-model",` +
		`"usage":{"input":7,"output":3,"cacheRead":0,"cacheWrite":0,` +
		`"cost":{"total":0.02}}},` +
		`{"type":"model_change","id":"mc-1","parentId":"u-1",` +
		`"timestamp":"2024-01-01T00:00:02Z","modelId":"fake-model-2"}]}`,
	"clear_queue": `{"steering":["s1"],"followUp":["f1"]}`,
	// A prompt is answered like any other command; the event burst behind
	// it is emitted separately (see emitStream).
	"prompt":      `{"sessionId":"sess-1"}`,
	"steer":       `{"sessionId":"sess-1"}`,
	"follow_up":   `{"sessionId":"sess-1"}`,
	"abort":       emptyData,
	"cycle_model": `{"model":{"id":"fake-model-2","name":"Fake Model 2"}}`,
	// pi answers set_model with the FLAT pi-ai Model object — verified on
	// pi 0.87.1: success(id, "set_model", model) — so data is
	// {id,name,api,provider,baseUrl,reasoning,input,cost,contextWindow,
	// maxTokens}. pi never sends a nested {model:{…}} wrapper, so the
	// fixture is flat: the decoder must be exercised on the shape
	// production actually uses.
	"set_model": `{"id":"fake-model","name":"Fake Model","api":"anthropic",` +
		`"provider":"fake-provider","baseUrl":"","reasoning":true,` +
		`"input":["text"],"cost":{"input":1.5,"output":6,"cacheRead":0.1,` +
		`"cacheWrite":1.5},"contextWindow":200000,"maxTokens":64000}`,
	// Session switching. pi answers data {cancelled:boolean} — true when an
	// extension vetoed through session_before_switch (see vetoFixtures).
	"new_session":    `{"cancelled":false}`,
	"switch_session": `{"cancelled":false}`,
	"clone":          `{"cancelled":false}`,
	"fork":           `{"text":"","cancelled":false}`,
	"get_fork_messages": `{"messages":[{"entryId":"leaf-1","text":"hi"},` +
		`{"entryId":"leaf-2","text":"hello"}]}`,
	"set_session_name": emptyData,
	"compact": `{"summary":"summary of the session",` +
		`"firstKeptEntryId":"leaf-2","tokensBefore":15000,` +
		`"estimatedTokensAfter":3000}`,
	"bash": `{"output":"ok\n","exitCode":0,"cancelled":false,` +
		`"truncated":false}`,
	"abort_bash":              emptyData,
	"abort_retry":             emptyData,
	"cycle_thinking_level":    `{"level":"high"}`,
	"get_last_assistant_text": `{"text":"hello from fake pi"}`,
	"export_html":             `{"path":"/tmp/fakepi-export.html"}`,
}

// vetoFixtures are the pi answers for a session switch an extension
// cancelled through session_before_switch: the command SUCCEEDED and the
// session did not change. FAKEPI_VETO=<type[,type]> selects them.
var vetoFixtures = map[string]string{
	"new_session":    `{"cancelled":true}`,
	"switch_session": `{"cancelled":true}`,
	"clone":          `{"cancelled":true}`,
	"fork":           `{"text":"","cancelled":true}`,
}

// piBusyMessage is pi's own refusal for a prompt sent while the agent is
// streaming with no streamingBehavior to queue it with
// (dist/core/agent-session.js). FAKEPI_BUSY=<type[,type]> answers with it.
const piBusyMessage = "Agent is already processing. " +
	"Specify streamingBehavior ('steer' or 'followUp') to queue the message."

// emptyData is the data object every state-changing command answers with
// (pi returns success and nothing else for them).
const emptyData = `{}`

func main() {
	logw := openLog(os.Getenv("FAKEPI_LOG"))
	defer logw.Close()

	// argv is part of the contract: how pitago launches pi (--mode rpc plus
	// the session/provider/model flags) is asserted by the wire test.
	argv := append([]string{"fakepi"}, os.Args[1:]...)
	logw.write(map[string]any{"kind": "spawn", "args": argv})

	fail := map[string]bool{}
	for _, t := range strings.Split(os.Getenv("FAKEPI_FAIL"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			fail[t] = true
		}
	}
	veto := map[string]bool{}
	for _, t := range strings.Split(os.Getenv("FAKEPI_VETO"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			veto[t] = true
		}
	}
	busy := map[string]bool{}
	for _, t := range strings.Split(os.Getenv("FAKEPI_BUSY"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			busy[t] = true
		}
	}

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		var cmd struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &cmd); err != nil {
			// Record the unparsable line verbatim: a malformed frame is
			// exactly the kind of wire regression this harness exists for.
			logw.write(map[string]any{"kind": "unparsed", "raw": line})
			continue
		}
		// Log before answering: the request record is on disk before the
		// matching response goes out, so a test that waits for the response
		// can always read the request.
		logw.write(map[string]any{"kind": "request", "type": cmd.Type, "id": cmd.ID, "raw": line})

		if fail[cmd.Type] {
			writeLine(out, map[string]any{
				"id": cmd.ID, "type": "response", "command": cmd.Type,
				"success": false, "error": "fakepi: injected failure for " + cmd.Type,
			})
			out.Flush()
			continue
		}

		// pi's "already processing" refusal: success:false with pi's own
		// wording, so the client's typed error is exercised for real.
		if busy[cmd.Type] {
			writeLine(out, map[string]any{
				"id": cmd.ID, "type": "response", "command": cmd.Type,
				"success": false, "error": piBusyMessage,
			})
			out.Flush()
			continue
		}

		// An extension veto: the switch command succeeded, the session did
		// not change.
		if data, ok := vetoFixtures[cmd.Type]; veto[cmd.Type] && ok {
			writeLine(out, map[string]any{
				"id": cmd.ID, "type": "response", "command": cmd.Type,
				"success": true, "data": json.RawMessage(data),
			})
			out.Flush()
			continue
		}

		// Fire-and-forget: pi never answers extension_ui_response, and the
		// client only writes it (Client.Fire) without waiting.
		if cmd.Type == "extension_ui_response" {
			continue
		}

		data, ok := fixtures[cmd.Type]
		if !ok {
			// No fixture means the test asked for something this harness
			// does not model. Answer as an error so it fails loudly instead
			// of hanging until the client times out.
			writeLine(out, map[string]any{
				"id": cmd.ID, "type": "response", "command": cmd.Type,
				"success": false, "error": "fakepi: no fixture for command " + cmd.Type,
			})
			out.Flush()
			continue
		}
		writeLine(out, map[string]any{
			"id": cmd.ID, "type": "response", "command": cmd.Type,
			"success": true, "data": json.RawMessage(data),
		})
		out.Flush()

		if cmd.Type == "prompt" {
			// A prompt is only useful to pitago with the event stream
			// behind it: two text_delta chunks (the client merges
			// consecutive streaming chunks) plus message_end.
			emitStream(out)
		}
	}
	// stdin EOF: exit cleanly like pi does when its host closes the pipe.
}

// emitStream writes the streaming burst pi sends while answering a prompt.
func emitStream(out *bufio.Writer) {
	for _, chunk := range []string{"Hel", "lo from fake pi"} {
		writeLine(out, map[string]any{
			"type": "message_update",
			"assistantMessageEvent": map[string]any{
				"type": "text_delta", "contentIndex": 0, "delta": chunk,
			},
		})
	}
	writeLine(out, map[string]any{
		"type": "message_end",
		"assistantMessageEvent": map[string]any{
			"type": "done", "reason": "stop",
		},
	})
	out.Flush()
}

func writeLine(out *bufio.Writer, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakepi: marshal:", err)
		return
	}
	out.Write(b)
	out.WriteByte('\n')
}

// logWriter appends transcript records as JSONL. A nil-safe no-op when
// FAKEPI_LOG is unset, so the fake also works as a plain handshake stub.
type logWriter struct {
	f *os.File
}

func openLog(path string) *logWriter {
	if path == "" {
		return &logWriter{}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakepi: open log:", err)
		return &logWriter{}
	}
	return &logWriter{f: f}
}

func (l *logWriter) write(rec map[string]any) {
	if l.f == nil {
		return
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	l.f.Write(append(b, '\n'))
	l.f.Sync()
}

func (l *logWriter) Close() {
	if l.f != nil {
		l.f.Close()
	}
}
