package pirpc

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// treeExtTS is the pi extension behind NavigateTree and SetTreeLabel (see
// tree_ext.ts), embedded so a single pitago binary stays self-contained.
//
//go:embed tree_ext.ts
var treeExtTS []byte

// The extension commands /tree runs. Both are internal: GetCommands hides
// them so they never show in the "/" popup.
const (
	TreeNavCommand   = "pitago-tree"
	TreeLabelCommand = "pitago-tree-label"
)

// ErrTreeExtUnavailable means the running pi did not load the tree
// extension (the file could not be written, or an older pi rejected it).
var ErrTreeExtUnavailable = errors.New("pi did not load pitago's tree extension, so the session tree cannot be navigated")

// TreeNavTimeout bounds one navigation. A branch summary is a full model
// call over the abandoned branch, so it gets far more than a plain RPC.
const TreeNavTimeout = 30 * time.Minute

// InstallTreeExt writes the tree extension under the user cache dir and
// returns its path, for Options.Extensions.
func InstallTreeExt() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return InstallTreeExtIn(filepath.Join(dir, "pitago", "pi-ext"))
}

// InstallTreeExtIn is InstallTreeExt with the target directory given.
// Unchanged content is left alone, so the file's mtime does not churn.
func InstallTreeExtIn(dir string) (string, error) {
	path := filepath.Join(dir, "pitago-tree.ts")
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, treeExtTS) {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// Write then rename, so a pi starting in parallel never loads half a file.
	tmp, err := os.CreateTemp(dir, ".pitago-tree-*.ts")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(treeExtTS); err != nil {
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

// TreeNavOptions are pi's navigateTree options that /tree sets.
type TreeNavOptions struct {
	Summarize bool
	// CustomInstructions is the "Summarize with custom prompt" text; nil
	// when the user did not ask for one.
	CustomInstructions *string
}

// TreeNavResult is navigateTree's outcome.
type TreeNavResult struct {
	// Cancelled: pi did not move (an extension vetoed it, or the branch
	// summary was aborted).
	Cancelled bool
	// Unknown: the command ran but its report never arrived, so the
	// caller has to re-read the session to see what happened.
	Unknown bool
}

// NavigateTree moves the session to targetID the way pi's /tree does
// (session.navigateTree): a user or custom message moves the leaf to its
// parent and its text goes back to the editor, any other entry becomes the
// leaf, and Summarize writes a branch summary of the branch being left.
func (c *Client) NavigateTree(targetID string, opt TreeNavOptions) (TreeNavResult, error) {
	if err := c.requireCommand(TreeNavCommand); err != nil {
		return TreeNavResult{}, err
	}
	f, err := os.CreateTemp("", "pitago-tree-*.json")
	if err != nil {
		return TreeNavResult{}, err
	}
	out := f.Name()
	f.Close()
	defer os.Remove(out)
	req := map[string]any{"targetId": targetID, "summarize": opt.Summarize, "out": out}
	if opt.CustomInstructions != nil {
		req["customInstructions"] = *opt.CustomInstructions
	}
	if _, err := c.runExtCommand(TreeNavCommand, req, TreeNavTimeout); err != nil {
		return TreeNavResult{}, err
	}
	raw, _ := os.ReadFile(out)
	if len(bytes.TrimSpace(raw)) == 0 {
		return TreeNavResult{Unknown: true}, nil
	}
	var rep struct {
		Cancelled bool    `json:"cancelled"`
		Error     *string `json:"error"`
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		return TreeNavResult{Unknown: true}, nil
	}
	if rep.Error != nil {
		return TreeNavResult{}, errors.New(*rep.Error)
	}
	return TreeNavResult{Cancelled: rep.Cancelled}, nil
}

// SetTreeLabel sets an entry's label, or clears it when label is blank
// (pi's appendLabelChange from the tree's label editor).
func (c *Client) SetTreeLabel(entryID, label string) error {
	if err := c.requireCommand(TreeLabelCommand); err != nil {
		return err
	}
	_, err := c.runExtCommand(TreeLabelCommand, map[string]any{"entryId": entryID, "label": label}, 60*time.Second)
	return err
}

// requireCommand refuses to send an internal command pi does not know: the
// text would otherwise start a model turn with "/pitago-tree …" as the
// prompt.
func (c *Client) requireCommand(name string) error {
	cmds, err := c.getCommandsRaw()
	if err != nil {
		return err
	}
	if !hasCommand(cmds, name) {
		return ErrTreeExtUnavailable
	}
	return nil
}

// runExtCommand runs "/<name> <json>" and waits for pi to finish it: pi
// answers an extension command's prompt only once the handler returned.
func (c *Client) runExtCommand(name string, req any, timeout time.Duration) (Response, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	// JSON never contains a raw newline, so the args stay on one line.
	msg := "/" + name + " " + strings.TrimSpace(string(b))
	return c.Send(Command{Type: "prompt", Message: msg}, timeout)
}

// isTreeCommand reports pitago's internal tree commands.
func isTreeCommand(name string) bool {
	return name == TreeNavCommand || name == TreeLabelCommand
}
