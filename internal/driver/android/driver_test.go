package android

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/wire"
)

// dialHello reads the size and SDK from info and the version from the agent's greeting.
func TestDialHelloAndAdopt(t *testing.T) {
	a := newFakeAgent(t)
	a.greet(func(int) map[string]any {
		return map[string]any{"ev": "hello", "platform": "android", "version": "9.9.9", "w": 1, "h": 2, "seq": 0}
	})
	d := newDriver(t, a)
	if d.port != a.port() || d.w != 1080 || d.h != 2400 || d.sdk != 34 {
		t.Errorf("adopted port %d size %dx%d sdk %d", d.port, d.w, d.h, d.sdk)
	}
	if got := a.ops(); !reflect.DeepEqual(got, []string{"info", "ping"}) {
		t.Errorf("dialHello sent %v, want info then ping", got)
	}
	if d.Conn() == nil || d.Conn() != d.conn() {
		t.Error("Conn does not expose the current connection")
	}
	c, h, err := dialHello(a.port(), time.Second)
	if err != nil || h.Version != "9.9.9" || h.W != 1080 {
		t.Fatalf("dialHello = %+v, %v; want the greeting's version and info's size", h, err)
	}
	c.Close()
}

// dialHello fails (and closes the connection) when info or ping fails.
func TestDialHelloFailures(t *testing.T) {
	for _, op := range []string{"info", "ping"} {
		t.Run(op, func(t *testing.T) {
			a := newFakeAgent(t)
			a.on(op, func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "not ready"} })
			_, _, err := dialHello(a.port(), time.Second)
			var ae *wire.AgentError
			if !errors.As(err, &ae) || ae.Op != op {
				t.Errorf("err = %v, want the agent's %s error", err, op)
			}
		})
	}
	if _, _, err := dialHello(1, 100*time.Millisecond); err == nil {
		t.Error("dialing a closed port succeeded")
	}
}

// adopt enables NoopService (Compose sends accessibility events only with a service on) unless TB_NO_A11Y_SERVICE is set.
func TestAdoptEnablesNoopService(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "")
	a := newFakeAgent(t)
	newDriver(t, a)
	cmds := a.shCmds()
	if len(cmds) != 1 {
		t.Fatalf("sh calls %v, want one", cmds)
	}
	for _, want := range []string{
		"settings get secure enabled_accessibility_services",
		"*" + noopService + "*) ;;",
		`null|"") settings put secure enabled_accessibility_services ` + noopService,
		`settings put secure enabled_accessibility_services "$cur:` + noopService + `"`,
	} {
		if !strings.Contains(cmds[0], want) {
			t.Errorf("service command lacks %q:\n%s", want, cmds[0])
		}
	}
}

// TB_NO_A11Y_SERVICE keeps adopt from touching the accessibility settings.
func TestAdoptWithoutNoopService(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	a := newFakeAgent(t)
	newDriver(t, a)
	if cmds := a.shCmds(); len(cmds) != 0 {
		t.Errorf("sh calls %v, want none", cmds)
	}
}

