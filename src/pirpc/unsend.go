package pirpc

import (
	"bytes"
	_ "embed"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// unsendExtTS is the pi extension behind Unsend (see unsend_ext.ts). It is
// embedded so a single pitago binary stays self-contained.
//
//go:embed unsend_ext.ts
var unsendExtTS []byte

// UnsendCommand is the extension command Unsend runs. It is internal:
// GetCommands hides it so it never shows in the "/" popup.
const UnsendCommand = "pitago-unsend"

// ErrUnsendUnavailable means the running pi did not load the unsend
// extension (the file could not be written, or an older pi rejected it).
// Callers fall back to a plain cancel.
var ErrUnsendUnavailable = errors.New("pirpc: pitago-unsend extension not loaded")

// InstallUnsendExt writes the unsend extension under the user cache dir and
// returns its path, for Options.Extensions. Unchanged content is left alone,
// so the file's mtime does not churn between runs.
func InstallUnsendExt() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return InstallUnsendExtIn(filepath.Join(dir, "pitago", "pi-ext"))
}

// InstallUnsendExtIn is InstallUnsendExt with the target directory given.
func InstallUnsendExtIn(dir string) (string, error) {
	path := filepath.Join(dir, "pitago-unsend.ts")
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, unsendExtTS) {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// Write then rename, so a pi starting in parallel never loads half a file.
	tmp, err := os.CreateTemp(dir, ".pitago-unsend-*.ts")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(unsendExtTS); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}

// Unsend takes back the prompt pitago sent at `since`, provided the model
// has not answered it yet. pi then drops the prompt from the session's
// context (the leaf moves back to before it, in the same session file).
//
// Call it only after the turn was aborted. It reports true when no user
// message sent at or after `since` is left in the session.
func (c *Client) Unsend(since time.Time) (bool, error) {
	cmds, err := c.getCommandsRaw()
	if err != nil {
		return false, err
	}
	if !hasCommand(cmds, UnsendCommand) {
		// Never send the text without the command behind it: pi would
		// start a model turn with "/pitago-unsend …" as the prompt.
		return false, ErrUnsendUnavailable
	}
	ms := since.UnixMilli()
	if _, err := c.Prompt("/" + UnsendCommand + " " + strconv.FormatInt(ms, 10)); err != nil {
		return false, err
	}
	// The command answers "handled" even when it chose to do nothing, so
	// read the session back instead of trusting the reply.
	msgs, err := c.GetMessages()
	if err != nil {
		return false, err
	}
	return !hasUserMessageSince(msgs, ms), nil
}

func hasCommand(cmds []RepoCommand, name string) bool {
	for _, cmd := range cmds {
		if cmd.Name == name {
			return true
		}
	}
	return false
}

// hasUserMessageSince reports whether a user message stamped at or after
// ms (unix milliseconds) is still in the session.
func hasUserMessageSince(msgs []AgentMessage, ms int64) bool {
	for _, msg := range msgs {
		if msg.Role == "user" && int64(msg.Timestamp) >= ms {
			return true
		}
	}
	return false
}

// hideInternalCommands drops pitago's own extension commands from a
// get_commands answer.
func hideInternalCommands(cmds []RepoCommand) []RepoCommand {
	out := cmds[:0]
	for _, cmd := range cmds {
		if cmd.Name != UnsendCommand && !isTreeCommand(cmd.Name) {
			out = append(out, cmd)
		}
	}
	return out
}
