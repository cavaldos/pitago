package pirpc

import (
	"encoding/json"
	"strings"
)

// ImageContent is one vision attachment for prompt/steer/follow_up.
// Matches pi's RPC type: {type:"image", data:<base64>, mimeType}.
type ImageContent struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

// Command is one JSONL line sent to pi stdin. Only set the fields your
// command needs; the rest are omitted. Message has NO omitempty: pi does
// command.message.startsWith(...) unguarded, so a missing message crashes
// the prompt ("Cannot read properties of undefined") — tray-only sends
// (images, empty text) must still transmit "message":"".
type Command struct {
	ID                 string `json:"id,omitempty"`
	Type               string `json:"type"`
	Message            string `json:"message"`
	Images             []ImageContent `json:"images,omitempty"`
	StreamingBehavior  string `json:"streamingBehavior,omitempty"`
	ShellCommand       string `json:"command,omitempty"`
	SessionPath        string `json:"sessionPath,omitempty"`
	ParentSession      string `json:"parentSession,omitempty"`
	Provider           string `json:"provider,omitempty"`
	ModelID            string `json:"modelId,omitempty"`
	Level              string `json:"level,omitempty"`
	Mode               string `json:"mode,omitempty"`
	Enabled            *bool  `json:"enabled,omitempty"`
	Name               string `json:"name,omitempty"`
	CustomInstructions string `json:"customInstructions,omitempty"`
	OutputPath         string `json:"outputPath,omitempty"`
	EntryID            string `json:"entryId,omitempty"`
	Since              string `json:"since,omitempty"`
	// extension_ui_response fields
	Value     *string `json:"value,omitempty"`
	Confirmed *bool   `json:"confirmed,omitempty"`
	Cancelled *bool   `json:"cancelled,omitempty"`
}

// Response is pi's reply to a command (matched by ID).
type Response struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Error   string          `json:"error,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Event is any non-response line from pi stdout.
type Event struct {
	Type string
	Raw  json.RawMessage
}

// ContentBlock is one entry of an assistant/toolResult content array.
// User echoes may also carry {"type":"image"} blocks (no text payload).
type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ImageCount returns how many {"type":"image"} blocks raw holds.
func ImageCount(raw json.RawMessage) int {
	n := 0
	for _, b := range BlocksOf(raw) {
		if b.Type == "image" {
			n++
		}
	}
	return n
}

// AgentMessage is one row of get_messages (role: user/assistant/toolResult/bashExecution).
type AgentMessage struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content,omitempty"`
	Command      string          `json:"command,omitempty"` // bashExecution
	Output       string          `json:"output,omitempty"`  // bashExecution
	ExitCode     int             `json:"exitCode,omitempty"`
	ToolCallID   string          `json:"toolCallId,omitempty"` // toolResult
	ToolName     string          `json:"toolName,omitempty"`   // toolResult
	Details      json.RawMessage `json:"details,omitempty"`    // toolResult (e.g. edit diff)
	IsError      bool            `json:"isError,omitempty"`
	StopReason   string          `json:"stopReason,omitempty"`   // assistant
	ErrorMessage string          `json:"errorMessage,omitempty"` // assistant
}

// TextOf joins all {"type":"text"} blocks; plain-string content also works.
func TextOf(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []ContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	out := ""
	for _, b := range blocks {
		if b.Type == "text" {
			out += b.Text
		}
	}
	return out
}

// BlocksOf parses a content array (nil when content is a plain string).
func BlocksOf(raw json.RawMessage) []ContentBlock {
	var blocks []ContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil
	}
	return blocks
}

// State mirrors get_state data (subset we display).
type State struct {
	Model struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Provider      string `json:"provider"`
		ContextWindow int    `json:"contextWindow"`
	} `json:"model"`
	ThinkingLevel  string `json:"thinkingLevel"`
	IsStreaming    bool   `json:"isStreaming"`
	SteeringMode   string `json:"steeringMode"`
	FollowUpMode   string `json:"followUpMode"`
	AutoCompaction bool   `json:"autoCompactionEnabled"`
	SessionFile    string `json:"sessionFile"`
	SessionID      string `json:"sessionId"`
	SessionName    string `json:"sessionName"`
	MessageCount   int    `json:"messageCount"`
}

// ModelCost is the per-1M pricing pi reports on each model.
type ModelCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// ModelInfo is one entry of get_available_models.
type ModelInfo struct {
	ID               string             `json:"id"`
	Name             string             `json:"name"`
	Provider         string             `json:"provider"`
	API              string             `json:"api"`
	BaseURL          string             `json:"baseUrl"`
	Reasoning        bool               `json:"reasoning"`
	Input            []string           `json:"input"`
	Cost             ModelCost          `json:"cost"`
	ContextWindow    int                `json:"contextWindow"`
	MaxTokens        int                `json:"maxTokens"`
	ThinkingLevelMap map[string]*string `json:"thinkingLevelMap"`
}

