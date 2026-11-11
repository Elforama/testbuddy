package session

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/devlock"
)

// Register serves every session command over the daemon socket; ping answers the build id
// and stop ends the server.
func TestRegisterOverSocket(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	srv := daemon.NewServer(time.Hour)
	New().Register(srv)
	served := make(chan error, 1)
	go func() { served <- srv.Serve() }()

	var c net.Conn
	deadline := time.Now().Add(5 * time.Second)
	for {
		var err error
		if c, err = net.Dial("unix", daemon.SocketPath()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon did not listen: %v", err)
		}
		time.Sleep(10 * time.Millisecond) // server start-up only
	}
	defer c.Close()
	r := bufio.NewReader(c)
	send := func(req daemon.Request) daemon.Response {
		t.Helper()
		b, _ := json.Marshal(req)
		if _, err := c.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
		line, err := r.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		var resp daemon.Response
		json.Unmarshal(line, &resp)
		return resp
	}

	if resp := send(daemon.Request{Cmd: "ping"}); !resp.OK || resp.Text != daemon.BuildID() {
		t.Errorf("ping = %+v", resp)
	}
	// every command is registered: with no device open they answer, rather than "unknown command"
	for _, cmd := range []string{"devices", "open", "close", "look", "locate", "do", "watch", "shot", "log", "save", "clear"} {
		resp := send(daemon.Request{Cmd: cmd, Args: json.RawMessage(`{"device":"nothing-here"}`)})
		if strings.Contains(resp.Text, "unknown command") {
			t.Errorf("%s is not registered: %+v", cmd, resp)
		}
	}
	if resp := send(daemon.Request{Cmd: "nope"}); resp.Exit != 2 || !strings.Contains(resp.Text, "unknown command") {
		t.Errorf("unknown = %+v", resp)
	}
	if resp := send(daemon.Request{Cmd: "stop"}); !resp.OK || resp.Text != "tb daemon stopped" {
		t.Errorf("stop = %+v", resp)
	}
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not stop the server")
	}
}

func TestLocateReportsAmbiguityWithoutTapping(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(button("Continue", 100), button("Continue", 200))
	d := addDev(s, f, "")
	resp, _ := call(t, s.locate, map[string]any{"selector": "Continue"}, "")
	var report struct {
		Count      int
		Candidates []locatorCandidate
	}
	if err := json.Unmarshal(resp.JSON, &report); err != nil {
		t.Fatalf("response %q: %v", resp.Text, err)
	}
	if !resp.OK || report.Count != 2 || len(report.Candidates) != 2 ||
		!report.Candidates[0].Chosen || report.Candidates[1].Chosen || report.Candidates[0].Ref == 0 {
		t.Fatalf("unexpected report: %s", resp.JSON)
	}
	if len(f.taps) != 0 || len(d.steps) != 0 {
		t.Fatal("inspection performed or recorded an action")
	}
}

// Every handler rejects arguments that are not its JSON object with exit 2.
func TestHandlersRejectBadArgs(t *testing.T) {
	s := New()
	for name, h := range map[string]handler{"open": s.open, "close": s.close, "look": s.look, "shot": s.shot, "do": s.do, "save": s.save, "watch": s.watch} {
		resp, _ := call(t, h, `{"device": 3`, "")
		if resp.Exit != 2 || resp.OK {
			t.Errorf("%s: %+v", name, resp)
		}
	}
}

// A command finds its device by alias, serial or name; with one device open it needs none.
func TestResolve(t *testing.T) {
	s := New()
	a := addDev(s, newFake("emulator-5554", "Pixel 7", device.Android), "alice")
	ctx := context.Background()
	for _, want := range []string{"alice", "emulator-5554", "Pixel 7", "pixel 7", ""} {
		d, err := s.resolve(ctx, devArgs{Device: want})
		if err != nil || d != a {
			t.Errorf("resolve(%q) = %v, %v", want, d, err)
		}
	}
	b := addDev(s, newFake("UDID-1", "iPhone 17", device.IOS), "")
	if d, err := s.resolve(ctx, devArgs{Device: "UDID-1"}); err != nil || d != b {
		t.Errorf("resolve(UDID-1) = %v, %v", d, err)
	}
	_, err := s.resolve(ctx, devArgs{})
	if err == nil || err.Error() != "several devices are open (alice (Pixel 7), iPhone 17 UDID-1): say which with -d" {
		t.Errorf("ambiguous: %v", err)
	}
}

