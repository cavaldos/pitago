package pirpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The pi RPC commands pitago used to refuse to use.
//
// pi declares all of them as first-class commands in
// dist/modes/rpc/rpc-types.d.ts (the same union pi's own TUI drives), so
// every wrapper below is pi-parity, not an invention: "pi builtins don't run
// over RPC" is only true for the keybindings the TUI binds locally. pitago
// was simply re-implementing or omitting them, which cost the user commands
// pi already had (compact, fork, clone, /name, /export-html, !bash, …).
//
// Each wrapper documents the pi ground truth it mirrors. Where pi's payload
// shape is only declared (not exercised) in rpc-types.d.ts, the decoder is
// deliberately tolerant and the doc says so.

// Timeouts for the new commands. They mirror the ones pi's own client uses
// (dist/modes/rpc/rpc-client.d.ts): state changes are fast, compaction and
// a full session export can take a while.
const (
	cmdTimeoutQuick = 15 * time.Second
	cmdTimeoutWork  = 30 * time.Second
	cmdTimeoutLLM   = 60 * time.Second
)

// --- session switching, and the extension veto --------------------------
//
// pi answers new_session, switch_session, fork and clone with
// data {cancelled: boolean} (rpc-types.d.ts). The flag is true when an
// extension vetoed the switch through the session_before_switch hook: the
// command SUCCEEDED, the session did NOT change. Reading it as "done" is how
// a host silently loses the user's session, so every wrapper returns it.

var (
	// ErrVeto matches a session switch an extension cancelled.
	ErrVeto = errors.New("cancelled by an extension")

	// ErrAgentBusy matches pi's refusal to take a prompt while it is still
	// streaming: a prompt without streamingBehavior has nowhere to go, so
	// pi answers success:false instead of guessing (dist/core/agent-session.js
	// "Agent is already processing. Specify streamingBehavior ('steer' or
	// 'followUp') to queue the message.").
	ErrAgentBusy = errors.New("pi agent is already processing")
)

// VetoError reports an extension cancelling a session switch through
// session_before_switch. It unwraps to ErrVeto.
type VetoError struct{ Command string }

func (e *VetoError) Error() string {
	return fmt.Sprintf("pirpc: %s was cancelled by an extension (session_before_switch)", e.Command)
}

func (e *VetoError) Unwrap() error { return ErrVeto }

// IsVeto reports whether err is an extension veto of a session switch.
func IsVeto(err error) bool { return errors.Is(err, ErrVeto) }

// AgentBusyError reports pi refusing a prompt because the agent is still
// streaming. It unwraps to ErrAgentBusy; Message is pi's own text, kept so
// the UI can show it verbatim (it names both queueing modes).
type AgentBusyError struct {
	Command string
	Message string
}

func (e *AgentBusyError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("pirpc: %s: %v", e.Command, ErrAgentBusy)
	}
	return e.Message
}

func (e *AgentBusyError) Unwrap() error { return ErrAgentBusy }

// IsAgentBusy reports whether err is pi's "already processing" refusal.
func IsAgentBusy(err error) bool { return errors.Is(err, ErrAgentBusy) }

// piBusyMarker is pi's own wording for the busy refusal (see ErrAgentBusy).
const piBusyMarker = "Agent is already processing"

// commandError maps a success:false response onto a typed error where pi's
// text identifies the cause, so callers can branch (IsAgentBusy) instead of
// matching strings.
func commandError(resp Response) error {
	if strings.Contains(resp.Error, piBusyMarker) {
		return &AgentBusyError{Command: resp.Command, Message: resp.Error}
	}
	return fmt.Errorf("pi: %s failed: %s", resp.Command, resp.Error)
}

// SwitchResult is pi's answer to the session-switching commands
// (new_session, switch_session, clone): {cancelled: boolean}.
type SwitchResult struct {
	Cancelled bool // an extension vetoed through session_before_switch
}

// VetoErr turns a switch result into an error when an extension vetoed it,
// so a host can surface the refusal with one branch. Returns nil otherwise
// (err is returned untouched).
func (r SwitchResult) VetoErr(command string, err error) error {
	if err != nil {
		return err
	}
	if r.Cancelled {
		return &VetoError{Command: command}
	}
	return nil
}

func decodeSwitch(resp Response) (SwitchResult, error) {
	var out SwitchResult
	if len(resp.Data) > 0 {
		// A missing/!data payload means "not cancelled" — pi only sends
		// data on these commands, and older builds send none.
		_ = json.Unmarshal(resp.Data, &out)
	}
	return out, nil
}