// TreeEntry is one session entry; TreeNode forms the get_tree hierarchy.
// Field names mirror pi's SessionEntry/SessionTreeNode JSON: message entries
// carry Message, model_change carries Provider/ModelID, thinking_level_change
// carries ThinkingLevel, compaction carries Summary/TokensBefore,
// branch_summary carries Summary, custom/custom_message carry CustomType
// (+Content), label entries carry Label, session_info carries Name.
type TreeEntry struct {
	Type          string          `json:"type"`
	ID            string          `json:"id"`
	ParentID      *string         `json:"parentId,omitempty"`
	Timestamp     string          `json:"timestamp,omitempty"`
	Provider      string          `json:"provider,omitempty"`
	ModelID       string          `json:"modelId,omitempty"`
	ThinkingLevel string          `json:"thinkingLevel,omitempty"`
	Summary       string          `json:"summary,omitempty"`
	TokensBefore  int             `json:"tokensBefore,omitempty"`
	CustomType    string          `json:"customType,omitempty"`
	Content       json.RawMessage `json:"content,omitempty"`
	Label         string          `json:"label,omitempty"`
	Name          string          `json:"name,omitempty"`
	Message       AgentMessage    `json:"message"`
}

type TreeNode struct {
	Entry          TreeEntry  `json:"entry"`
	Children       []TreeNode `json:"children"`
	Label          string     `json:"label,omitempty"`
	LabelTimestamp string     `json:"labelTimestamp,omitempty"`
}

// Stats mirrors get_session_stats data (subset we display).
type Stats struct {
	SessionID     string  `json:"sessionId"`
	SessionFile   string  `json:"sessionFile"`
	TotalMessages int     `json:"totalMessages"`
	UserMsgs      int     `json:"-"`
	AsstMsgs      int     `json:"-"`
	ToolCalls     int     `json:"toolCalls"`
	ToolResults   int     `json:"-"`
	Cost          float64 `json:"cost"`
	In            int     `json:"-"`
	Out           int     `json:"-"`
	CacheRead     int     `json:"-"`
	CacheWrite    int     `json:"-"`
	TokensTotal   int     `json:"-"`
	ContextPct    float64 `json:"-"`
	ContextToks   int     `json:"-"`
	ContextWin    int     `json:"-"`
}

