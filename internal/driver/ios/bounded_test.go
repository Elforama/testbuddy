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

// Issue #130: a screen read tells the agent how long the host waits for it (replyMs), so an
// agent whose app does not answer accessibility answers first, saying what it waited for,
// instead of the host timing out blind. The agent answers 2 s before replyMs: the host waits
// that much longer than readTimeout, so the app keeps the 30 s it had before.
func TestScreenSendsTheReplyLimit(t *testing.T) {
	r := newFakeRunner(t)
	r.on("tree", func(map[string]any) map[string]any { return map[string]any{"seq": 1, "windows": []any{}} })
	d := newIOSDriver(t, r)
	if _, err := d.Screen(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := num(t, r.last("tree"), "replyMs"); got != 32000 {
		t.Errorf("tree replyMs = %v, want 32000 (%v)", got, r.last("tree"))
	}
	// A context that ends sooner shortens the wait, and the agent is told so.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := d.Screen(ctx); err != nil {
		t.Fatal(err)
	}
	if got := num(t, r.last("tree"), "replyMs"); got > 3000 || got < 2000 {
		t.Errorf("tree replyMs = %v under a 3 s context (%v)", got, r.last("tree"))
	}
}

// Issue #130: an agent that gave up on a screen read because the app's main thread was busy
// says so with "transient". In CI mode the host asks once more (a read has no effect on the
// app); elsewhere the agent's error, which says what it waited for, fails the step.
func TestScreenAsksAgainInCIWhenTheAppDidNotAnswer(t *testing.T) {
	busy := map[string]any{"ok": false, "transient": true,
		"error": "reading the screen did not finish: the app did not answer accessibility for 30.0 s (taking the snapshot); nothing was read"}
	ok := map[string]any{"seq": 3, "windows": []any{}}
	cases := []struct {
		name    string
		ci      bool
		replies []map[string]any
		asks    int
		fails   bool
	}{
		{"answers the second time", true, []map[string]any{busy, ok}, 2, false},
		{"asks once more only", true, []map[string]any{busy, busy, ok}, 2, true},
		{"not transient", true, []map[string]any{{"ok": false, "error": "no foreground app"}, ok}, 1, true},
		{"not ci", false, []map[string]any{busy, ok}, 1, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TB_CI", map[bool]string{true: "1", false: "0"}[c.ci])
			r := newFakeRunner(t)
			var mu sync.Mutex
			n := 0
			r.on("tree", func(map[string]any) map[string]any {
				mu.Lock()
				defer mu.Unlock()
				out := map[string]any{}
				for k, v := range c.replies[min(n, len(c.replies)-1)] {
					out[k] = v
				}
				n++
				return out
			})
			d := newIOSDriver(t, r)
			_, err := d.Screen(ctxT(t))
			if got := len(r.requests("tree")); got != c.asks {
				t.Fatalf("asked %d times, want %d", got, c.asks)
			}
			var agentErr *wire.AgentError
			if c.fails != (err != nil) || (err != nil && !errors.As(err, &agentErr)) {
				t.Fatalf("err = %v, want failure %v", err, c.fails)
			}
			if err != nil && c.replies[0]["transient"] == true && !strings.Contains(err.Error(), "the app did not answer accessibility") {
				t.Errorf("the error does not say what was waited for: %v", err)
			}
			if notes := strings.Join(d.Notes(), "\n"); (c.asks == 2) != strings.Contains(notes, "asking once more") {
				t.Errorf("notes %q", notes)
			}
		})
	}
}

// Issue #130: key and hideKeyboard wait on the app too: they carry replyMs, as
// type and erase do (issue #46), and in CI mode an op that gave up before sending anything is
// asked once more.
func TestKeyAndHideKeyboardSendTheReplyLimit(t *testing.T) {
	r := actionRunner(t)
	d := newIOSDriver(t, r)
	if _, err := d.PressKey(context.Background(), "enter", driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	// 5 s timeout + 15 s: the limit call() waits for the reply.
	if got := num(t, r.last("key"), "replyMs"); got != 20000 {
		t.Errorf("key replyMs = %v, want 20000 (%v)", got, r.last("key"))
	}
	if err := d.HideKeyboard(context.Background()); err != nil {
		t.Fatal(err)
	}
	// 5 s timeout + 5 s for the tries + 15 s.
	if got := num(t, r.last("hideKeyboard"), "replyMs"); got != 25000 {
		t.Errorf("hideKeyboard replyMs = %v, want 25000 (%v)", got, r.last("hideKeyboard"))
	}
}

func TestKeyAndHideKeyboardAskAgainOnlyWhenNothingWasSent(t *testing.T) {
	notStarted := map[string]any{"ok": false, "transient": true,
		"error": "pressing key 66 did not start: the app did not answer accessibility for 18.0 s (checking which app is in front); the key was not pressed"}
	ok := map[string]any{"result": "changed", "seq": 5}
	for _, op := range []string{"key", "hideKeyboard"} {
		for _, ciMode := range []bool{true, false} {
			t.Run(op+map[bool]string{true: " ci", false: " not ci"}[ciMode], func(t *testing.T) {
				t.Setenv("TB_CI", map[bool]string{true: "1", false: "0"}[ciMode])
				r := newFakeRunner(t)
				var mu sync.Mutex
				n := 0
				r.on(op, func(map[string]any) map[string]any {
					mu.Lock()
					defer mu.Unlock()
					reply := ok
					if n == 0 {
						reply = notStarted
					}
					n++
					out := map[string]any{}
					for k, v := range reply {
						out[k] = v
					}
					return out
				})
				d := newIOSDriver(t, r)
				var err error
				if op == "key" {
					_, err = d.PressKey(ctxT(t), "enter", driver.ActionOpts{})
				} else {
					err = d.HideKeyboard(ctxT(t))
				}
				want := map[bool]int{true: 2, false: 1}[ciMode]
				if got := len(r.requests(op)); got != want {
					t.Fatalf("asked %d times, want %d", got, want)
				}
				if ciMode != (err == nil) {
					t.Errorf("err = %v", err)
				}
			})
		}
	}
}

// Issue #130: the host's typing budget (40 ms a character) counts characters, not bytes:
// "héllo 👋" is 7 characters in 11 bytes, "こんにちは" 5 in 15.
func TestTypingBudgetCountsCharacters(t *testing.T) {
	r := actionRunner(t)
	d := newIOSDriver(t, r)
	for _, c := range []struct {
		text string
		want float64
	}{{"hello", 20200}, {"héllo 👋", 20280}, {"こんにちは", 20200}} {
		if _, err := d.TypeText(context.Background(), c.text, driver.ActionOpts{}); err != nil {
			t.Fatal(err)
		}
		if got := num(t, r.last("type"), "replyMs"); got != c.want {
			t.Errorf("type %q: replyMs = %v, want %v (5 s + 40 ms a character + 15 s)", c.text, got, c.want)
		}
	}
}
