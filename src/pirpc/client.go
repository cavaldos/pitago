package pirpc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Client spawns `pi --mode rpc` and speaks JSONL with it.
// Responses (type=response) are matched to sends by id; everything else
// is queued and delivered to the event callback by one pump goroutine.
//
// The reader never invokes the callback itself. That callback bounces into
// the UI thread (tea.Program.Send) and blocks whenever the UI is busy;
// calling it inline would back-pressure pi's stdout, fill the pipe and stall
// the agent itself. Reader → queue → pump keeps that coupling gone, and a
// single pump keeps delivery in pi's order.
type Client struct {
	cmd     *exec.Cmd
	stdin   *os.File
	mu      sync.Mutex
	pending map[string]chan Response
	seq     int
	// onEvent is written by the UI thread right after Spawn and read by the
	// pump and the process waiter, so it needs its own lock. A plain exported
	// field raced here: the waiter could observe a half-published callback
	// and drop pi_exited. Use SetOnEvent, never assign directly.
	cbMu   sync.RWMutex
	onEv   func(Event)
	done   chan struct{}
	once   sync.Once
	closed atomic.Bool // intentional Close: waiter skips pi_exited

	// Event queue (per spawn). Unbounded on purpose: a bounded queue would
	// only move the same stall from the UI thread back to the reader.
	evMu      sync.Mutex
	evQueue   []Event
	evStopped bool          // Close(): drop instead of buffering
	evWake    chan struct{} // cap 1, "queue is non-empty"
	evEOF     chan struct{} // reader hit EOF: flush, then stop
	evStop    chan struct{} // Close(): stop the pump now
	evExited  chan struct{} // pump goroutine returned
	evOnce    sync.Once     // creates the channels above
	evRunOnce sync.Once     // starts the pump
	evEOFOnce sync.Once     // closes evEOF
	evStopOne sync.Once     // closes evStop
}

// Options controls how pi is spawned.
type Options struct {
	Bin       string // default "pi" (or $PI_BIN)
	Provider  string // --provider
	Model     string // --model
	Continue  bool   // -c (resume most recent session)
	NoSession bool   // --no-session
	Session   string // --session <path|id> (resume exact session)
	Dir       string // working directory for the pi child (default: ours)
}

// piStderrLog captures the pi child's stderr (fresh per spawn; read it
// right after a startup failure for pi's own reason).
const piStderrLog = "/tmp/pitago-pi-stderr.log"

// StderrTail returns the pi child's stderr collapsed to one short line
// ("" when empty) — the actual reason when pi dies at startup.
func StderrTail() string {
	raw, err := os.ReadFile(piStderrLog)
	if err != nil {
		return ""
	}
	s := strings.Join(strings.Fields(strings.TrimSpace(string(raw))), " ")
	const maxStderrTail = 240
	if len(s) > maxStderrTail {
		s = s[:maxStderrTail] + "…"
	}
	return s
}

