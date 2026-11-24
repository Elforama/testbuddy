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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := d.Screen(ctx); err != nil {
		t.Fatal(err)
	}
	if got := num(t, r.last("tree"), "replyMs"); got > 3000 || got < 2000 {
		t.Errorf("tree replyMs = %v under a 3 s context (%v)", got, r.last("tree"))
	}
}

func TestScreenSaysCIModeToTheAgent(t *testing.T) {
	for _, mode := range []string{"1", "0"} {
		t.Run("TB_CI="+mode, func(t *testing.T) {
			t.Setenv("TB_CI", mode)
			r := newFakeRunner(t)
			r.on("tree", func(map[string]any) map[string]any { return map[string]any{"seq": 1, "windows": []any{}} })
			d := newIOSDriver(t, r)
			if _, err := d.Screen(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, _ := r.last("tree")["ci"].(bool)
			if want := mode == "1"; got != want {
				t.Errorf(`tree "ci" = %v, want %v (%v)`, got, want, r.last("tree"))
			}
		})
	}
}

func TestSwipeSaysCIModeToTheAgent(t *testing.T) {
	for _, mode := range []string{"1", "0"} {
		t.Run("TB_CI="+mode, func(t *testing.T) {
			t.Setenv("TB_CI", mode)
			r := newFakeRunner(t)
			r.on("swipe", func(map[string]any) map[string]any { return map[string]any{"result": "changed", "seq": 1} })
			d := newIOSDriver(t, r)
			for _, drag := range []bool{false, true} {
				if _, err := d.Swipe(context.Background(), 10, 10, 300, 10, 10*time.Millisecond, driver.ActionOpts{Drag: drag}); err != nil {
					t.Fatal(err)
				}
				got, _ := r.last("swipe")["ci"].(bool)
				if want := mode == "1"; got != want {
					t.Errorf(`swipe (drag %v) "ci" = %v, want %v (%v)`, drag, got, want, r.last("swipe"))
				}
			}
		})
	}
}

func TestHideKeyboardSaysCIModeToTheAgent(t *testing.T) {
	for _, mode := range []string{"1", "0"} {
		t.Run("TB_CI="+mode, func(t *testing.T) {
			t.Setenv("TB_CI", mode)
			r := newFakeRunner(t)
			r.on("hideKeyboard", func(map[string]any) map[string]any { return map[string]any{"hidden": true, "seq": 1} })
			d := newIOSDriver(t, r)
			if err := d.HideKeyboard(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, _ := r.last("hideKeyboard")["ci"].(bool)
			if want := mode == "1"; got != want {
				t.Errorf(`hideKeyboard "ci" = %v, want %v (%v)`, got, want, r.last("hideKeyboard"))
			}
		})
	}
}

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

func TestKeyAndHideKeyboardSendTheReplyLimit(t *testing.T) {
	r := actionRunner(t)
	d := newIOSDriver(t, r)
	if _, err := d.PressKey(context.Background(), "enter", driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := num(t, r.last("key"), "replyMs"); got != 20000 {
		t.Errorf("key replyMs = %v, want 20000 (%v)", got, r.last("key"))
	}
	if err := d.HideKeyboard(context.Background()); err != nil {
		t.Fatal(err)
	}
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
