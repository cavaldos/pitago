package app

import tea "github.com/charmbracelet/bubbletea"

// Live /trajectory: while the window is open, every pi event that can add
// or finish a step re-reads the session tree in the background and swaps
// the rows in place. The cursor always jumps to the newest step when one
// lands; reloads with no new step keep the selection by entry id. One reload runs at a
// time: steps landing meanwhile mark the model dirty and one more reload
// runs when the current one lands, so a busy turn never queues a backlog.

// trajLiveEvent reports whether a pi event may change the trajectory.
func trajLiveEvent(t string) bool {
	switch t {
	case "message_end", "tool_execution_end", "agent_end", "agent_settled", "compaction_end":
		return true
	}
	return false
}

// trajOpen returns the open trajectory window (nil when none).
func (m *Model) trajOpen() *Dialog {
	for _, d := range m.Dialogs {
		if d.Kind == "trajectory" {
			return d
		}
	}
	return nil
}

// trajLiveReload starts a background reload after evType when the window
// is open. It reads the local pi, so a followed foreign session (whose
// events are not this pi's) never triggers it.
func (m *Model) trajLiveReload(evType string) tea.Cmd {
	if !trajLiveEvent(evType) || m.followRemote {
		return nil
	}
	d := m.trajOpen()
	if d == nil {
		return nil
	}
	if m.trajLoading {
		m.trajDirty = true
		return nil
	}
	cmd := m.RunBuiltin(BuiltinTrajectoryLive, d.Scope)
	if cmd != nil {
		m.trajLoading = true
		m.trajDirty = false
	}
	return cmd
}

// trajLiveDone clears the in-flight latch and runs the pending reload, if
// a step landed while the last one was loading.
func (m *Model) trajLiveDone() tea.Cmd {
	m.trajLoading = false
	if !m.trajDirty {
		return nil
	}
	m.trajDirty = false
	if d := m.trajOpen(); d != nil && !m.followRemote {
		if cmd := m.RunBuiltin(BuiltinTrajectoryLive, d.Scope); cmd != nil {
			m.trajLoading = true
			return cmd
		}
	}
	return nil
}

// applyTrajLive swaps fresh rows into the open window. Errors and empty
// snapshots keep the current rows: a transient RPC failure must not wipe
// what the user is reading.
func (m *Model) applyTrajLive(msg TrajectoryMsg) {
	d := m.trajOpen()
	if d == nil || msg.Err != nil || len(msg.Options) == 0 || msg.Scope != d.Scope {
		return
	}
	selID := trajSelID(d)
	off := d.TrajOff

	d.Options, d.Descs, d.Payload, d.TrajIDs = msg.Options, msg.Descs, msg.Payload, msg.IDs
	cursor := d.Cursor
	d.Reindex() // keeps the user's filter; resets TrajOff
	d.Cursor = cursor
	newest := trajIDAt(d, msg.Newest)
	if newest != "" && newest != d.TrajNewest {
		// A new step landed: the cursor always jumps to it.
		trajFocusNewest(d, msg.Newest)
		m.Refresh()
		return
	}
	if selID != "" {
		for fi, ri := range d.FIdx {
			if ri < len(d.TrajIDs) && d.TrajIDs[ri] == selID {
				d.Cursor = fi
				break
			}
		}
	}
	if d.Cursor >= len(d.FIdx) {
		d.Cursor = len(d.FIdx) - 1
	}
	if d.Cursor < 0 {
		d.Cursor = 0
	}
	// Same step still selected: keep the reader's scroll position (the
	// renderer clamps it if the detail got shorter).
	if n := len(d.FIdx); selID != "" && d.Cursor < n {
		if ri := d.FIdx[d.Cursor]; ri < len(d.TrajIDs) && d.TrajIDs[ri] == selID {
			d.TrajOff = off
		}
	}
	m.Refresh()
}

// trajIDAt is the entry id of raw step ri ("" when out of range).
func trajIDAt(d *Dialog, ri int) string {
	if ri < 0 || ri >= len(d.TrajIDs) {
		return ""
	}
	return d.TrajIDs[ri]
}

// trajSelID is the entry id of the selected step ("" when none).
func trajSelID(d *Dialog) string {
	if d.Cursor < 0 || d.Cursor >= len(d.FIdx) {
		return ""
	}
	return trajIDAt(d, d.FIdx[d.Cursor])
}

// trajFocusNewest puts the cursor on raw step ri (the newest one) and
// remembers it. When the filter hides that step, the last visible step
// is selected instead.
func trajFocusNewest(d *Dialog, ri int) {
	d.TrajNewest = trajIDAt(d, ri)
	d.TrajOff = 0
	if len(d.FIdx) == 0 {
		d.Cursor = 0
		return
	}
	d.Cursor = len(d.FIdx) - 1
	for fi, r := range d.FIdx {
		if r == ri {
			d.Cursor = fi
			return
		}
	}
}
