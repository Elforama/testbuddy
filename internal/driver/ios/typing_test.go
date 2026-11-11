package ios

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/wire"
)

// Issue #46: type and erase tell the agent how long the host waits for their answer, so the
// agent answers first (with an error saying what it waited for) instead of the host timing
// out blind.
func TestTypingSendsTheReplyLimit(t *testing.T) {
	r := actionRunner(t)
	d := newIOSDriver(t, r)
	if _, err := d.TypeText(context.Background(), "hello", driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	// 5 s timeout + 40 ms a byte + 15 s: the limit call() waits for the reply.
	if got := num(t, r.last("type"), "replyMs"); got != 20200 {
		t.Errorf("type replyMs = %v, want 20200 (%v)", got, r.last("type"))
	}
	if _, err := d.EraseText(context.Background(), 3, driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := num(t, r.last("erase"), "replyMs"); got != 20060 {
		t.Errorf("erase replyMs = %v, want 20060 (%v)", got, r.last("erase"))
	}
	// A context that ends sooner shortens the wait, and the agent is told so.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := d.TypeText(ctx, "hello", driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := num(t, r.last("type"), "replyMs"); got > 3000 || got < 2000 {
		t.Errorf("type replyMs = %v under a 3 s context (%v)", got, r.last("type"))
	}
	if _, err := d.Tap(ctxT(t), 1, 2, driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.last("tap")["replyMs"]; ok {
		t.Errorf("tap sent replyMs: %v", r.last("tap"))
	}
}

// Issue #46: an agent that gave up before sending any key (the app did not answer
// accessibility in time) says so with "transient"; the host asks once more, as a new request.
// Keys that may have gone are never sent again: such an error is not transient.
func TestTypingAsksAgainOnlyWhenNoKeyWasSent(t *testing.T) {
	notStarted := map[string]any{"ok": false, "transient": true,
		"error": "typing did not start: the app did not answer accessibility for 16.0 s (checking which field has keyboard focus); no keys were sent"}
	ok := map[string]any{"result": "changed", "seq": 5}
	cases := []struct {
		name    string
		replies []map[string]any
		asks    int
		fails   bool
	}{
		{"answers the second time", []map[string]any{notStarted, ok}, 2, false},
		{"asks once more only", []map[string]any{notStarted, notStarted, ok}, 2, true},
		{"keys may have gone", []map[string]any{{"ok": false, "error": "typing 2 characters: 1 of 2 characters confirmed, the next 1 not (the input was not confirmed within 3.0 s): they may still arrive, so they are not sent again"}, ok}, 1, true},
		{"other errors", []map[string]any{{"ok": false, "error": "expected keyboard focus on name, observed none"}, ok}, 1, true},
	}
	for _, op := range []string{"type", "erase"} {
		for _, c := range cases {
			t.Run(op+" "+c.name, func(t *testing.T) {
				r := newFakeRunner(t)
				var mu sync.Mutex
				n := 0
				r.on(op, func(map[string]any) map[string]any {
					mu.Lock()
					defer mu.Unlock()
					reply := c.replies[min(n, len(c.replies)-1)]
					n++
					out := map[string]any{}
					for k, v := range reply {
						out[k] = v
					}
					return out
				})
				d := newIOSDriver(t, r)
				var err error
				if op == "type" {
					_, err = d.TypeText(ctxT(t), "hi", driver.ActionOpts{})
				} else {
					_, err = d.EraseText(ctxT(t), 2, driver.ActionOpts{})
				}
				reqs := r.requests(op)
				if len(reqs) != c.asks {
					t.Fatalf("asked %d times, want %d: %v", len(reqs), c.asks, reqs)
				}
				if c.asks == 2 && reqs[0]["rid"] == reqs[1]["rid"] {
					t.Errorf("asked again with the same token (answered from the first run): %v", reqs)
				}
				var agentErr *wire.AgentError
				if c.fails != (err != nil) || (err != nil && !errors.As(err, &agentErr)) {
					t.Errorf("err = %v, want failure %v", err, c.fails)
				}
				notes := strings.Join(d.Notes(), "\n")
				if (c.asks == 2) != strings.Contains(notes, "no keys were sent") {
					t.Errorf("notes %q", notes)
				}
			})
		}
	}
}
