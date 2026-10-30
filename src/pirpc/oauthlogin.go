package pirpc

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// In-TUI OAuth login. pitago does not re-implement any provider flow: it
// runs pi's own pi-ai OAuth code through a tiny node bridge (embedded
// oauth_bridge.mjs) and relays its events/prompts over JSONL. The
// credential lands in pi's auth.json via pi's own AuthStorage (file lock).

//go:embed oauth_bridge.mjs
var oauthBridgeJS string

// OAuthOption is one choice of a select prompt.
type OAuthOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// OAuthEvent is one bridge event. Type: auth_url | device_code | info |
// progress | prompt | prompt_cancel | done | error.
type OAuthEvent struct {
	Type            string        `json:"type"`
	URL             string        `json:"url,omitempty"`
	Instructions    string        `json:"instructions,omitempty"`
	UserCode        string        `json:"userCode,omitempty"`
	VerificationURI string        `json:"verificationUri,omitempty"`
	Message         string        `json:"message,omitempty"`
	ID              string        `json:"id,omitempty"`
	Kind            string        `json:"kind,omitempty"` // prompt: text | secret | select | manual_code
	Placeholder     string        `json:"placeholder,omitempty"`
	Options         []OAuthOption `json:"options,omitempty"`
}

// OAuthLoginInfo describes one provider pi can log into with OAuth.
type OAuthLoginInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Label string `json:"label"`
}

// OAuthSession is one running login. Events closes after done/error.
type OAuthSession struct {
	Provider string
	Events   <-chan OAuthEvent

	cmd   *exec.Cmd
	stdin io.WriteCloser
	mu    sync.Mutex
	shut  bool
}