// Unknown devices are looked up among connected ones; with none connected the error says so.
func TestResolveNotOpen(t *testing.T) {
	s := New()
	ctx := context.Background()
	if _, err := s.resolve(ctx, devArgs{}); err == nil || !strings.Contains(err.Error(), "no device is connected") {
		t.Errorf("err = %v", err)
	}
	if _, err := s.resolve(ctx, devArgs{Device: "bob"}); err == nil || !strings.Contains(err.Error(), `no connected device matches "bob"`) {
		t.Errorf("err = %v", err)
	}
	addDev(s, newFake("emulator-5554", "Pixel 7", device.Android), "")
	// a platform with one device open still looks for a matching connected device
	if _, err := s.resolve(ctx, devArgs{Platform: "ios"}); err == nil || !strings.Contains(err.Error(), "no ios device is connected") {
		t.Errorf("err = %v", err)
	}
}

// A name outlives its connection: after the device drops, the name still means that
// device's serial (a reconnect looks for it).
func TestResolveRemembersNames(t *testing.T) {
	s := New()
	s.names["alice"] = "emulator-5554" // her connection dropped
	_, err := s.resolve(context.Background(), devArgs{Device: "alice"})
	if err == nil || !strings.Contains(err.Error(), `no connected device matches "emulator-5554"`) {
		t.Errorf("err = %v, want a lookup of alice's serial", err)
	}
}

// When the named device is connected again (unnamed), the remembered name re-attaches to it.
func TestResolveReattachesName(t *testing.T) {
	adbLists(t, "emulator-5554 Pixel_7")
	s := New()
	d := addDev(s, newFake("emulator-5554", "Pixel 7", device.Android), "")
	s.names["alice"] = "emulator-5554"
	got, err := s.resolve(context.Background(), devArgs{Device: "alice"})
	if err != nil || got != d {
		t.Fatalf("resolve = %v, %v", got, err)
	}
	if d.Name != "alice" {
		t.Errorf("name = %q, want alice", d.Name)
	}
}

// label shows the alias with the device name, or the name and serial.
func TestDevLabel(t *testing.T) {
	d := &Dev{Info: device.Info{Name: "Pixel 7", Serial: "emulator-5554"}}
	if got := d.label(); got != "Pixel 7 emulator-5554" {
		t.Errorf("label = %q", got)
	}
	d.Name = "bob"
	if got := d.label(); got != "bob (Pixel 7)" {
		t.Errorf("label = %q", got)
	}
}