// Spawn starts pi --mode rpc.
func Spawn(opt Options) (*Client, error) {
	bin := opt.Bin
	if bin == "" {
		bin = os.Getenv("PI_BIN")
	}
	if bin == "" {
		bin = "pi"
	}
	args := []string{"--mode", "rpc"}
	if opt.Continue {
		args = append(args, "-c")
	}
	if opt.NoSession {
		args = append(args, "--no-session")
	}
	if opt.Session != "" {
		args = append(args, "--session", opt.Session)
	}
	if opt.Provider != "" {
		args = append(args, "--provider", opt.Provider)
	}
	if opt.Model != "" {
		args = append(args, "--model", opt.Model)
	}
	cmd := exec.Command(bin, args...)
	if opt.Dir != "" {
		cmd.Dir = opt.Dir // pi takes its session cwd from the process dir
	}
	// The key overlay (pienv.go) is applied here, not through os.Setenv, so
	// pitago's own environment stays as the user's shell exported it. nil
	// means no overlay: the child then inherits the environment verbatim,
	// exactly like launching pi from this shell.
	cmd.Env = piChildEnviron()

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdin = stdinR
	cmd.Stdout = stdoutW
	logF, _ := os.Create(piStderrLog)
	if logF != nil {
		cmd.Stderr = logF
	}

	if err := cmd.Start(); err != nil {
		stdinR.Close()
		stdinW.Close()
		stdoutR.Close()
		stdoutW.Close()
		return nil, fmt.Errorf("start pi: %w", err)
	}
	stdinR.Close() // child owns read end (via dup); close parent copy
	stdoutW.Close()

	c := &Client{
		cmd:     cmd,
		stdin:   stdinW,
		pending: make(map[string]chan Response),
		done:    make(chan struct{}),
	}
	go c.readLoop(stdoutR)
	go func() {
		cmd.Wait()
		if logF != nil {
			logF.Close()
		}
		c.once.Do(func() { close(c.done) })
		// An intentional Close (login/logout/resume reconnect, TUI exit)
		// must not look like a crash: the replacer respawnMsg owns the UI.
		// Without this, the old pi's death notice can land after respawnMsg
		// (respawning already false) and fake a "pi has exited".
		if fn := c.eventFn(); !c.closed.Load() && fn != nil {
			fn(Event{Type: "pi_exited"})
		}
	}()
	return c, nil
}

// readLoop splits stdout on '\n' only (protocol requirement) and routes lines.
// One unmarshal per line: Response carries type+id, so the envelope and the
// response share a single parse; events keep the raw line bytes (cloned —
// the bufio buffer is reused — for the pump/UI thread to parse once).
//
// Every event is queued, never delivered here: readLoop only touches the
// queue mutex, so a slow OnEvent can never stall stdout. Responses still
// resolve inline — they are matched to a Send that is already waiting.
func (c *Client) readLoop(r *os.File) {
	defer r.Close()
	defer c.finishReading()
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return
		}
		line = bytes.TrimRight(line, "\r\n")
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}
		if resp.Type == "response" {
			c.mu.Lock()
			ch := c.pending[resp.ID]
			delete(c.pending, resp.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- resp
			}
			continue
		}
		c.enqueue(Event{Type: resp.Type, Raw: bytes.Clone(line)})
	}
}

// Event queue -------------------------------------------------------
//
// enqueue hands one event to the pump. It is what the reader calls, so it
// must stay O(1) and lock-free with respect to the UI: the only lock it
// takes is a short queue mutex, never c.mu.
func (c *Client) enqueue(ev Event) {
	c.initEventQueue()
	c.evRunOnce.Do(func() { go c.eventPump() })

	c.evMu.Lock()
	dropped := c.evStopped
	if !dropped {
		c.evQueue = append(c.evQueue, ev)
	}
	c.evMu.Unlock()
	if dropped {
		return // Close() already ran: the UI is tearing down
	}
	select {
	case c.evWake <- struct{}{}:
	default: // a wake is already pending
	}
}

// initEventQueue creates the queue channels lazily, so a hand-built
// Client (tests) behaves exactly like a spawned one.
func (c *Client) initEventQueue() {
	c.evOnce.Do(func() {
		c.evWake = make(chan struct{}, 1)
		c.evEOF = make(chan struct{})
		c.evStop = make(chan struct{})
		c.evExited = make(chan struct{})
	})
}

// finishReading: readLoop hit EOF (pi closed stdout). The pump delivers
// what is left and then exits, so nothing already read is dropped.
func (c *Client) finishReading() {
	c.initEventQueue()
	c.evEOFOnce.Do(func() { close(c.evEOF) })
}

// stopEventPump: Close(). Drops the backlog — pi is being killed, so the
// UI only cares about the stream stopping.
func (c *Client) stopEventPump() {
	c.initEventQueue()
	c.evMu.Lock()
	c.evStopped = true
	c.evQueue = nil
	c.evMu.Unlock()
	c.evStopOne.Do(func() { close(c.evStop) })
}