// A restarted agent (its counter back at 0) continues the driver's count: seqBase moves past the last seq.
func TestAdoptKeepsSeqIncreasing(t *testing.T) {
	a := newFakeAgent(t)
	d := newDriver(t, a)
	events, cancel := d.Events()
	defer cancel()
	a.push(map[string]any{"ev": "changed", "seq": 50})
	if e := waitEvent(t, events, "changed"); e.Seq != 50 {
		t.Fatalf("event seq %d, want 50", e.Seq)
	}
	if d.Seq() != 50 {
		t.Fatalf("Seq = %d", d.Seq())
	}
	b := newFakeAgent(t)
	c, h, err := dialHello(b.port(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	old := d.conn()
	d.adopt(c, b.port(), h)
	old.Close()
	if d.seqBase != 51 {
		t.Errorf("seqBase = %d, want 51", d.seqBase)
	}
	if d.Seq() != 51 {
		t.Errorf("Seq after restart = %d, want 51", d.Seq())
	}
	b.push(map[string]any{"ev": "changed", "seq": 3})
	if e := waitEvent(t, events, "changed"); e.Seq != 54 {
		t.Errorf("event seq after restart = %d, want 54", e.Seq)
	}
	b.push(map[string]any{"ev": "settled", "seq": 3})
	if e := waitEvent(t, events, "settled"); e.Seq != 54 {
		t.Errorf("settled seq = %d, want 54", e.Seq)
	}
	if d.port != b.port() {
		t.Errorf("port %d, want the new agent's", d.port)
	}
}

// Pushed events reach subscribers with their package; a closed driver's pump stops quietly.
func TestEventsFromAgent(t *testing.T) {
	a := newFakeAgent(t)
	d := newDriver(t, a)
	events, cancel := d.Events()
	defer cancel()
	a.push(map[string]any{"ev": "changed", "seq": 7, "pkg": "com.example.app"})
	e := waitEvent(t, events, "changed")
	if e.Seq != 7 || e.Pkg != "com.example.app" {
		t.Errorf("event %+v", e)
	}
	a.push(map[string]any{"ev": "settled", "seq": 7})
	if e := waitEvent(t, events, "settled"); e.Seq != 7 || e.Pkg != "" {
		t.Errorf("event %+v", e)
	}
	d.Close()
	a.dropConns()
	select {
	case e := <-events:
		t.Errorf("closed driver sent %+v", e)
	case <-time.After(100 * time.Millisecond):
	}
}

// reconnect does nothing when the dropped connection was already replaced.
func TestReconnectAlreadyReplaced(t *testing.T) {
	a := newFakeAgent(t)
	d := newDriver(t, a)
	other, _, err := dialHello(a.port(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := d.reconnect(ctxT(t), other); err != nil {
		t.Errorf("reconnect = %v", err)
	}
	if d.conn() == other {
		t.Error("reconnect replaced the current connection")
	}
}

// Screenshot defaults to PNG and returns the binary payload.
func TestScreenshot(t *testing.T) {
	a := newFakeAgent(t)
	png := []byte("\x89PNG\r\n\x1a\nfake\nimage")
	a.on("screenshot", func(map[string]any) map[string]any {
		return map[string]any{"w": 540, "h": 1200, "format": "png", "_bin": png}
	})
	d := newDriver(t, a)
	b, err := d.Screenshot(ctxT(t), 0.5, "")
	if err != nil || string(b) != string(png) {
		t.Fatalf("Screenshot = %q, %v", b, err)
	}
	req := a.last("screenshot")
	if req["format"] != "png" || num(t, req, "scale") != 0.5 {
		t.Errorf("request %v", req)
	}
	if _, err := d.Screenshot(ctxT(t), 1, "jpeg"); err != nil || a.last("screenshot")["format"] != "jpeg" {
		t.Errorf("jpeg: %v %v", err, a.last("screenshot"))
	}
	a.on("screenshot", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "secure window"} })
	if _, err := d.Screenshot(ctxT(t), 1, ""); err == nil {
		t.Error("error not reported")
	}
}

// Sh returns output and exit code; shOK turns a non-zero exit into an error with the output.
func TestShAndShOK(t *testing.T) {
	a := newFakeAgent(t)
	d := newDriver(t, a)
	a.on("sh", func(req map[string]any) map[string]any {
		if req["cmd"] == "false" {
			return map[string]any{"code": 1, "out": "  failed here \n"}
		}
		return map[string]any{"code": 0, "out": "ok\n"}
	})
	out, code, err := d.Sh(ctxT(t), "echo ok")
	if out != "ok\n" || code != 0 || err != nil {
		t.Errorf("Sh = %q %d %v", out, code, err)
	}
	if num(t, a.last("sh"), "timeout") != 60000 {
		t.Errorf("sh timeout %v", a.last("sh"))
	}
	if _, err := d.shOK(ctxT(t), "false"); err == nil || err.Error() != "false: exit 1: failed here" {
		t.Errorf("shOK = %v", err)
	}
	a.on("sh", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "denied"} })
	if _, code, err := d.Sh(ctxT(t), "x"); code != -1 || err == nil {
		t.Errorf("failed Sh = %d %v", code, err)
	}
}