// NewSession starts a fresh session (pi: new_session{}), optionally as a
// child of an existing one (pi: new_session{parentSession}).
//
// The old NewSession() swallowed the veto, so a cancelled Ctrl+N looked like
// a successful new session while the conversation stayed where it was.
// A veto-aware host calls NewSessionResult.
func (c *Client) NewSessionResult(parentSession string) (SwitchResult, error) {
	resp, err := c.Send(Command{Type: "new_session", ParentSession: parentSession}, cmdTimeoutWork)
	if err != nil {
		return SwitchResult{}, err
	}
	return decodeSwitch(resp)
}

// NewSession keeps the pre-existing signature for callers that only care
// whether pi accepted the command; it drops the veto flag.
func (c *Client) NewSession() error {
	_, err := c.NewSessionResult("")
	return err
}

// SwitchSession resumes a session file (pi: switch_session{sessionPath} —
// sessionPath is REQUIRED, it is the path from ListSessions / get_state).
func (c *Client) SwitchSession(sessionPath string) (SwitchResult, error) {
	if strings.TrimSpace(sessionPath) == "" {
		// pi requires the field; sending an empty one would resume an
		// arbitrary session (or the newest) instead of what the user picked.
		return SwitchResult{}, fmt.Errorf("pirpc: switch_session needs a session path")
	}
	resp, err := c.Send(Command{Type: "switch_session", SessionPath: sessionPath}, cmdTimeoutQuick)
	if err != nil {
		return SwitchResult{}, err
	}
	return decodeSwitch(resp)
}

// Fork re-branches the session at an entry (pi: fork{entryId}; entryId comes
// from GetTree / GetEntries). pi answers {text, cancelled}: text is the
// message that starts the forked turn, cancelled is the session_before_switch
// veto.
func (c *Client) Fork(entryID string) (ForkResult, error) {
	if strings.TrimSpace(entryID) == "" {
		return ForkResult{}, fmt.Errorf("pirpc: fork needs an entry id")
	}
	resp, err := c.Send(Command{Type: "fork", EntryID: entryID}, cmdTimeoutWork)
	if err != nil {
		return ForkResult{}, err
	}
	var out ForkResult
	if len(resp.Data) > 0 {
		_ = json.Unmarshal(resp.Data, &out)
	}
	return out, nil
}

// ForkResult is pi's fork answer: the text pi replays into the new branch
// plus the session_before_switch veto flag.
type ForkResult struct {
	SwitchResult
	Text string
}

// VetoErr mirrors SwitchResult.VetoErr for fork.
func (r ForkResult) VetoErr(command string, err error) error {
	return r.SwitchResult.VetoErr(command, err)
}

// Clone duplicates the current session (pi: clone{}) and switches to the
// copy. Answers {cancelled: boolean} like the other switch commands.
func (c *Client) Clone() (SwitchResult, error) {
	resp, err := c.Send(Command{Type: "clone"}, cmdTimeoutWork)
	if err != nil {
		return SwitchResult{}, err
	}
	return decodeSwitch(resp)
}

// ForkMessage is one row of get_fork_messages: the entry the fork would
// branch at and its text (rpc-types.d.ts: {entryId, text}).
type ForkMessage struct {
	EntryID string `json:"entryId"`
	Text    string `json:"text"`
}

// GetForkMessages lists the branch points pi offers for a fork. The entry
// ids it returns are exactly what Fork(entryID) takes.
func (c *Client) GetForkMessages() ([]ForkMessage, error) {
	resp, err := c.Send(Command{Type: "get_fork_messages"}, cmdTimeoutQuick)
	if err != nil {
		return nil, err
	}
	var data struct {
		Messages []ForkMessage `json:"messages"`
	}
	if len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, &data); err != nil {
			return nil, err
		}
	}
	return data.Messages, nil
}

// SetSessionName names the current session (pi: set_session_name{name}); pi
// persists it as the session_info entry the resume picker shows.
func (c *Client) SetSessionName(name string) error {
	_, err := c.Send(Command{Type: "set_session_name", Name: name}, cmdTimeoutQuick)
	return err
}

// --- compaction ---------------------------------------------------------

// CompactionResult is pi's compact answer (CompactionResult in
// dist/core/compaction/compaction.d.ts). Only the fields a UI shows.
type CompactionResult struct {
	Summary              string `json:"summary"`
	FirstKeptEntryID     string `json:"firstKeptEntryId"`
	TokensBefore         int    `json:"tokensBefore"`
	EstimatedTokensAfter int    `json:"estimatedTokensAfter"`
}

// Compact summarizes the conversation now (pi: compact{customInstructions?}).
// An empty customInstructions compacts with pi's own instructions. This is an
// LLM call, hence the long timeout.
func (c *Client) Compact(customInstructions string) (CompactionResult, error) {
	var out CompactionResult
	resp, err := c.Send(Command{Type: "compact", CustomInstructions: customInstructions}, cmdTimeoutLLM*4)
	if err != nil {
		return out, err
	}
	if len(resp.Data) > 0 {
		err = json.Unmarshal(resp.Data, &out)
	}
	return out, err
}