// piNodeBin finds node: PATH first, then the node pi's installer ships
// (it is never added to shell profiles).
func piNodeBin() (string, error) {
	if p, err := exec.LookPath("node"); err == nil {
		return p, nil
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		if h, err := os.UserHomeDir(); err == nil {
			data = filepath.Join(h, ".local", "share")
		}
	}
	if p := filepath.Join(data, "pi-node", "current", "bin", "node"); fileExists(p) {
		return p, nil
	}
	return "", errors.New("node not found (needed to run pi's OAuth login)")
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// okPiPkg reports whether dir is a pi-coding-agent package with the auth
// modules the bridge imports.
func okPiPkg(dir string) bool {
	return dir != "" && fileExists(filepath.Join(dir, "dist", "core", "auth-storage.js"))
}

// PiPackageRoot finds the installed pi-coding-agent package dir. Handles
// npm/Homebrew (pi → .../dist/bundle/cli.js), pi's managed installer
// (~/.pi/agent/bin/pi shell launcher → install/releases/<v>), and
// `npm root -g`. Override with PI_PKG_ROOT.
func PiPackageRoot() (string, error) {
	if r := os.Getenv("PI_PKG_ROOT"); r != "" {
		if okPiPkg(r) {
			return r, nil
		}
		return "", fmt.Errorf("bad PI_PKG_ROOT %q", r)
	}
	bin := strings.TrimSpace(os.Getenv("PI_BIN"))
	if bin == "" {
		bin, _ = exec.LookPath("pi")
	}
	if bin != "" {
		p := bin
		if rp, err := filepath.EvalSymlinks(bin); err == nil {
			p = rp
		}
		// Walk up from the resolved binary to the package dir.
		for d := filepath.Dir(p); d != filepath.Dir(d); d = filepath.Dir(d) {
			if okPiPkg(d) {
				return d, nil
			}
		}
		// Managed launcher: <agent>/bin/pi + <agent>/install/current-version.
		agent := filepath.Dir(filepath.Dir(p))
		if v, err := os.ReadFile(filepath.Join(agent, "install", "current-version")); err == nil {
			r := filepath.Join(agent, "install", "releases", strings.TrimSpace(string(v)),
				"node_modules", "@earendil-works", "pi-coding-agent")
			if okPiPkg(r) {
				return r, nil
			}
		}
	}
	if out, err := exec.Command("npm", "root", "-g").Output(); err == nil {
		r := filepath.Join(strings.TrimSpace(string(out)), "@earendil-works", "pi-coding-agent")
		if okPiPkg(r) {
			return r, nil
		}
	}
	return "", errors.New("pi package not found (is pi installed?)")
}

// bridgeCmd writes the embedded bridge to a temp file and builds the node
// command. cleanup removes the temp file once node has loaded it.
func bridgeCmd(args ...string) (*exec.Cmd, func(), error) {
	node, err := piNodeBin()
	if err != nil {
		return nil, nil, err
	}
	root, err := PiPackageRoot()
	if err != nil {
		return nil, nil, err
	}
	f, err := os.CreateTemp("", "pitago-oauth-*.mjs")
	if err != nil {
		return nil, nil, err
	}
	name := f.Name()
	if _, err := f.WriteString(oauthBridgeJS); err != nil {
		f.Close()
		os.Remove(name)
		return nil, nil, err
	}
	f.Close()
	cmd := exec.Command(node, append([]string{name, "--pi-root", root}, args...)...)
	return cmd, func() { os.Remove(name) }, nil
}

var (
	oauthMu   sync.Mutex
	oauthKick sync.Once
	oauthList []OAuthLoginInfo
)

// OAuthLogins lists the providers pi can log into with OAuth. The first
// call starts reading pi's live list in the background and returns the
// static mirror; later calls see the live list once it has landed, so the
// UI never blocks on spawning node.
func OAuthLogins() []OAuthLoginInfo {
	oauthKick.Do(func() { go loadOAuthLogins() })
	oauthMu.Lock()
	defer oauthMu.Unlock()
	if len(oauthList) > 0 {
		return oauthList
	}
	return OAuthLoginsStatic
}

func loadOAuthLogins() {
	cmd, cleanup, err := bridgeCmd("list")
	if err != nil {
		return
	}
	defer cleanup()
	raw, err := cmd.Output()
	if err != nil {
		return
	}
	var ev struct {
		Type string           `json:"type"`
		List []OAuthLoginInfo `json:"list"`
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Type == "providers" && len(ev.List) > 0 {
			oauthMu.Lock()
			oauthList = ev.List
			oauthMu.Unlock()
			return
		}
	}
}

// OAuthLoginsStatic mirrors pi 1.4's OAuth providers (used when the live
// list can't be read).
var OAuthLoginsStatic = []OAuthLoginInfo{
	{"anthropic", "Anthropic (Claude Pro/Max)", ""},
	{"github-copilot", "GitHub Copilot", ""},
	{"kimi-coding", "Kimi Code (subscription)", "Sign in with Kimi Code"},
	{"meta", "Meta (Muse subscription)", "Sign in with Meta"},
	{"openai", "OpenAI (ChatGPT subscription)", "Sign in with ChatGPT"},
	{"openai-codex", "OpenAI (ChatGPT Plus/Pro)", ""},
	{"openrouter", "OpenRouter OAuth", "Sign in with OpenRouter"},
	{"radius", "Radius", ""},
	{"xai", "xAI (Grok/X subscription)", "Sign in with SuperGrok or X Premium"},
}

// OAuthLoginFor returns the OAuth info for provider (ok=false = no OAuth).
func OAuthLoginFor(provider string) (OAuthLoginInfo, bool) {
	for _, p := range OAuthLogins() {
		if p.ID == provider {
			return p, true
		}
	}
	return OAuthLoginInfo{}, false
}

// StartOAuthLogin spawns the bridge for one provider's OAuth login.
func StartOAuthLogin(provider string) (*OAuthSession, error) {
	cmd, cleanup, err := bridgeCmd("login", provider)
	if err != nil {
		return nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cleanup()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cleanup()
		return nil, err
	}
	var stderr strings.Builder
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4096}
	if err := cmd.Start(); err != nil {
		cleanup()
		return nil, err
	}
	ch := make(chan OAuthEvent, 16)
	s := &OAuthSession{Provider: provider, Events: ch, cmd: cmd, stdin: stdin}
	go func() {
		defer close(ch)
		defer cleanup()
		final := false
		br := bufio.NewReaderSize(stdout, 64<<10)
		for {
			line, err := br.ReadString('\n')
			if line = strings.TrimSpace(line); line != "" {
				var ev OAuthEvent
				if json.Unmarshal([]byte(line), &ev) == nil && ev.Type != "" {
					if ev.Type == "done" || ev.Type == "error" {
						final = true
					}
					ch <- ev
				}
			}
			if err != nil {
				break
			}
		}
		werr := cmd.Wait()
		if !final {
			msg := "login process exited"
			if t := strings.TrimSpace(stderr.String()); t != "" {
				msg += ": " + lastLine(t)
			} else if werr != nil {
				msg += ": " + werr.Error()
			}
			ch <- OAuthEvent{Type: "error", Message: msg}
		}
	}()
	return s, nil
}

func lastLine(s string) string {
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}

type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := len(p)
		if k > l.n {
			k = l.n
		}
		l.w.Write(p[:k])
		l.n -= k
	}
	return len(p), nil
}

func (s *OAuthSession) send(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shut || s.stdin == nil {
		return errors.New("login closed")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.stdin.Write(append(raw, '\n'))
	return err
}

// Answer replies to a prompt event (select → option id).
func (s *OAuthSession) Answer(id, value string) error {
	return s.send(map[string]string{"type": "answer", "id": id, "value": value})
}

// Cancel aborts the login; the bridge exits and Events closes.
func (s *OAuthSession) Cancel() {
	_ = s.send(map[string]string{"type": "cancel"})
	s.mu.Lock()
	if !s.shut && s.stdin != nil {
		s.stdin.Close()
	}
	s.shut = true
	s.mu.Unlock()
}
