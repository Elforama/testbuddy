package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
)

// callAs is call with the request's command name set, as the daemon sets it.
func callAs(t *testing.T, cmd string, h handler, args map[string]any) (daemon.Response, []daemon.Response) {
	t.Helper()
	return call(t, func(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
		req.Cmd = cmd
		return h(ctx, req, send)
	}, args, "")
}

// a1Busy opens session a1 and starts a tb do on it that stays mid-step until unblock is closed;
// done is closed when the do ends.
func a1Busy(t *testing.T) (s *Service, unblock, done chan struct{}) {
	t.Helper()
	t.Setenv("TB_HOME", t.TempDir())
	f := newFake("emulator-5750", "Pixel 7", device.Android)
	f.show(loginScreen()...)
	s = New()
	s.openDrv = func(context.Context, device.Info) (driver.Driver, error) { return f, nil }
	if _, err := s.connect(context.Background(), f.info, "a1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.CloseAll(context.Background()) })
	started, unblock := blockTyping(f)
	done = make(chan struct{})
	go func() {
		callAs(t, "do", s.do, map[string]any{"device": "a1", "commands": "inputText: hello"})
		close(done)
	}()
	waitFor(t, started, "the do's step")
	return s, unblock, done
}

// issue #401: a command sent while another runs on the same session waited for it with no
// output, so a caller could not tell a queued command from a hung one. After a moment it now
// says what it waits for, and again when it starts.
func TestQueuedCommandSaysWhatItWaitsFor(t *testing.T) {
	s, unblock, done := a1Busy(t)
	type result struct {
		r     daemon.Response
		parts []daemon.Response
	}
	looked := make(chan result, 1)
	go func() {
		r, parts := callAs(t, "look", s.look, map[string]any{"device": "a1"})
		looked <- result{r, parts}
	}()
	time.Sleep(1500 * time.Millisecond)
	close(unblock)
	<-done
	res := <-looked
	var said []string
	for _, p := range res.parts {
		said = append(said, p.Text)
	}
	all := strings.Join(said, "")
	if !res.r.OK || !strings.Contains(all, "waiting for the command running on a1 (Pixel 7): tb do inputText: hello (started ") || !strings.Contains(all, "now running tb look") {
		t.Errorf("issue #401: the queued look said %q, then %+v", all, res.r)
	}
}

// issue #401: the waiting notes are stderr parts (not the command's output); --no-wait fails at
// once, saying what runs.
func TestQueuedNotesAndNoWait(t *testing.T) {
	s, unblock, done := a1Busy(t)
	start := time.Now()
	r, _ := callAs(t, "look", s.look, map[string]any{"device": "a1", "no_wait": true})
	if r.OK || !strings.Contains(r.Text, "a1 (Pixel 7) is running a command: tb do inputText: hello (started ") || time.Since(start) > time.Second {
		t.Errorf("--no-wait: %+v after %s", r, time.Since(start))
	}
	old := queuedNoteAfter
	queuedNoteAfter = 50 * time.Millisecond
	t.Cleanup(func() { queuedNoteAfter = old })
	looked := make(chan []daemon.Response, 1)
	go func() {
		_, parts := callAs(t, "look", s.look, map[string]any{"device": "a1"})
		looked <- parts
	}()
	time.Sleep(200 * time.Millisecond)
	close(unblock)
	<-done
	parts := <-looked
	if len(parts) != 2 || !parts[0].Stderr || !parts[1].Stderr {
		t.Errorf("notes: %+v", parts)
	}
}

// Review of issue #401: the CLI sends tb do's commands as a YAML list; the note names them
// without the list's dashes.
func TestDescribeYAMLList(t *testing.T) {
	req := daemon.Request{Cmd: "do", Args: []byte(`{"commands":"- launchApp: com.example\n- tapOn: Login\n"}`)}
	if got := describe(req); got != "tb do launchApp: com.example; tapOn: Login" {
		t.Errorf("describe: %q", got)
	}
	// tb wait's command, with options under it: only the command is named.
	req.Args = []byte(`{"commands":"- extendedWaitUntil:\n    visible: \"Hi\"\n    timeout: 17000\n- tapOn:\n    id: login\n"}`)
	if got := describe(req); got != "tb do extendedWaitUntil; tapOn" {
		t.Errorf("describe, nested: %q", got)
	}
}