// --- bash ---------------------------------------------------------------

// BashResult is pi's bash answer (BashResult in
// dist/core/bash-executor.d.ts). ExitCode is absent (nil) when the command
// was killed, so it is a pointer rather than an int.
type BashResult struct {
	Output         string `json:"output"`
	ExitCode       *int   `json:"exitCode,omitempty"`
	Cancelled      bool   `json:"cancelled"`
	Truncated      bool   `json:"truncated"`
	FullOutputPath string `json:"fullOutputPath,omitempty"`
}

// ExitCodeOr returns the exit code, or -1 when pi reports none (killed).
func (r BashResult) ExitCodeOr(def int) int {
	if r.ExitCode == nil {
		return def
	}
	return *r.ExitCode
}

// Bash runs a shell command through pi (pi: bash{command, excludeFromContext?})
// instead of spawning pitago's own shell: the command then runs in pi's cwd
// and its output is a normal session entry, visible to the model and to the
// transcript. excludeFromContext keeps the output out of the model's context
// while still showing it (pi: false — the default — is omitted from the wire,
// which is what pi does).
func (c *Client) Bash(command string, excludeFromContext bool) (BashResult, error) {
	var out BashResult
	if strings.TrimSpace(command) == "" {
		return out, fmt.Errorf("pirpc: bash needs a command")
	}
	resp, err := c.Send(Command{Type: "bash", ShellCommand: command, ExcludeFromContext: &excludeFromContext}, cmdTimeoutWork)
	if err != nil {
		return out, err
	}
	if len(resp.Data) > 0 {
		err = json.Unmarshal(resp.Data, &out)
	}
	return out, err
}

// AbortBash cancels a running bash command (pi: abort_bash{}), like pi's
// Esc on a running !command.
func (c *Client) AbortBash() error {
	_, err := c.Send(Command{Type: "abort_bash"}, cmdTimeoutQuick)
	return err
}

// AbortRetry stops pi's automatic retry after a provider failure
// (pi: abort_retry{}), so the user is not stuck in a retry loop.
func (c *Client) AbortRetry() error {
	_, err := c.Send(Command{Type: "abort_retry"}, cmdTimeoutQuick)
	return err
}

// --- thinking level -----------------------------------------------------

// CycleThinkingLevel advances the thinking level like pi's cycle and returns
// the new one. pi answers {level} (rpc-types.d.ts) — or data:null when the
// model has no levels, which is reported as "".
func (c *Client) CycleThinkingLevel() (string, error) {
	resp, err := c.Send(Command{Type: "cycle_thinking_level"}, cmdTimeoutQuick)
	if err != nil {
		return "", err
	}
	var data struct {
		Level string `json:"level"`
	}
	if len(resp.Data) > 0 {
		_ = json.Unmarshal(resp.Data, &data)
	}
	return data.Level, nil
}

// --- conversation reads -------------------------------------------------

// GetLastAssistantText returns the last assistant text (pi:
// get_last_assistant_text → {text: string | null}), i.e. pi's "copy last
// answer" source. An empty string means pi has none yet (null).
func (c *Client) GetLastAssistantText() (string, error) {
	resp, err := c.Send(Command{Type: "get_last_assistant_text"}, cmdTimeoutQuick)
	if err != nil {
		return "", err
	}
	var data struct {
		Text *string `json:"text"`
	}
	if len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, &data); err != nil {
			return "", err
		}
	}
	if data.Text == nil {
		return "", nil
	}
	return *data.Text, nil
}

// ExportHTML writes the session as a shareable HTML transcript (pi:
// export_html{outputPath?}) and returns the path pi wrote to. An empty
// outputPath lets pi choose its own location (pi answers {path}).
func (c *Client) ExportHTML(outputPath string) (string, error) {
	resp, err := c.Send(Command{Type: "export_html", OutputPath: outputPath}, cmdTimeoutLLM)
	if err != nil {
		return "", err
	}
	var data struct {
		Path string `json:"path"`
	}
	if len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, &data); err != nil {
			return "", err
		}
	}
	return data.Path, nil
}

// --- follow-up ----------------------------------------------------------

// FollowUp queues a message to run after the current turn
// (pi: follow_up{message, images}). It is the second half of the
// streamingBehaviour pi demands: a prompt sent while pi is streaming is
// refused (ErrAgentBusy) unless it is a steer or a follow-up.
func (c *Client) FollowUp(msg string, images ...ImageContent) (Response, error) {
	return c.Send(Command{Type: "follow_up", Message: msg, Images: images}, cmdTimeoutLLM)
}