// takeEvents hands the pump everything queued so far.
func (c *Client) takeEvents() []Event {
	c.evMu.Lock()
	defer c.evMu.Unlock()
	if len(c.evQueue) == 0 {
		return nil
	}
	batch := c.evQueue
	c.evQueue = nil
	return batch
}

// queueEmpty reports whether the reader has nothing pending.
func (c *Client) queueEmpty() bool {
	c.evMu.Lock()
	defer c.evMu.Unlock()
	return len(c.evQueue) == 0
}

// streamLinger bounds how long the pump parks a trailing streaming chunk
// waiting for its successor to merge into it. It is only paid at the tail
// of a burst and is well under one UI frame.
const streamLinger = 3 * time.Millisecond

// eventPump is the only caller of OnEvent, so delivery order matches pi's
// stdout order. It never holds c.mu: that would block Send/Fire for as
// long as the UI is busy, which is the stall we are removing.
//
// A trailing streaming chunk is parked for up to streamLinger so the next
// chunk of the same kind can merge into it — pi streams text in bursts, and
// merging them keeps one burst from becoming hundreds of UI hops.
func (c *Client) eventPump() {
	defer close(c.evExited)
	linger := time.NewTimer(time.Hour)
	if !linger.Stop() {
		<-linger.C
	}
	defer linger.Stop()

	var hold Event
	holding := false
	// flush delivers a parked run on its own (linger expiry, EOF, or a
	// successor that turned out not to match).
	flush := func() {
		if holding {
			holding = false
			c.dispatch(hold)
		}
	}

	for {
		batch := c.takeEvents()
		if holding {
			batch = append([]Event{hold}, batch...)
		}
		// Park the trailing run (if any) instead of dispatching it: the
		// next batch can still merge into it.
		hold, holding = c.mergeBatch(batch, true)

		var wait <-chan time.Time
		if holding {
			if !linger.Stop() {
				select {
				case <-linger.C:
				default:
				}
			}
			linger.Reset(streamLinger)
			wait = linger.C
		}
		select {
		case <-c.evWake:
		case <-wait:
			// Only give up the parked run when nothing else is waiting —
			// otherwise keep it and let the next round merge into it.
			if c.queueEmpty() {
				flush()
			}
		case <-c.evEOF:
			// Final drain: everything the reader queued is still delivered,
			// parked run first, in order.
			for {
				rest := c.takeEvents()
				if len(rest) == 0 {
					flush()
					return
				}
				if holding {
					rest = append([]Event{hold}, rest...)
					holding = false
				}
				c.mergeBatch(rest, false)
			}
		case <-c.evStop:
			return
		}
	}
}

// mergeBatch walks batch in order, merging consecutive streaming chunks of
// the same kind and content block into one line so a burst of text costs
// one UI hop instead of hundreds. Every other event type is forwarded
// untouched — merging is a streaming optimisation, not a filter.
//
// When park is set and the batch ends in a streaming run, that (already
// merged) run is returned instead of dispatched, so the next batch can
// still merge into it. Nothing is lost either way.
func (c *Client) mergeBatch(batch []Event, park bool) (Event, bool) {
	for i := 0; i < len(batch); {
		typ, index, text, ok := streamingDelta(batch[i])
		if !ok {
			c.dispatch(batch[i])
			i++
			continue
		}
		j := i + 1
		for j < len(batch) {
			t2, index2, s2, ok2 := streamingDelta(batch[j])
			if !ok2 || t2 != typ || index2 != index {
				break
			}
			text += s2
			j++
		}
		ev := batch[i]
		if j > i+1 {
			ev = mergeDelta(ev, text)
		}
		if park && j == len(batch) {
			return ev, true
		}
		c.dispatch(ev)
		i = j
	}
	return Event{}, false
}

func (c *Client) dispatch(ev Event) {
	// Copy the callback out under the lock, then call it unlocked: the
	// callback re-enters the UI and may itself rewire the client.
	if fn := c.eventFn(); fn != nil {
		fn(ev)
	}
}

