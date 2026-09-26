package builtin

// The pi builtins that DO have an RPC command.
//
// pitago used to intercept /compact, /fork, /clone, /name and /export and
// answer "needs pi's own TUI". That was wrong: pi declares all of them as
// first-class RPC commands (dist/modes/rpc/rpc-types.d.ts, the same union
// pi's own TUI drives) and drives exactly these from its interactive mode.
// Only the keybindings pi binds locally are TUI-only.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/app"
	"pitago/src/components/format"
)

// compactSession is pi's /compact: summarize the context now. It is an LLM
// call, so it can take minutes — the status line says so and the RPC waits.
// "/compact <instructions>" passes pi's own customInstructions.
func compactSession(m *app.Model, arg string) tea.Cmd {
	m.Status = "compacting context — this is an LLM call, it can take a while…"
	m.Refresh()
	instructions := strings.TrimSpace(arg)
	return func() tea.Msg {
		res, err := m.Pi.Compact(instructions)
		notice := "context compacted"
		switch {
		case res.TokensBefore > 0 && res.EstimatedTokensAfter > 0:
			notice = fmt.Sprintf("context compacted · %s → %s tokens",
				format.FmtComma(res.TokensBefore), format.FmtComma(res.EstimatedTokensAfter))
		case res.TokensBefore > 0:
			notice = fmt.Sprintf("context compacted · from %s tokens", format.FmtComma(res.TokensBefore))
		}
		// Compaction rewrote the transcript in pi, so the chat, the stats
		// and the context gauge have to be re-read (Reload).
		return app.PiOpMsg{Op: "compact", Notice: notice, Err: err, Reload: err == nil}
	}
}

// forkPick lists the branch points pi offers (pi: getUserMessagesForForking
// → get_fork_messages) so /fork can pick one instead of guessing an entry
// id. Paths parallels Options and holds the entry ids Fork takes.
func forkPick(m *app.Model) tea.Cmd {
	m.Status = "loading fork points…"
	m.Refresh()
	return func() tea.Msg {
		msgs, err := m.Pi.GetForkMessages()
		if err != nil {
			return app.PiOpMsg{Op: "fork", Err: err}
		}
		if len(msgs) == 0 {
			return app.PiOpMsg{Op: "fork", Notice: "no messages to fork from"}
		}
		opts, descs, payload, ids := make([]string, 0, len(msgs)), make([]string, 0, len(msgs)),
			make([]string, 0, len(msgs)), make([]string, 0, len(msgs))
		for i, f := range msgs {
			opts = append(opts, forkLabel(f.Text))
			descs = append(descs, fmt.Sprintf("message %d", i+1))
			payload = append(payload, f.Text)
			ids = append(ids, f.EntryID)
		}
		return app.PickerMsg{Kind: "fork", Options: opts, Descs: descs,
			Payload: payload, Paths: ids, Current: opts[len(opts)-1]}
	}
}

// forkLabel keeps a picker row on one line: pi's own selector shows the
// first line of the message.
func forkLabel(text string) string {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(text), "\n", 2)[0])
	if line == "" {
		line = "(empty message)"
	}
	return app.Short(line, 80)
}

// cloneSession duplicates the session at its current position (pi: /clone
// → runtimeHost.fork(leafId,{position:"at"})). A session_before_switch veto
// is reported, not treated as done.
func cloneSession(m *app.Model) tea.Cmd {
	m.Status = "cloning session…"
	m.Refresh()
	return func() tea.Msg {
		res, err := m.Pi.Clone()
		verr := res.VetoErr("clone", err)
		return app.PiOpMsg{Op: "clone", Notice: "cloned to a new session",
			Err: verr, Reload: verr == nil}
	}
}

// nameSession is pi's /name: "/name" with no argument shows the current
// name (or the usage), "/name <name>" sets it — pi persists it as the
// session_info entry its resume picker shows.
func nameSession(m *app.Model, arg string) tea.Cmd {
	name := strings.TrimSpace(arg)
	if name == "" {
		return func() tea.Msg {
			st, err := m.Pi.GetState()
			if err != nil {
				return app.PiOpMsg{Op: "name", Err: err}
			}
			if strings.TrimSpace(st.SessionName) == "" {
				return app.PiOpMsg{Op: "name", Notice: "usage: /name <name>"}
			}
			return app.PiOpMsg{Op: "name", Notice: "session name: " + st.SessionName}
		}
	}
	return func() tea.Msg {
		if err := m.Pi.SetSessionName(name); err != nil {
			return app.PiOpMsg{Op: "name", Err: err}
		}
		return app.PiOpMsg{Op: "name", Notice: "session name set: " + name}
	}
}

// exportSession is pi's /export. pi's RPC exposes export_html only, so a
// .jsonl target is refused with the reason instead of silently exporting
// something else; the HTML path is pi's own writer, not a pitago copy.
func exportSession(m *app.Model, arg string) tea.Cmd {
	path := strings.TrimSpace(arg)
	if strings.HasSuffix(strings.ToLower(path), ".jsonl") {
		m.AddBlock(app.Block{Kind: "notice",
			Text: "pi's RPC only exports HTML — drop the .jsonl suffix, or import the JSONL in pi's own TUI"})
		m.Refresh()
		return nil
	}
	m.Status = "exporting session…"
	m.Refresh()
	return func() tea.Msg {
		written, err := m.Pi.ExportHTML(path)
		if err != nil {
			return app.PiOpMsg{Op: "export", Err: err}
		}
		if written == "" {
			written = "(pi reported no path)"
		}
		return app.PiOpMsg{Op: "export", Notice: "session exported to: " + written}
	}
}
