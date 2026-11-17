// Package ev is pitago's own UI event vocabulary: messages, commands and the
// reducer-style Model contract that src/app is written against.
//
// It mirrors the subset of Bubble Tea v1 the app used (same names, same key
// strings) so the app no longer imports any TUI runtime directly. A runtime
// adapter (src/tui/teabridge today, the tview runtime next) translates its
// native input into these types and executes the returned Cmds.
package ev

import "time"

// Msg is any event delivered to Model.Update.
type Msg interface{}

// Cmd is deferred work run off the UI goroutine; its result is fed back
// into Update. A nil Cmd is a no-op.
type Cmd func() Msg

// Model is the reducer contract the runtime drives.
type Model interface {
	Init() Cmd
	Update(Msg) (Model, Cmd)
	View() string
}

// WindowSizeMsg reports the terminal size (sent once at start and on resize).
type WindowSizeMsg struct {
	Width  int
	Height int
}

// QuitMsg asks the runtime to shut down.
type QuitMsg struct{}

// Quit is a Cmd that quits the program.
func Quit() Msg { return QuitMsg{} }

// MouseModeMsg asks the runtime to switch mouse reporting.
// All=true enables all-motion tracking, false disables the mouse.
type MouseModeMsg struct{ All bool }

// EnableMouseAllMotion is a Cmd turning on all-motion mouse reporting.
func EnableMouseAllMotion() Msg { return MouseModeMsg{All: true} }

// DisableMouse is a Cmd turning mouse reporting off.
func DisableMouse() Msg { return MouseModeMsg{All: false} }

// BatchMsg runs its Cmds concurrently, unordered.
type BatchMsg []Cmd

// SequenceMsg runs its Cmds one at a time, in order.
type SequenceMsg []Cmd

// Batch combines Cmds to run concurrently. Nil Cmds are dropped; zero
// Cmds give nil and a single Cmd is returned as is.
func Batch(cmds ...Cmd) Cmd { return compact[BatchMsg](cmds) }

// Sequence combines Cmds to run in order (same compaction as Batch).
func Sequence(cmds ...Cmd) Cmd { return compact[SequenceMsg](cmds) }

func compact[T ~[]Cmd](cmds []Cmd) Cmd {
	var valid []Cmd
	for _, c := range cmds {
		if c != nil {
			valid = append(valid, c)
		}
	}
	switch len(valid) {
	case 0:
		return nil
	case 1:
		return valid[0]
	default:
		return func() Msg { return T(valid) }
	}
}

// Tick fires fn once after d. The timer starts when Tick is called, not
// when the runtime executes the Cmd (Bubble Tea v1 semantics).
func Tick(d time.Duration, fn func(time.Time) Msg) Cmd {
	t := time.NewTimer(d)
	return func() Msg {
		ts := <-t.C
		return fn(ts)
	}
}

// Sender delivers a Msg into the running program from any goroutine.
type Sender func(Msg)
