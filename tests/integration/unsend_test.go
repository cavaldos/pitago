// End-to-end coverage for the take-back of a cancelled prompt: a real `pi
// --mode rpc` child loads pitago's embedded unsend extension, talks to a
// local stand-in model server, and pirpc.Unsend must drop exactly the
// prompts the model never answered.
//
// Hermetic: PI_CODING_AGENT_DIR, the extension dir and the child's cwd are
// t.TempDir(), and the "model" is a loopback HTTP server, so no real
// provider, credential or session of the developer is touched.
package integration

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"pitago/src/pirpc"
)

// fakeModel is an OpenAI-compatible streaming endpoint. It records each
// request body; with answer set it streams one text chunk first. Either
// way it then holds the stream open until pi aborts it.
type fakeModel struct {
	answer atomic.Bool
	mu     sync.Mutex
	bodies []string
}

func (f *fakeModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.bodies = append(f.bodies, string(body))
	f.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	fl := w.(http.Flusher)
	fl.Flush()
	if f.answer.Load() {
		fmt.Fprint(w, `data: {"id":"x","object":"chat.completion.chunk","created":1,"model":"fake",`+
			`"choices":[{"index":0,"delta":{"role":"assistant","content":"hi there"},"finish_reason":null}]}`+"\n\n")
		fl.Flush()
	}
	<-r.Context().Done()
}

func (f *fakeModel) requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.bodies)
}

func (f *fakeModel) lastBody() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bodies) == 0 {
		return ""
	}
	return f.bodies[len(f.bodies)-1]
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestUnsendTakesBackOnlyUnansweredPrompts(t *testing.T) {
	bin := requirePi(t)
	model := &fakeModel{}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: model}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	root := t.TempDir()
	agent := filepath.Join(root, "agent")
	work := filepath.Join(root, "work")
	for _, d := range []string{agent, work} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	models := fmt.Sprintf(`{"providers":{"fakeprov":{"baseUrl":"http://%s/v1",`+
		`"api":"openai-completions","apiKey":"x","models":[{"id":"fake"}]}}}`, ln.Addr())
	if err := os.WriteFile(filepath.Join(agent, "models.json"), []byte(models), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	ext, err := pirpc.InstallUnsendExtIn(filepath.Join(root, "ext"))
	if err != nil {
		t.Fatal(err)
	}

	c, err := pirpc.Spawn(pirpc.Options{Bin: bin, Provider: "fakeprov", Model: "fake", Dir: work,
		NoExtensions: true, Extensions: []string{ext}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	var sawText atomic.Bool
	c.SetOnEvent(func(e pirpc.Event) {
		if e.Type == "message_update" && strings.Contains(string(e.Raw), `"text_delta"`) {
			sawText.Store(true)
		}
	})
	waitFor(t, "pi to start", func() bool { _, err := c.GetStateWithin(2 * time.Second); return err == nil })

	cmds, err := c.GetCommands()
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range cmds {
		if cmd.Name == pirpc.UnsendCommand {
			t.Fatal("GetCommands must hide the internal unsend command")
		}
	}

	userTexts := func() []string {
		msgs, err := c.GetMessages()
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, m := range msgs {
			if m.Role == "user" {
				out = append(out, pirpc.TextOf(m.Content))
			}
		}
		return out
	}
	// send prompts, waits until the model server has the request (and,
	// when answering, until pi streamed the text), then aborts and asks
	// pi to take the prompt back.
	send := func(text string, answer bool) bool {
		t.Helper()
		model.answer.Store(answer)
		sawText.Store(false)
		n := model.requests()
		at := time.Now()
		if _, err := c.Prompt(text); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the model request", func() bool { return model.requests() > n })
		if answer {
			waitFor(t, "the streamed answer", sawText.Load)
		}
		if _, err := c.Abort(); err != nil {
			t.Fatal(err)
		}
		ok, err := c.Unsend(at)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}

	if !send("first prompt", false) {
		t.Error("an unanswered prompt must be taken back")
	}
	if got := userTexts(); len(got) != 0 {
		t.Errorf("session still holds %q", got)
	}
	if send("second prompt", true) {
		t.Error("an answered prompt must stay")
	}
	if !send("third prompt", false) {
		t.Error("an unanswered prompt after an answered one must be taken back")
	}
	if got := userTexts(); strings.Join(got, "|") != "second prompt" {
		t.Errorf("session user messages = %q, want only the answered one", got)
	}

	// The model's next request carries none of the taken-back prompts.
	n := model.requests()
	if _, err := c.Prompt("fourth prompt"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the next model request", func() bool { return model.requests() > n })
	body := model.lastBody()
	for _, gone := range []string{"first prompt", "third prompt"} {
		if strings.Contains(body, gone) {
			t.Errorf("taken-back %q reached the model", gone)
		}
	}
	for _, kept := range []string{"second prompt", "fourth prompt"} {
		if !strings.Contains(body, kept) {
			t.Errorf("%q missing from the model request", kept)
		}
	}
	_, _ = c.Abort()
}