func (s *Stats) UnmarshalJSON(data []byte) error {
	var wire struct {
		SessionID     string  `json:"sessionId"`
		SessionFile   string  `json:"sessionFile"`
		TotalMessages int     `json:"totalMessages"`
		UserMessages  int     `json:"userMessages"`
		AssistantMsgs int     `json:"assistantMessages"`
		ToolCalls     int     `json:"toolCalls"`
		ToolResults   int     `json:"toolResults"`
		Cost          float64 `json:"cost"`
		Tokens        struct {
			Input      int `json:"input"`
			Output     int `json:"output"`
			CacheRead  int `json:"cacheRead"`
			CacheWrite int `json:"cacheWrite"`
			Total      int `json:"total"`
		} `json:"tokens"`
		ContextUsage *struct {
			Tokens        *int     `json:"tokens"`
			ContextWindow int      `json:"contextWindow"`
			Percent       *float64 `json:"percent"`
		} `json:"contextUsage"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	s.SessionID = wire.SessionID
	s.SessionFile = wire.SessionFile
	s.TotalMessages = wire.TotalMessages
	s.UserMsgs = wire.UserMessages
	s.AsstMsgs = wire.AssistantMsgs
	s.ToolCalls = wire.ToolCalls
	s.ToolResults = wire.ToolResults
	s.Cost = wire.Cost
	s.In = wire.Tokens.Input
	s.Out = wire.Tokens.Output
	s.CacheRead = wire.Tokens.CacheRead
	s.CacheWrite = wire.Tokens.CacheWrite
	s.TokensTotal = wire.Tokens.Total
	if wire.ContextUsage != nil {
		if wire.ContextUsage.Tokens != nil {
			s.ContextToks = *wire.ContextUsage.Tokens
		}
		s.ContextWin = wire.ContextUsage.ContextWindow
		if wire.ContextUsage.Percent != nil {
			s.ContextPct = *wire.ContextUsage.Percent
		}
	}
	return nil
}

// EntryUsage is the token/cost payload on usage entries and messages.
type EntryUsage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
	Cost       struct {
		Total float64 `json:"total"`
	} `json:"cost"`
}

// EntryMessage is the message payload of a message-type session entry.
type EntryMessage struct {
	Role          string      `json:"role"`
	Provider      string      `json:"provider"`
	Model         string      `json:"model"`
	ResponseModel string      `json:"responseModel"`
	Usage         *EntryUsage `json:"usage"`
}

// SessionEntry is one row of get_entries (only the fields the
// usage/cost breakdown needs; the rest is ignored).
type SessionEntry struct {
	Type     string        `json:"type"`
	Provider string        `json:"provider"`
	Model    string        `json:"model"`
	Usage    *EntryUsage   `json:"usage"`
	Message  *EntryMessage `json:"message"`
}

// Delta is the assistantMessageEvent inside message_update.
type Delta struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
	Delta        string `json:"delta,omitempty"`
	Reason       string `json:"reason,omitempty"` // done: stop | toolUse | ...
	ID           string `json:"id,omitempty"`
	ToolName     string `json:"toolName,omitempty"`
	ToolCall     *struct {
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"toolCall,omitempty"`
}

// MessageUpdate is the parsed message_update event.
type MessageUpdate struct {
	Usage struct {
		Input  int `json:"input"`
		Output int `json:"output"`
		Cost   struct {
			Total float64 `json:"total"`
		} `json:"cost"`
	} `json:"usage"`
	Event Delta `json:"assistantMessageEvent"`
}

// UIRequest is an extension_ui_request (permission dialogs etc.).
type UIRequest struct {
	ID          string   `json:"id"`
	Method      string   `json:"method"`
	Title       string   `json:"title,omitempty"`
	Message     string   `json:"message,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Options     []string `json:"options,omitempty"`
	NotifyType  string   `json:"notifyType,omitempty"`
	StatusKey   string   `json:"statusKey,omitempty"`
	StatusText  string   `json:"statusText,omitempty"`
	Text        string   `json:"text,omitempty"`
}

// Queue mirrors queue_update.
type Queue struct {
	Steering []string `json:"steering"`
	FollowUp []string `json:"followUp"`
}

// SourceInfo tells which extension a command comes from (get_commands
// sourceInfo; absent for prompt/skill entries and builtins).
type SourceInfo struct {
	Source  string `json:"source"`
	Scope   string `json:"scope"` // user | project | ...
	Path    string `json:"path,omitempty"`
	BaseDir string `json:"baseDir,omitempty"`
	Origin  string `json:"origin,omitempty"`
}

// RepoCommand is one runnable /command: extension, prompt template or skill.
type RepoCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"` // builtin | pitago | extension | prompt | skill
	Location    string `json:"location,omitempty"`
	Path        string `json:"path,omitempty"`
	SourceInfo  *SourceInfo `json:"sourceInfo,omitempty"`
}

// SourceTag mirrors pi's getAutocompleteSourceTag: "u:npm:pi-subagents",
// "u", "p", "t". "" when there is no source info — pi then leaves the
// description untagged (prompt/skill entries, builtins).
func (c RepoCommand) SourceTag() string {
	if c.SourceInfo == nil {
		return ""
	}
	var prefix string
	switch c.SourceInfo.Scope {
	case "user":
		prefix = "u"
	case "project":
		prefix = "p"
	default:
		prefix = "t"
	}
	src := strings.TrimSpace(c.SourceInfo.Source)
	switch src {
	case "auto", "local", "cli":
		return prefix
	}
	if strings.HasPrefix(src, "npm:") {
		return prefix + ":" + src
	}
	if g := gitTag(src); g != "" {
		return prefix + ":" + g
	}
	return prefix
}

// gitTag compacts a git extension source to pi's "git:<host>/<path>[@ref]"
// (best-effort port of parseGitUrl; "" → caller falls back to scope only).
func gitTag(src string) string {
	u := strings.TrimSpace(src)
	hasPrefix := len(u) >= 4 && strings.EqualFold(u[:4], "git:")
	if hasPrefix {
		u = strings.TrimSpace(u[4:])
	}
	lower := strings.ToLower(u)
	explicit := strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "ssh://") ||
		strings.HasPrefix(lower, "git://") ||
		strings.HasPrefix(lower, "git@")
	if !hasPrefix && !explicit {
		return "" // pi: without git: prefix, only explicit protocol URLs
	}
	for _, p := range []string{"https://", "http://", "ssh://", "git://"} {
		if len(u) >= len(p) && strings.EqualFold(u[:len(p)], p) {
			u = u[len(p):]
			break
		}
	}
	if len(u) >= 4 && strings.EqualFold(u[:4], "git@") {
		u = u[4:]
		if i := strings.Index(u, ":"); i >= 0 {
			u = u[:i] + "/" + u[i+1:]
		}
	}
	ref := ""
	if i := strings.LastIndex(u, "#"); i >= 0 {
		ref, u = u[i+1:], u[:i]
	}
	u = strings.Trim(strings.TrimSuffix(u, ".git"), "/")
	host, _, ok := strings.Cut(u, "/")
	if !ok || (host != "localhost" && !strings.Contains(host, ".")) {
		return ""
	}
	if ref != "" {
		return "git:" + u + "@" + ref
	}
	return "git:" + u
}

// ParseQueue extracts queue_update payloads (arrays may be absent → nil).
func ParseQueue(raw json.RawMessage) Queue {
	var q Queue
	_ = json.Unmarshal(raw, &q)
	return q
}
