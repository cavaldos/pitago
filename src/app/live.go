package app

import (
	"context"
	"encoding/json"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/live"
	"pitago/src/pirpc"
)

type liveMsg struct {
	generation uint64
	message    live.Message
}

func (m Model) runLiveBridge() tea.Cmd {
	bridge := m.liveBridge
	if bridge == nil {
		bridge = &live.Bridge{CWD: m.cwd, OwnPID: m.Pi.PID()}
	}
	generation := m.liveGeneration
	return func() tea.Msg {
		cmd := bridge.Start(context.Background(), func(message live.Message) {
			if ProgRef != nil {
				ProgRef.Send(liveMsg{generation: generation, message: message})
			}
		})
		cmd()
		return nil
	}
}

// applyLive bridges the transport into existing transcript/event rendering.
// No remote path calls Pi.Send: remote events are observational only.
func (m *Model) applyLive(generation uint64, message live.Message) tea.Cmd {
	if generation != m.liveGeneration {
		return nil // queued before Ctrl+D; never resurrect detached follow mode
	}
	var liveCmd tea.Cmd
	if message.Connected {
		m.liveConnected = true
		m.followRemote = true
		m.remoteSession = message.Descriptor.SessionID
		m.session = ShortID(message.Descriptor.SessionID)
		m.Status = "following external Pi · read-only"
		m.Dialogs = nil
		m.clearTeamWidgetState()
		m.closeAt()
		m.cmdOpen = false
		m.ta.Blur()
	}
	if message.Disconnected {
		m.clearTeamWidgetState()
		m.liveConnected = false
		if m.followRemote {
			m.Status = "external Pi disconnected · reconnecting…"
		}
	}
	if snap := message.Snapshot; snap != nil {
		m.followRemote = true
		m.liveConnected = true
		m.remoteSession = snap.SessionID
		m.session = snap.SessionName
		if m.session == "" {
			m.session = ShortID(snap.SessionID)
		}
		m.ModelLbl = snap.Model
		if m.ModelLbl == "" {
			m.ModelLbl = "external"
		}
		m.thinkLvl = snap.ThinkingLevel
		m.blocks = nil
		m.clearTeamWidgetState()
		m.tools = make(map[string]int)
		m.progressByKey = make(map[string]int)
		m.curAsst, m.curThink = -1, -1
		m.asstDelta, m.thinkDelta = false, false
		messages := make([]pirpc.AgentMessage, 0, len(snap.Messages))
		for _, raw := range snap.Messages {
			var msg pirpc.AgentMessage
			if json.Unmarshal(raw, &msg) == nil {
				messages = append(messages, msg)
			}
		}
		m.restore(messages)
		liveCmd = m.setRemoteRunning(snap.IsStreaming)
	}
	if event := message.Event; event != nil {
		m.followRemote = true
		if jsonEventType(event.Raw) == "session_info_changed" {
			var info struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(event.Raw, &info) == nil {
				if info.Name != "" {
					m.session = info.Name
				} else {
					m.session = ShortID(m.remoteSession)
				}
			}
		}
		// handleEvent contains the canonical message/tool rendering. Its returned
		// commands may query the owned Pi, so deliberately do not execute them.
		eventType := jsonEventType(event.Raw)
		nm, _ := m.handleEvent(pirpc.Event{Type: eventType, Raw: event.Raw})
		*m = nm.(Model)
		m.liveConnected = true
		switch eventType {
		case "agent_start":
			liveCmd = m.setRemoteRunning(true)
		case "agent_settled":
			liveCmd = m.setRemoteRunning(false)
		default:
			m.Status = "following external Pi · read-only"
		}
	}
	m.Refresh()
	return liveCmd
}

// setRemoteRunning mirrors Pi's busy state into the sidebar and chat gutter.
// It returns only pet animation commands; it never schedules owned Pi RPCs.
func (m *Model) setRemoteRunning(running bool) tea.Cmd {
	m.thinking = running
	if running {
		m.Status = "external Pi running · read-only"
		m.pet.inTurn = true
		if m.pet.status.Busy() {
			if !m.pet.ticking {
				m.pet.ticking = true
				return petTickCmd()
			}
			return nil
		}
		return m.petSet(petWorking)
	}
	m.Status = "following external Pi · read-only"
	m.pet.inTurn = false
	if m.pet.status.Busy() {
		return m.petSet(petIdle)
	}
	return nil
}

func jsonEventType(raw json.RawMessage) string {
	var envelope struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &envelope)
	return envelope.Type
}

// detachLive returns to the owned session and fetches a fresh owned snapshot.
func (m *Model) detachLive() tea.Cmd {
	m.liveGeneration++ // invalidate every transport message already in the UI queue
	if m.liveBridge != nil {
		m.liveBridge.Stop()
	}
	m.followRemote = false
	m.liveConnected = false
	m.remoteSession = ""
	m.Dialogs = nil
	m.clearTeamWidgetState()
	m.closeAt()
	m.cmdOpen = false
	m.ta.Focus()
	m.Status = "detached external Pi · reconnecting owned session…"
	m.Refresh()
	return m.fetchAll()
}

// handleFollowKey preserves navigation/quit/yank controls while blocking every
// path that could type, steer, abort, switch model/session, or open a command.
func (m Model) handleFollowKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	if !m.followRemote {
		return m, nil, false
	}
	switch msg.Type {
	case tea.KeyCtrlD:
		return m, m.detachLive(), true
	case tea.KeyCtrlC, tea.KeyCtrlB, tea.KeyCtrlY, tea.KeyCtrlO, tea.KeyCtrlG:
		return m, nil, false // handled by the normal switch below
	case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd:
		return m, nil, false // viewport scrolling
	}
	m.Status = "external Pi is read-only · Ctrl+D detaches"
	m.Refresh()
	return m, nil, true
}
