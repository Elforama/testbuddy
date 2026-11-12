package android

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// AND-005: the app's soft-key callback belongs to its editor connection. Keep
// actual deletion and that callback together, with no hardware replay afterwards.
func TestBackspaceUsesEditorConnectionWithoutDuplicateKey(t *testing.T) {
	for _, mode := range []string{"sent", "unavailable", "rejected", "lost reply"} {
		t.Run(mode, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			var callbacks atomic.Int32
			go func() {
				for {
					c, e := ln.Accept()
					if e != nil {
						return
					}
					func() {
						defer c.Close()
						var target map[string]any
						if json.NewDecoder(c).Decode(&target) != nil {
							return
						}
						if target["pkg"] != "example.app" {
							return
						}
						if mode == "sent" || mode == "lost reply" {
							callbacks.Add(1)
						}
						if mode != "lost reply" {
							fmt.Fprintln(c, mode)
						}
					}()
				}
			}()
			a := newFakeAgent(t)
			a.on("editorTarget", func(map[string]any) map[string]any {
				return map[string]any{"seq": 12, "target": map[string]any{"pkg": "example.app", "window": 7, "node": 42, "id": "field", "cls": "EditText"}}
			})
			a.on("settle", func(map[string]any) map[string]any { return map[string]any{"result": "changed", "seq": 13} })
			a.on("key", func(map[string]any) map[string]any { return map[string]any{"result": "changed"} })
			port := ln.Addr().(*net.TCPAddr).Port
			stub := stubADB(t, []stubRule{{match: "forward tcp:0 localabstract:testbuddy-editor", out: fmt.Sprint(port)}})
			t.Setenv("TB_NO_A11Y_SERVICE", "1")
			d := stub.driver()
			c, h, e := dialHello(a.port(), 2*time.Second)
			if e != nil {
				t.Fatal(e)
			}
			d.adopt(c, a.port(), h)
			defer d.Close()
			t.Setenv("TB_NO_A11Y_SERVICE", "")
			_, e = d.PressKey(context.Background(), "Backspace", driver.ActionOpts{React: true})
			switch mode {
			case "sent":
				if e != nil || callbacks.Load() != 1 || len(a.requests("key")) != 0 {
					t.Fatalf("callback=%d hardware=%d error=%v", callbacks.Load(), len(a.requests("key")), e)
				}
			case "unavailable":
				if e != nil || callbacks.Load() != 0 || len(a.requests("key")) != 1 {
					t.Fatalf("fallback duplicated/lost key: callback=%d hardware=%d error=%v", callbacks.Load(), len(a.requests("key")), e)
				}
				target, ok := a.requests("key")[0]["expectedEditor"].(map[string]any)
				if !ok || target["id"] != "field" {
					t.Fatalf("fallback did not carry the original editor identity: %v", target)
				}
			default:
				if e == nil || len(a.requests("key")) != 0 {
					t.Fatalf("unsafe replay after %s: hardware=%d error=%v", mode, len(a.requests("key")), e)
				}
			}
			if mode == "sent" && !strings.Contains(strings.Join(stub.joined(), "\n"), "localabstract:testbuddy-editor") {
				t.Fatal("editor was not reached through adb")
			}
		})
	}
}

func TestBackspaceKeepsLegacyPathWhenEditorCapabilityIsUnavailable(t *testing.T) {
	for _, tc := range []struct {
		sdk      int
		disabled bool
	}{{26, false}, {29, false}, {32, false}, {33, true}, {34, true}, {34, false}} {
		t.Run(fmt.Sprintf("sdk%d_disabled%v", tc.sdk, tc.disabled), func(t *testing.T) {
			a := newFakeAgent(t)
			a.on("editorTarget", func(map[string]any) map[string]any { return map[string]any{"seq": 1} })
			a.on("key", func(map[string]any) map[string]any { return map[string]any{"result": "changed"} })
			d := newDriver(t, a)
			d.stateMu.Lock()
			d.sdk = tc.sdk
			d.stateMu.Unlock()
			setting := ""
			if tc.disabled {
				setting = "1"
			}
			t.Setenv("TB_NO_A11Y_SERVICE", setting)
			if _, err := d.PressKey(ctxT(t), "Backspace", driver.ActionOpts{}); err != nil {
				t.Fatal(err)
			}
			if len(a.requests("key")) != 1 {
				t.Fatal("legacy key was lost or duplicated")
			}
			if tc.sdk < 33 || tc.disabled {
				if len(a.requests("editorTarget")) != 0 {
					t.Fatal("unavailable API was queried")
				}
			}
		})
	}
}

