package pirpc

// timeout_test.go — the typed timeout, which is what lets the startup probe
// tell "pi never answered" apart from "pi answered and refused".
//
// The old check was a string match on " timed out". pi's own refusal text is
// embedded verbatim in a commandError, so a pi saying "upstream timed out"
// would have been classified as a timeout and retried forever.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A refusal whose text happens to contain "timed out" must NOT look like a
// timeout: retrying it just burns the whole readiness budget.
func TestIsTimeoutIgnoresRefusalText(t *testing.T) {
	refusal := commandError(Response{Command: "get_state", Error: "upstream provider timed out"})
	if !strings.Contains(refusal.Error(), " timed out") {
		t.Fatalf("sanity: the refusal text must contain the old marker, got %q", refusal)
	}
	if IsTimeout(refusal) {
		t.Fatalf("a refusal containing \"timed out\" was classified as a timeout: %v", refusal)
	}
	if IsTimeout(nil) {
		t.Fatal("nil is not a timeout")
	}
}

// The typed error is what IsTimeout keys on: it unwraps to ErrTimeout and
// keeps the historical message callers already display.
func TestTimeoutErrorIsTyped(t *testing.T) {
	err := error(&TimeoutError{Command: "get_state", Wait: 5 * time.Second})
	if !IsTimeout(err) {
		t.Fatalf("IsTimeout = false for %v", err)
	}
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("TimeoutError must unwrap to ErrTimeout: %v", err)
	}
	if err.Error() != "pi: get_state timed out" {
		t.Fatalf("message changed: %q", err.Error())
	}
	// Wrapping must keep it recognizable: the startup probe wraps the last
	// timeout in a "did not answer after N attempts" message.
	if !IsTimeout(fmt.Errorf("pi did not answer get_state after 3 attempts: %w", err)) {
		t.Fatal("a wrapped timeout must still be a timeout")
	}
}

// Against a real child: a get_state that is never answered is a timeout, and
// the same fake answering normally is not.
func TestIsTimeoutOnRealPipe(t *testing.T) {
	log := t.TempDir() + "/fakepi.jsonl"
	t.Setenv("FAKEPI_LOG", log)
	t.Setenv("FAKEPI_DELAY", "get_state=1s")
	bin := fakePiBin(t)
	cmd := exec.Command(bin, "--mode", "rpc")
	cmd.Env = append(os.Environ(), "FAKEPI_LOG="+log, "FAKEPI_DELAY=get_state=1s")
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

	if _, err := c.GetStateWithin(150 * time.Millisecond); !IsTimeout(err) {
		t.Fatalf("a slow get_state must be a typed timeout, got %v", err)
	}
	if _, err := c.GetStateWithin(5 * time.Second); err != nil {
		t.Fatalf("the same get_state inside a roomy window: %v", err)
	}
}