// devices lists every connected device, marking open ones and their session names.
func TestDevices(t *testing.T) {
	adbLists(t, "emulator-5554 Pixel_7", "emulator-5556 Pixel_8", "R58M Galaxy_S22")
	s := New()
	addDev(s, newFake("emulator-5554", "Pixel 7", device.Android), "alice")
	addDev(s, newFake("R58M", "Galaxy S22", device.Android), "")
	resp, _ := call(t, s.devices, nil, "")
	if !resp.OK {
		t.Fatalf("resp %+v", resp)
	}
	want := `android emulator-5554 "Pixel 7" (emulator)  session: alice
android emulator-5556 "Pixel 8" (emulator)
android R58M "Galaxy S22" (device)  connected
`
	if resp.Text != want {
		t.Errorf("text =\n%s\nwant\n%s", resp.Text, want)
	}
	var out []struct {
		Serial  string `json:"serial"`
		Session string `json:"session"`
		Open    bool   `json:"open"`
	}
	if err := json.Unmarshal(resp.JSON, &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 || !out[0].Open || out[0].Session != "alice" || out[1].Open || !out[2].Open || out[2].Session != "" {
		t.Errorf("json %+v", out)
	}
}

// With nothing connected, devices says how to get a device.
func TestDevicesNone(t *testing.T) {
	resp, _ := call(t, New().devices, nil, "")
	if !resp.OK || resp.Text != "no devices: start an Android emulator or boot an iOS simulator\n" || string(resp.JSON) != "null" {
		t.Errorf("resp %+v json %s", resp, resp.JSON)
	}
}

// open names an already-connected device and sets its default app.
func TestOpenNamesDevice(t *testing.T) {
	adbLists(t, "emulator-5554 Pixel_7")
	s := New()
	d := addDev(s, newFake("emulator-5554", "Pixel 7", device.Android), "")
	resp, _ := call(t, s.open, map[string]any{"device": "emulator-5554", "name": "alice", "app": "com.chat"}, "")
	if !resp.OK || !strings.HasPrefix(resp.Text, `connected alice: android emulator-5554 "Pixel 7" (emulator) in `) {
		t.Fatalf("resp %+v", resp)
	}
	if d.Name != "alice" || d.AppID != "com.chat" || s.names["alice"] != "emulator-5554" {
		t.Errorf("dev name=%q app=%q names=%v", d.Name, d.AppID, s.names)
	}
	// opening again without a name keeps it
	resp, _ = call(t, s.open, map[string]any{"device": "alice"}, "")
	if !resp.OK || !strings.HasPrefix(resp.Text, "connected alice: ") || d.AppID != "com.chat" {
		t.Errorf("reopen %+v app %q", resp, d.AppID)
	}
}

// open accepts an alias of an open device even when it is not in the device list.
func TestOpenByAlias(t *testing.T) {
	s := New()
	addDev(s, newFake("emulator-5554", "Pixel 7", device.Android), "alice")
	resp, _ := call(t, s.open, map[string]any{"device": "alice"}, "")
	if !resp.OK || !strings.HasPrefix(resp.Text, "connected alice: ") {
		t.Errorf("resp %+v", resp)
	}
}

// A name already used by another device is refused.
func TestOpenNameTaken(t *testing.T) {
	adbLists(t, "emulator-5554 Pixel_7", "emulator-5556 Pixel_8")
	s := New()
	addDev(s, newFake("emulator-5554", "Pixel 7", device.Android), "alice")
	addDev(s, newFake("emulator-5556", "Pixel 8", device.Android), "")
	resp, _ := call(t, s.open, map[string]any{"device": "emulator-5556", "name": "alice"}, "")
	if resp.Exit != 2 || resp.Text != `the name "alice" is already used by Pixel 7` {
		t.Errorf("resp %+v", resp)
	}
}

// open of a device that is not connected fails with exit 2.
func TestOpenUnknown(t *testing.T) {
	resp, _ := call(t, New().open, map[string]any{"device": "Pixel 99"}, "")
	if resp.Exit != 2 || !strings.Contains(resp.Text, `no connected device matches "Pixel 99"`) {
		t.Errorf("resp %+v", resp)
	}
}

// close drops one device, its driver and its name.
func TestCloseOne(t *testing.T) {
	s := New()
	fa, fb := newFake("emulator-5554", "Pixel 7", device.Android), newFake("UDID-1", "iPhone 17", device.IOS)
	addDev(s, fa, "alice")
	addDev(s, fb, "bob")
	resp, _ := call(t, s.close, map[string]any{"device": "alice"}, "")
	if !resp.OK || resp.Text != "closed alice (Pixel 7)\n" {
		t.Errorf("resp %+v", resp)
	}
	if fa.closed != 1 || fb.closed != 0 {
		t.Errorf("closed a=%d b=%d", fa.closed, fb.closed)
	}
	if _, ok := s.devs["emulator-5554"]; ok {
		t.Error("device still open")
	}
	if _, ok := s.names["alice"]; ok {
		t.Error("name kept after close")
	}
	if s.names["bob"] != "UDID-1" {
		t.Error("other name lost")
	}
}

// close all drops every device and every name.
func TestCloseAll(t *testing.T) {
	s := New()
	fa, fb := newFake("a", "A", device.Android), newFake("b", "B", device.IOS)
	addDev(s, fa, "alice")
	addDev(s, fb, "")
	s.names["gone"] = "c"
	resp, _ := call(t, s.close, map[string]any{"device": "all"}, "")
	if !resp.OK || resp.Text != "closed all devices\n" {
		t.Errorf("resp %+v", resp)
	}
	if fa.closed != 1 || fb.closed != 1 || len(s.devs) != 0 || len(s.names) != 0 {
		t.Errorf("closed %d %d devs %v names %v", fa.closed, fb.closed, s.devs, s.names)
	}
}

// close of an unknown device fails with exit 2.
func TestCloseUnknown(t *testing.T) {
	resp, _ := call(t, New().close, map[string]any{"device": "ghost"}, "")
	if resp.Exit != 2 {
		t.Errorf("resp %+v", resp)
	}
}

// A session holds its device: a device another process holds is refused before tb connects,
// naming the holder; closing a session (or all) frees its device for other processes.
func TestSessionLocks(t *testing.T) {
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	s := New()
	other, err := devlock.TryAcquire("emulator-5554", "tb test flows/")
	if err != nil {
		t.Fatal(err)
	}
	info := device.Info{Platform: device.Android, Serial: "emulator-5554", Name: "Pixel"}
	_, err = s.connect(context.Background(), info, "alice")
	if err == nil || !strings.Contains(err.Error(), "Pixel (emulator-5554) is in use by tb test flows/ (pid ") {
		t.Fatalf("connect to a held device: %v", err)
	}
	other.Release()

	// A session's lock is freed by close, and by close all.
	for _, all := range []bool{false, true} {
		f := newFake("emulator-5554", "Pixel", device.Android)
		d := addDev(s, f, "alice")
		d.lock, err = devlock.TryAcquire("emulator-5554", `tb session "alice"`)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := devlock.TryAcquire("emulator-5554", "tb test"); err == nil {
			t.Fatal("a session's device was not held")
		}
		dev := "alice"
		if all {
			dev = "all"
		}
		if r, _ := call(t, s.close, map[string]string{"device": dev}, ""); !r.OK {
			t.Fatalf("close %s: %+v", dev, r)
		}
		l, err := devlock.TryAcquire("emulator-5554", "tb test")
		if err != nil {
			t.Errorf("after close %s: %v", dev, err)
		}
		l.Release()
		if f.released != 1 {
			t.Errorf("close %s: the agent was released %d times, want 1", dev, f.released)
		}
	}
}

// One session per device in a daemon: opening a device another named session has, under a new
// name, is refused (it would take that session over); the same name, or no name, reuses it.
func TestSessionOneNamePerDevice(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel", device.Android)
	addDev(s, f, "alice")
	info := device.Info{Platform: device.Android, Serial: "emulator-5554", Name: "Pixel"}
	if _, err := s.connect(context.Background(), info, "bob"); err == nil || !strings.Contains(err.Error(), `is already open as session "alice"`) {
		t.Errorf("bob over alice: %v", err)
	}
	for _, name := range []string{"alice", ""} {
		if d, err := s.connect(context.Background(), info, name); err != nil || d.Name != "alice" {
			t.Errorf("name %q: %v, %v", name, d, err)
		}
	}
}

// issue #57: Ctrl-C on a `tb do` that waits for an element ends that wait. The daemon cancels
// the command when its client disconnects, so the device is free for the next `tb do` at
// once instead of after the full wait, and the session works as before.
func TestDoCancelledWhenClientDisconnects(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(loginScreen()...)
	d := addDev(s, f, "")
	srv := daemon.NewServer(time.Hour)
	s.Register(srv)
	served := make(chan error, 1)
	go func() { served <- srv.Serve() }()
	t.Cleanup(func() {
		srv.Stop()
		<-served
	})
	dial := func() net.Conn {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			c, err := net.Dial("unix", daemon.SocketPath())
			if err == nil {
				return c
			}
			if time.Now().After(deadline) {
				t.Fatalf("daemon did not listen: %v", err)
			}
			time.Sleep(10 * time.Millisecond) // server start-up only
		}
	}
	request := func(c net.Conn, cmd string, args any) {
		t.Helper()
		raw, _ := json.Marshal(args)
		b, _ := json.Marshal(daemon.Request{Cmd: cmd, Args: raw})
		if _, err := c.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
	}

	// The first client waits 20 s for an element that never shows, then goes away.
	first := dial()
	request(first, "do", map[string]any{"commands": "extendedWaitUntil:\n  visible: Never there\n  timeout: 20000\n"})
	deadline := time.Now().Add(5 * time.Second)
	for d.mu.TryLock() { // the wait has started once it holds the device
		d.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("the first do did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	first.Close()

	// The next client's command runs at once.
	second := dial()
	defer second.Close()
	request(second, "do", map[string]any{"commands": "inputText: hello"})
	got := make(chan daemon.Response, 1)
	go func() {
		line, err := bufio.NewReader(second).ReadBytes('\n')
		var resp daemon.Response
		if err == nil {
			json.Unmarshal(line, &resp)
		}
		got <- resp
	}()
	select {
	case resp := <-got:
		if !resp.OK || !strings.Contains(resp.Text, "ok Input text hello") {
			t.Errorf("second do = %+v", resp)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("issue #57: the next do still waits 3s after the first client disconnected: the first do kept running and holds the device")
	}
	var typed []string
	f.get(func() { typed = append(typed, f.typed...) })
	if len(typed) != 1 || typed[0] != "hello" {
		t.Errorf("typed %q", typed)
	}
	d.mu.Lock()
	steps := append([]string(nil), d.steps...)
	d.mu.Unlock()
	if len(steps) != 1 || steps[0] != "- inputText: hello" {
		t.Errorf("recorded %q: only the command that passed belongs in the recording", steps)
	}
}