// editorServer is the device's editor helper behind an adb forward: it reads one target per
// connection, counts it, and answers "sent" (or nothing, once reply is false).
type editorServer struct {
	ln       net.Listener
	received atomic.Int32
	reply    atomic.Bool
}

func newEditorServer(t *testing.T, reply bool) *editorServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &editorServer{ln: ln}
	s.reply.Store(reply)
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				var target map[string]any
				if json.NewDecoder(c).Decode(&target) != nil {
					return
				}
				s.received.Add(1)
				if s.reply.Load() {
					fmt.Fprintln(c, "sent")
				}
			}()
		}
	}()
	return s
}

func (s *editorServer) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

// editorDriver is a driver on SDK 34 whose editor forward the stub adb answers with port.
func editorDriver(t *testing.T, port int) (*Driver, *fakeAgent, *adbStub) {
	t.Helper()
	a := newFakeAgent(t)
	a.on("editorTarget", func(map[string]any) map[string]any {
		return map[string]any{"seq": 12, "target": map[string]any{"pkg": "example.app", "window": 7, "node": 42, "id": "field", "cls": "EditText"}}
	})
	a.on("settle", func(map[string]any) map[string]any { return map[string]any{"result": "changed", "seq": 13} })
	a.on("key", func(map[string]any) map[string]any { return map[string]any{"result": "changed"} })
	stub := stubADB(t, []stubRule{{match: "forward tcp:0 localabstract:testbuddy-editor", out: fmt.Sprint(port)}})
	d := stub.driver()
	adoptAgent(t, d, a)
	t.Cleanup(func() { d.Close() })
	d.stateMu.Lock()
	d.sdk = 34
	d.stateMu.Unlock()
	return d, a, stub
}