// SetOnEvent installs the event callback. The UI thread calls this once after
// Spawn (and again after a respawn); the pump and the process waiter read it
// from their own goroutines.
func (c *Client) SetOnEvent(fn func(Event)) {
	c.cbMu.Lock()
	c.onEv = fn
	c.cbMu.Unlock()
}

// eventFn returns the current callback, or nil when none is installed.
func (c *Client) eventFn() func(Event) {
	c.cbMu.RLock()
	defer c.cbMu.RUnlock()
	return c.onEv
}

// streamingDelta reports whether ev is an assistant text/thinking stream
// chunk and returns its merge key (delta type + content block) plus the
// chunk payload. Only those two merge: every other delta carries
// structured data (toolCall arguments, ids) that concatenation would
// corrupt. contentIndex is part of the key so two adjacent text blocks
// never bleed into each other.
func streamingDelta(ev Event) (typ string, contentIndex int, text string, ok bool) {
	if ev.Type != "message_update" {
		return "", 0, "", false
	}
	var mu struct {
		Event struct {
			Type         string `json:"type"`
			ContentIndex int    `json:"contentIndex"`
			Delta        string `json:"delta"`
		} `json:"assistantMessageEvent"`
	}
	if json.Unmarshal(ev.Raw, &mu) != nil {
		return "", 0, "", false
	}
	switch mu.Event.Type {
	case "text_delta", "thinking_delta":
		return mu.Event.Type, mu.Event.ContentIndex, mu.Event.Delta, true
	}
	return "", 0, "", false
}

// mergeDelta returns base with its delta payload replaced by text; every
// other field is carried over verbatim, so the merged line parses exactly
// like the chunks it replaces.
func mergeDelta(base Event, text string) Event {
	var top map[string]json.RawMessage
	if json.Unmarshal(base.Raw, &top) != nil {
		return base
	}
	var inner map[string]json.RawMessage
	if json.Unmarshal(top["assistantMessageEvent"], &inner) != nil {
		return base
	}
	payload, err := json.Marshal(text)
	if err != nil {
		return base
	}
	inner["delta"] = payload
	innerRaw, err := json.Marshal(inner)
	if err != nil {
		return base
	}
	top["assistantMessageEvent"] = innerRaw
	merged, err := json.Marshal(top)
	if err != nil {
		return base
	}
	return Event{Type: base.Type, Raw: json.RawMessage(merged)}
}

// Done closes when the pi process exits.
func (c *Client) Done() <-chan struct{} { return c.done }

// PID identifies the owned child so live-session discovery never attaches to it.
func (c *Client) PID() int {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

// nextID mints a request id.
func (c *Client) nextID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	return fmt.Sprintf("go-%d", c.seq)
}

// Send writes one command and waits for its response.
func (c *Client) Send(cmd Command, timeout time.Duration) (Response, error) {
	if cmd.ID == "" {
		cmd.ID = c.nextID()
	}
	raw, err := json.Marshal(cmd)
	if err != nil {
		return Response{}, err
	}
	ch := make(chan Response, 1)
	// One critical section: register + write stay atomic so concurrent
	// Sends can't interleave JSONL lines (Fire already holds it across Write).
	c.mu.Lock()
	c.pending[cmd.ID] = ch
	_, werr := c.stdin.Write(append(raw, '\n'))
	if werr != nil {
		delete(c.pending, cmd.ID)
	}
	c.mu.Unlock()
	if werr != nil {
		return Response{}, werr
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case resp := <-ch:
		if !resp.Success {
			// Typed where pi's text identifies the cause (IsAgentBusy),
			// plain text otherwise.
			return resp, commandError(resp)
		}
		return resp, nil
	case <-timer.C:
		c.mu.Lock()
		delete(c.pending, cmd.ID)
		c.mu.Unlock()
		return Response{}, fmt.Errorf("pi: %s timed out", cmd.Type)
	case <-c.done:
		return Response{}, fmt.Errorf("pi process exited")
	}
}

