package pirpc

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Fake-pi harness for the command wrappers.
//
// It is the same stdlib fake the wire suite uses (tests/fakepi, driven
// through PI_BIN), built from the test itself so the senders in this package
// are verified against a real JSONL pipe instead of only against a
// hand-built struct.
//
// Its fixtures are pinned to the payload shapes the installed pi 0.87.1
// actually produces, verified there and not from taste: set_model answers the
// FLAT pi-ai Model object, get_entries carries message / usage / model_change
// entries as pi writes them (no top-level provider/model on a message
// entry), and the session switches answer {cancelled}. A fixture that
// invents a shape pi never sends makes the decoders look correct while
// production drifts, so the shapes that were wrong are pinned by the tests
// below and in tests/wire.

// fakePiBin builds tests/fakepi once per test and returns the binary path.
func fakePiBin(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fakepi")
	cmd := exec.Command("go", "build", "-o", bin, "../../tests/fakepi")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build tests/fakepi: %v\n%s", err, out)
	}
	return bin
}

// fakeRequest is one line the fake received.
type fakeRequest struct {
	Type string `json:"type"`
	Raw  string `json:"raw"`
}

// startFakePi launches the fake with its own transcript and the given
// FAKEPI_* switches, and returns the client plus the transcript file.
func startFakePi(t *testing.T, env map[string]string) (*Client, string) {
	t.Helper()
	bin := fakePiBin(t)
	log := filepath.Join(t.TempDir(), "fakepi.jsonl")
	cmd := exec.Command(bin, "--mode", "rpc")
	cmd.Env = append(os.Environ(),
		"FAKEPI_LOG="+log,
		"FAKEPI_VETO="+env["FAKEPI_VETO"],
		"FAKEPI_BUSY="+env["FAKEPI_BUSY"],
		"FAKEPI_FAIL="+env["FAKEPI_FAIL"],
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fakepi: %v", err)
	}
	// One waiter only: cmd.Wait is not safe to call twice, and the goroutine
	// below owns it. Cleanup just kills the process and closes the pipe.
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = stdin.Close()
	})

	c := &Client{pending: make(map[string]chan Response), done: make(chan struct{})}
	c.stdin = stdin.(*os.File)
	go c.readLoop(stdout.(*os.File))
	go func() {
		_ = cmd.Wait()
		c.once.Do(func() { close(c.done) })
	}()
	// No Client.Close here: this hand-built client has no exec.Cmd to kill
	// and the cleanup above owns the process.
	return c, log
}

// mintedID matches the request ids Client generates itself.
var mintedID = regexp.MustCompile(`^go-[0-9]+$`)

// rawRequests returns every line the fake received, with the generated id
// replaced by "<id>" so the golden payloads are stable.
func rawRequests(t *testing.T, log string) []string {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read transcript: %v", err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var rec struct {
			Kind string `json:"kind"`
			Raw  string `json:"raw"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("transcript line %q: %v", line, err)
		}
		if rec.Kind != "request" {
			continue
		}
		var envelope struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(rec.Raw), &envelope); err != nil {
			t.Fatalf("request %q: %v", rec.Raw, err)
		}
		if mintedID.MatchString(envelope.ID) {
			out = append(out, strings.Replace(rec.Raw, `"id":"`+envelope.ID+`"`, `"id":"<id>"`, 1))
			continue
		}
		out = append(out, rec.Raw)
	}
	return out
}

// lastRequest is the newest line the fake received (the command just sent).
func lastRequest(t *testing.T, log string) string {
	t.Helper()
	reqs := rawRequests(t, log)
	if len(reqs) == 0 {
		t.Fatal("fakepi received no request")
	}
	return reqs[len(reqs)-1]
}