// adoptAgent connects d to a again, as a reconnect does, without the NoopService step.
func adoptAgent(t *testing.T, d *Driver, a *fakeAgent) {
	t.Helper()
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	c, h, err := dialHello(a.port(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.adopt(c, a.port(), h); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TB_NO_A11Y_SERVICE", "")
}

func editorForwards(stub *adbStub) int {
	n := 0
	for _, c := range stub.joined() {
		if strings.Contains(c, "forward tcp:0 localabstract:testbuddy-editor") {
			n++
		}
	}
	return n
}

// issue #69: an adb server restart drops every forward, the editor's too. Its cached port was
// never checked again, so every later Backspace failed to dial it, for the rest of the run.
// A dial failure writes nothing, so forwarding again and dialing once more is safe.
func TestBackspaceForwardsAgainWhenTheEditorForwardIsLost(t *testing.T) {
	first := newEditorServer(t, true)
	d, a, stub := editorDriver(t, first.port())
	if _, err := d.PressKey(ctxT(t), "Backspace", driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	if first.received.Load() != 1 {
		t.Fatalf("first Backspace reached the editor %d times, want 1", first.received.Load())
	}
	// The forward is lost: nothing listens on its port any more. A new one gets a new port.
	first.ln.Close()
	second := newEditorServer(t, true)
	stub.rules([]stubRule{{match: "forward tcp:0 localabstract:testbuddy-editor", out: fmt.Sprint(second.port())}})
	if _, err := d.PressKey(ctxT(t), "Backspace", driver.ActionOpts{}); err != nil {
		t.Fatalf("Backspace after the editor forward was lost: %v", err)
	}
	if second.received.Load() != 1 || len(a.requests("key")) != 0 {
		t.Fatalf("second Backspace: editor=%d hardware=%d, want exactly one editor key", second.received.Load(), len(a.requests("key")))
	}
	if n := editorForwards(stub); n != 2 {
		t.Fatalf("editor forwarded %d times, want 2 (once, then again after the loss)", n)
	}
	// The next one uses the new forward as it is.
	if _, err := d.PressKey(ctxT(t), "Backspace", driver.ActionOpts{}); err != nil || second.received.Load() != 2 || editorForwards(stub) != 2 {
		t.Fatalf("third Backspace: editor=%d forwards=%d error=%v", second.received.Load(), editorForwards(stub), err)
	}
}

// issue #69: the retry is for a failed dial only, and only once. When the second dial fails
// too, Backspace fails without looping, and the following one forwards afresh.
func TestBackspaceRetriesALostEditorForwardOnlyOnce(t *testing.T) {
	gone := newEditorServer(t, true)
	port := gone.port()
	gone.ln.Close()
	d, _, stub := editorDriver(t, port)
	if _, err := d.PressKey(ctxT(t), "Backspace", driver.ActionOpts{}); err == nil {
		t.Fatal("Backspace succeeded with no editor listening")
	}
	if n := editorForwards(stub); n != 2 {
		t.Fatalf("editor forwarded %d times, want 2 (first dial, one retry)", n)
	}
	live := newEditorServer(t, true)
	stub.rules([]stubRule{{match: "forward tcp:0 localabstract:testbuddy-editor", out: fmt.Sprint(live.port())}})
	if _, err := d.PressKey(ctxT(t), "Backspace", driver.ActionOpts{}); err != nil || live.received.Load() != 1 {
		t.Fatalf("Backspace after a failed retry: editor=%d error=%v", live.received.Load(), err)
	}
}

// issue #69: once the target is written, the helper may have deleted already. A lost reply is
// not a dial failure: no second write, no new forward, no hardware key.
func TestBackspaceDoesNotReplayAfterWritingToTheEditor(t *testing.T) {
	mute := newEditorServer(t, false)
	d, a, stub := editorDriver(t, mute.port())
	if _, err := d.PressKey(ctxT(t), "Backspace", driver.ActionOpts{}); err == nil {
		t.Fatal("Backspace succeeded with no reply from the editor")
	}
	if mute.received.Load() != 1 || len(a.requests("key")) != 0 || editorForwards(stub) != 1 {
		t.Fatalf("replayed after writing: editor=%d hardware=%d forwards=%d", mute.received.Load(), len(a.requests("key")), editorForwards(stub))
	}
}

// issue #69: a new agent connection is a sign that adb may have lost its forwards (an adb
// server restart drops them all), and the old port may now be something else that accepts the
// connection but is not the editor. When adb no longer lists the editor's forward on that
// port, the driver forwards again instead of writing the key into whatever answers there.
func TestAdoptingAConnectionForgetsAnEditorForwardAdbNoLongerHas(t *testing.T) {
	old := newEditorServer(t, true)
	d, a, stub := editorDriver(t, old.port())
	if _, err := d.PressKey(ctxT(t), "Backspace", driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	old.reply.Store(false) // the old port now answers, but never as the editor
	fresh := newEditorServer(t, true)
	stub.rules([]stubRule{
		{match: "forward --list", out: fmt.Sprintf("%s tcp:%d localabstract:testbuddy-agent\n", testSerial, a.port())},
		{match: "forward tcp:0 localabstract:testbuddy-editor", out: fmt.Sprint(fresh.port())},
	})
	adoptAgent(t, d, a)
	if _, err := d.PressKey(ctxT(t), "Backspace", driver.ActionOpts{}); err != nil {
		t.Fatalf("Backspace after a reconnect: %v", err)
	}
	if fresh.received.Load() != 1 || old.received.Load() != 1 {
		t.Fatalf("after a new connection: fresh forward got %d keys, the old port %d; want 1 and 1 (the first Backspace only)", fresh.received.Load(), old.received.Load())
	}
}

// issue #69: after a reconnect the port is kept when adb still lists it for this device and
// the editor, wherever it is in the list: a stale editor forward listed first (left by an
// earlier run) does not make each reconnect add a forward of its own.
func TestAdoptingAConnectionKeepsAnEditorForwardAdbStillHas(t *testing.T) {
	ed := newEditorServer(t, true)
	d, a, stub := editorDriver(t, ed.port())
	if _, err := d.PressKey(ctxT(t), "Backspace", driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	stub.rules([]stubRule{
		{match: "forward --list", out: fmt.Sprintf("%s tcp:1 localabstract:testbuddy-editor\nemulator-9999 tcp:2 localabstract:testbuddy-editor\n%s tcp:%d localabstract:testbuddy-editor\n", testSerial, testSerial, ed.port())},
		{match: "forward tcp:0 localabstract:testbuddy-editor", out: fmt.Sprint(ed.port())},
	})
	for i := range 3 {
		adoptAgent(t, d, a)
		if _, err := d.PressKey(ctxT(t), "Backspace", driver.ActionOpts{}); err != nil {
			t.Fatalf("Backspace after reconnect %d: %v", i+1, err)
		}
	}
	if n := editorForwards(stub); n != 1 || ed.received.Load() != 4 {
		t.Fatalf("after 3 reconnects with the forward still there: forwards=%d editor=%d, want 1 and 4", n, ed.received.Load())
	}
}

// issue #69: when adb cannot list its forwards, the port's state is unknown: keep it. The
// dial retry still replaces it if it is really gone.
func TestAdoptingAConnectionKeepsTheEditorForwardWhenAdbCannotList(t *testing.T) {
	ed := newEditorServer(t, true)
	d, a, stub := editorDriver(t, ed.port())
	if _, err := d.PressKey(ctxT(t), "Backspace", driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	stub.rules([]stubRule{
		{match: "forward --list", out: "error: protocol fault", code: 1},
		{match: "forward tcp:0 localabstract:testbuddy-editor", out: fmt.Sprint(ed.port())},
	})
	adoptAgent(t, d, a)
	if _, err := d.PressKey(ctxT(t), "Backspace", driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	if n := editorForwards(stub); n != 1 || ed.received.Load() != 2 {
		t.Fatalf("with adb unable to list: forwards=%d editor=%d, want the port kept", n, ed.received.Load())
	}
}

// issue #69: only a refused dial means the forward is gone. A dial cut short by the step's
// own context says nothing about the forward: keep its port, and do not forward again.
func TestACancelledEditorDialKeepsTheForward(t *testing.T) {
	ed := newEditorServer(t, true)
	d, _, stub := editorDriver(t, ed.port())
	if _, err := d.PressKey(ctxT(t), "Backspace", driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.editorMu.Lock()
	conn, err := d.dialEditor(ctx)
	port := d.editorPort
	d.editorMu.Unlock()
	if err == nil {
		conn.Close()
		t.Fatal("dial succeeded with a cancelled context")
	}
	if port != ed.port() || editorForwards(stub) != 1 {
		t.Fatalf("after a cancelled dial: port=%d (want %d kept) forwards=%d", port, ed.port(), editorForwards(stub))
	}
}

// issue #69: `adb forward --remove tcp:N` matches the local port only, whatever -s says. After
// an adb restart, N may be another device's forward: remove it only when adb lists it as this
// device's editor forward.
func TestClosingRemovesOnlyTheEditorForwardAdbListsForThisDevice(t *testing.T) {
	for _, tc := range []struct {
		name, list string
		code       int
		remove     bool
	}{
		{"ours", "%[1]s tcp:%[2]d localabstract:testbuddy-editor\n", 0, true},
		{"another device's", "emulator-9999 tcp:%[2]d localabstract:testbuddy-agent\n", 0, false},
		{"gone", "", 0, false},
		{"adb cannot list", "error", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ed := newEditorServer(t, true)
			d, _, stub := editorDriver(t, ed.port())
			if _, err := d.PressKey(ctxT(t), "Backspace", driver.ActionOpts{}); err != nil {
				t.Fatal(err)
			}
			list := tc.list
			if strings.Contains(list, "%") {
				list = fmt.Sprintf(list, testSerial, ed.port())
			}
			stub.rules([]stubRule{{match: "forward --list", out: list, code: tc.code}})
			d.closeEditorForward()
			removed := containsAny(stub.joined(), fmt.Sprintf("forward --remove tcp:%d", ed.port()))
			if removed != tc.remove {
				t.Fatalf("removed=%v, want %v", removed, tc.remove)
			}
			d.editorMu.Lock()
			port := d.editorPort
			d.editorMu.Unlock()
			if port != 0 {
				t.Fatalf("port %d still cached after close", port)
			}
		})
	}
}