// Fire writes a command that expects no response (extension_ui_response).
func (c *Client) Fire(cmd Command) error {
	raw, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.stdin.Write(append(raw, '\n'))
	return err
}

// Close kills the pi process. The waiter suppresses pi_exited for an
// intentional close, so reconnects never report a fake crash. The event
// pump is stopped too, so a torn-down client stops feeding the UI.
func (c *Client) Close() {
	c.closed.Store(true)
	c.stopEventPump()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
}

// Convenience wrappers -------------------------------------------------

func (c *Client) Prompt(msg string, images ...ImageContent) (Response, error) {
	return c.Send(Command{Type: "prompt", Message: msg, Images: images}, 60*time.Second)
}

func (c *Client) Steer(msg string, images ...ImageContent) (Response, error) {
	return c.Send(Command{Type: "prompt", Message: msg, Images: images, StreamingBehavior: "steer"}, 60*time.Second)
}

func (c *Client) Abort() (Response, error) {
	return c.Send(Command{Type: "abort"}, 15*time.Second)
}

func (c *Client) ClearQueue() (clearedSteer, clearedFollow []string, err error) {
	resp, err := c.Send(Command{Type: "clear_queue"}, 15*time.Second)
	if err != nil {
		return nil, nil, err
	}
	var data struct {
		Steering []string `json:"steering"`
		FollowUp []string `json:"followUp"`
	}
	if len(resp.Data) > 0 {
		_ = json.Unmarshal(resp.Data, &data)
	}
	return data.Steering, data.FollowUp, nil
}

// NewSession lives in commands.go next to the other session-switching
// wrappers, where the {cancelled:boolean} veto handling is documented.

func (c *Client) GetState() (State, error) {
	var s State
	resp, err := c.Send(Command{Type: "get_state"}, 15*time.Second)
	if err != nil {
		return s, err
	}
	if len(resp.Data) > 0 {
		err = json.Unmarshal(resp.Data, &s)
	}
	return s, err
}

func (c *Client) GetMessages() ([]AgentMessage, error) {
	resp, err := c.Send(Command{Type: "get_messages"}, 15*time.Second)
	if err != nil {
		return nil, err
	}
	var data struct {
		Messages []AgentMessage `json:"messages"`
	}
	if len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, &data); err != nil {
			return nil, err
		}
	}
	return data.Messages, nil
}

func (c *Client) GetStats() (Stats, error) {
	var s Stats
	resp, err := c.Send(Command{Type: "get_session_stats"}, 15*time.Second)
	if err != nil {
		return s, err
	}
	if len(resp.Data) > 0 {
		err = json.Unmarshal(resp.Data, &s)
	}
	return s, err
}

