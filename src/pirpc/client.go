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
// is forwarded to OnEvent. OnEvent runs on the reader goroutine — the
// caller must bounce it to the UI thread (e.g. tea.Program.Send).
type Client struct {
	cmd     *exec.Cmd
	stdin   *os.File
	mu      sync.Mutex
	pending map[string]chan Response
	seq     int
	OnEvent func(Event)
	done    chan struct{}
	once    sync.Once
	closed  atomic.Bool // intentional Close: waiter skips pi_exited
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
		if !c.closed.Load() && c.OnEvent != nil {
			c.OnEvent(Event{Type: "pi_exited"})
		}
	}()
	return c, nil
}

// readLoop splits stdout on '\n' only (protocol requirement) and routes lines.
// One unmarshal per line: Response carries type+id, so the envelope and the
// response share a single parse; events keep the raw line bytes (cloned —
// the bufio buffer is reused — for the UI thread to parse once).
func (c *Client) readLoop(r *os.File) {
	defer r.Close()
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
		if c.OnEvent != nil {
			c.OnEvent(Event{Type: resp.Type, Raw: bytes.Clone(line)})
		}
	}
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
			return resp, fmt.Errorf("pi: %s failed: %s", resp.Command, resp.Error)
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
// intentional close, so reconnects never report a fake crash.
func (c *Client) Close() {
	c.closed.Store(true)
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

func (c *Client) NewSession() error {
	_, err := c.Send(Command{Type: "new_session"}, 30*time.Second)
	return err
}

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
func (c *Client) SetModelByID(provider, id string) (string, error) {
	resp, err := c.Send(Command{Type: "set_model", Provider: provider, ModelID: id}, 20*time.Second)
	if err != nil {
		return "", err
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
	var flat struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	_ = json.Unmarshal(resp.Data, &flat)
	if flat.ID != "" {
		return flat.ID, nil
	}
	return flat.Name, nil
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