// CycleModel switches to the next model, returns its display label.
func (c *Client) CycleModel() (string, error) {
	resp, err := c.Send(Command{Type: "cycle_model"}, 15*time.Second)
	if err != nil {
		return "", err
	}
	var data struct {
		Model *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"model"`
	}
	if len(resp.Data) > 0 {
		_ = json.Unmarshal(resp.Data, &data)
	}
	if data.Model == nil {
		return "", fmt.Errorf("only one model available")
	}
	if data.Model.ID != "" {
		return data.Model.ID, nil
	}
	return data.Model.Name, nil
}

// GetCommands lists extension commands, prompt templates and skills.
func (c *Client) GetCommands() ([]RepoCommand, error) {
	resp, err := c.Send(Command{Type: "get_commands"}, 15*time.Second)
	if err != nil {
		return nil, err
	}
	var data struct {
		Commands []RepoCommand `json:"commands"`
	}
	if len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, &data); err != nil {
			return nil, err
		}
	}
	return data.Commands, nil
}

// GetModels lists every configured model (including scoped ones).
func (c *Client) GetModels() ([]ModelInfo, error) {
	resp, err := c.Send(Command{Type: "get_available_models"}, 20*time.Second)
	if err != nil {
		return nil, err
	}
	var data struct {
		Models []ModelInfo `json:"models"`
	}
	if len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, &data); err != nil {
			return nil, err
		}
	}
	return data.Models, nil
}

// SetModelByID switches model, returns its display label.
//
// pi answers with the FLAT pi-ai Model object — verified on pi 0.87.1,
// success(id, "set_model", model), so data is
// {id,name,api,provider,baseUrl,reasoning,input,cost,contextWindow,
// maxTokens} — which is why the flat decode comes first. The nested
// {model:{…}} branch below is tolerance for a build that wraps it, not the
// shape pi sends; keeping it costs nothing and cannot mislabel a flat answer
// (a flat answer has no "model" key).
func (c *Client) SetModelByID(provider, id string) (string, error) {
	resp, err := c.Send(Command{Type: "set_model", Provider: provider, ModelID: id}, 20*time.Second)
	if err != nil {
		return "", err
	}
	var flat struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	_ = json.Unmarshal(resp.Data, &flat)
	if flat.ID != "" {
		return flat.ID, nil
	}
	if flat.Name != "" {
		return flat.Name, nil
	}
	var nested struct {
		Model *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"model"`
	}
	_ = json.Unmarshal(resp.Data, &nested)
	if nested.Model != nil {
		if nested.Model.ID != "" {
			return nested.Model.ID, nil
		}
		return nested.Model.Name, nil
	}
	return "", nil
}

// GetLevels lists the current model's thinking levels.
func (c *Client) GetLevels() ([]string, error) {
	resp, err := c.Send(Command{Type: "get_available_thinking_levels"}, 15*time.Second)
	if err != nil {
		return nil, err
	}
	var data struct {
		Levels []string `json:"levels"`
	}
	if len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, &data); err != nil {
			return nil, err
		}
	}
	return data.Levels, nil
}

// SetLevel changes the thinking level.
func (c *Client) SetLevel(level string) error {
	_, err := c.Send(Command{Type: "set_thinking_level", Level: level}, 15*time.Second)
	return err
}

// GetTree fetches the session tree + leaf id.
func (c *Client) GetTree() ([]TreeNode, string, error) {
	resp, err := c.Send(Command{Type: "get_tree"}, 15*time.Second)
	if err != nil {
		return nil, "", err
	}
	var data struct {
		Tree   []TreeNode `json:"tree"`
		LeafID string     `json:"leafId"`
	}
	if len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, &data); err != nil {
			return nil, "", err
		}
	}
	return data.Tree, data.LeafID, nil
}

// GetEntries fetches all session entries (for the /session cost breakdown).
func (c *Client) GetEntries() ([]SessionEntry, error) {
	resp, err := c.Send(Command{Type: "get_entries"}, 15*time.Second)
	if err != nil {
		return nil, err
	}
	var data struct {
		Entries []SessionEntry `json:"entries"`
	}
	if len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, &data); err != nil {
			return nil, err
		}
	}
	return data.Entries, nil
}

// SetSteering changes the steering mode ("all" | "one-at-a-time").
func (c *Client) SetSteering(mode string) error {
	_, err := c.Send(Command{Type: "set_steering_mode", Mode: mode}, 15*time.Second)
	return err
}

// SetFollowUp changes the follow-up mode ("all" | "one-at-a-time").
func (c *Client) SetFollowUp(mode string) error {
	_, err := c.Send(Command{Type: "set_follow_up_mode", Mode: mode}, 15*time.Second)
	return err
}

// SetAutoCompact enables/disables auto compaction.
func (c *Client) SetAutoCompact(on bool) error {
	_, err := c.Send(Command{Type: "set_auto_compaction", Enabled: &on}, 15*time.Second)
	return err
}

// SetAutoRetry enables/disables auto retry.
func (c *Client) SetAutoRetry(on bool) error {
	_, err := c.Send(Command{Type: "set_auto_retry", Enabled: &on}, 15*time.Second)
	return err
}
