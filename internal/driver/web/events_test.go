package web

// The event side of the web driver: CDP events → changes, the quiet timer → "settled", and the
// waits built on them (Settle, WaitSettled), driven by injected events and real (short) timers.

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// locked runs fn under the driver's lock (its timers read the same fields).
func locked(d *Driver, fn func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	fn()
}

func isLoading(d *Driver) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.loading
}

func frameURL(d *Driver, session string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if f := d.frames[session]; f != nil {
		return f.url
	}
	return ""
}

func raw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func notify(a, s int) json.RawMessage {
	return raw(map[string]any{"name": "__tbNotify", "payload": jsonArg(map[string]int{"a": a, "s": s})})
}

// A solid change bumps seq and the solid count; a restless one only seq. Other bindings are ignored.
func TestBindingCalled(t *testing.T) {
	d := newBareDriver()
	d.onEvent("Runtime.bindingCalled", notify(2, 1), "")
	if seq, solid, _ := state(d); seq != 1 || solid != 1 || d.anims != 2 {
		t.Errorf("solid change: seq %d solid %d anims %d", seq, solid, d.anims)
	}
	d.onEvent("Runtime.bindingCalled", notify(0, 0), "")
	if seq, solid, _ := state(d); seq != 2 || solid != 1 || d.anims != 0 {
		t.Errorf("restless change: seq %d solid %d anims %d", seq, solid, d.anims)
	}
	d.onEvent("Runtime.bindingCalled", raw(map[string]any{"name": "__tbNotify", "payload": "{}"}), "")
	if _, solid, _ := state(d); solid != 2 {
		t.Errorf("a payload without s counts as solid: solid %d", solid)
	}
	d.onEvent("Runtime.bindingCalled", raw(map[string]any{"name": "otherBinding", "payload": "{}"}), "")
	if seq, _, _ := state(d); seq != 3 {
		t.Errorf("another binding changed seq to %d", seq)
	}
	d.onEvent("Runtime.bindingCalled", notify(1, 1), "frame-session")
	if seq, _, _ := state(d); seq != 4 {
		t.Errorf("a frame's change was not counted: seq %d", seq)
	}
	d.Close()
}

// The main frame's loading state holds the page unsettled; other frames' loading does not.
func TestFrameLoading(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	d.onEvent("Page.frameStartedLoading", raw(map[string]string{"frameId": "child"}), "")
	if !isLoading(d) {
		t.Error("with no main frame known, any frame's loading counts")
	}
	d.onEvent("Page.frameStoppedLoading", raw(map[string]string{"frameId": "child"}), "")
	locked(d, func() { d.mainFrame = "main" })
	d.onEvent("Page.frameStartedLoading", raw(map[string]string{"frameId": "child"}), "")
	if isLoading(d) {
		t.Error("a child frame's loading counted")
	}
	seq, _, _ := state(d)
	d.onEvent("Page.frameStartedLoading", raw(map[string]string{"frameId": "main"}), "")
	if !isLoading(d) {
		t.Error("main frame loading not seen")
	}
	if s, _, _ := state(d); s != seq+1 {
		t.Error("loading start is not a change")
	}
	d.mu.Lock()
	settled := d.isSettled()
	d.mu.Unlock()
	if settled {
		t.Error("a loading page counts as settled")
	}
	d.onEvent("Page.frameStoppedLoading", raw(map[string]string{"frameId": "main"}), "")
	if isLoading(d) {
		t.Error("main frame still loading")
	}
}

// A top-level navigation records the main frame and reports the URL; a child frame's does not.
func TestFrameNavigated(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	events, cancel := subscribe(d)
	defer cancel()
	d.onEvent("Page.frameNavigated", raw(map[string]any{"frame": map[string]string{"id": "child", "parentId": "main", "url": "https://ads.example/"}}), "")
	if d.mainFrame != "" {
		t.Errorf("child frame became main: %q", d.mainFrame)
	}
	if seq, _, _ := state(d); seq != 0 {
		t.Errorf("child navigation changed seq to %d", seq)
	}
	d.onEvent("Page.frameNavigated", raw(map[string]any{"frame": map[string]string{"id": "F1", "url": "https://example.com/home"}}), "")
	if d.mainFrame != "F1" {
		t.Errorf("mainFrame = %q", d.mainFrame)
	}
	if e := waitEvent(t, events, "changed"); e.Pkg != "https://example.com/home" {
		t.Errorf("event %+v, want the new URL as its package", e)
	}
}

// Load, DOM-ready, same-document navigation and dialogs are changes; loads are counted.
func TestPageEvents(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	for i, m := range []string{"Page.loadEventFired", "Page.navigatedWithinDocument", "Page.domContentEventFired", "Page.javascriptDialogOpening"} {
		d.onEvent(m, raw(map[string]any{}), "")
		if seq, solid, _ := state(d); seq != int64(i+1) || solid != int64(i+1) {
			t.Errorf("%s: seq %d solid %d", m, seq, solid)
		}
	}
	if d.loads != 1 {
		t.Errorf("loads = %d", d.loads)
	}
	d.onEvent("Page.lifecycleEvent", raw(map[string]any{"name": "networkIdle"}), "")
	d.onEvent("Page.loadEventFired", raw(map[string]any{}), "frame-session") // a frame's load is not the page's
	if seq, _, _ := state(d); seq != 4 || d.loads != 1 {
		t.Errorf("ignored events changed seq %d loads %d", seq, d.loads)
	}
}

// First-party XHR/fetch requests hold the page busy until they finish; documents, scripts and
// third-party requests do not.
func TestNetworkRequests(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	req := func(id, typ, u string) json.RawMessage {
		return raw(map[string]any{"requestId": id, "type": typ, "documentURL": "https://shop.example.com/cart", "request": map[string]string{"url": u}})
	}
	d.onEvent("Network.requestWillBeSent", req("1", "XHR", "https://api.example.com/cart"), "")
	d.onEvent("Network.requestWillBeSent", req("2", "Fetch", "https://shop.example.com/price"), "")
	d.onEvent("Network.requestWillBeSent", req("3", "Script", "https://shop.example.com/app.js"), "")
	d.onEvent("Network.requestWillBeSent", req("4", "XHR", "https://collect.analytics.io/beacon"), "")
	d.onEvent("Network.requestWillBeSent", req("5", "Fetch", "https://api.example.com/frame"), "frame-session")
	d.mu.Lock()
	var ids []string
	for id := range d.inflight {
		ids = append(ids, id)
	}
	d.mu.Unlock()
	sort.Strings(ids)
	if !reflect.DeepEqual(ids, []string{"1", "2", "5"}) {
		t.Errorf("in flight %v, want 1 2 5", ids)
	}
	seq, _, _ := state(d)
	d.onEvent("Network.loadingFinished", raw(map[string]string{"requestId": "1"}), "")
	d.onEvent("Network.loadingFailed", raw(map[string]string{"requestId": "5"}), "frame-session")
	d.onEvent("Network.loadingFinished", raw(map[string]string{"requestId": "3"}), "") // never tracked
	if s, _, _ := state(d); s != seq+2 {
		t.Errorf("finishing tracked requests: seq %d, want %d", s, seq+2)
	}
	d.mu.Lock()
	left := len(d.inflight)
	d.mu.Unlock()
	if left != 1 {
		t.Errorf("%d in flight, want 1", left)
	}
}

// Other events from a frame's session are ignored.
func TestFrameSessionEventsIgnored(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	for _, m := range []string{"Page.frameStartedLoading", "Page.domContentEventFired", "Runtime.consoleAPICalled"} {
		d.onEvent(m, raw(map[string]any{"frameId": "x"}), "frame-session")
	}
	if seq, _, _ := state(d); seq != 0 || isLoading(d) {
		t.Errorf("frame session events: seq %d loading %v", seq, d.loading)
	}
}

// Cross-origin frames: attached iframes are tracked by session and set up like the page; their
// URL follows target-info changes and their own navigations; detaching forgets them.
func TestFrameTargets(t *testing.T) {
	page := newFakeCDP(t)
	d := newWebDriver(t, page, nil)
	attach := func(session, target, typ, url string) {
		d.onEvent("Target.attachedToTarget", raw(map[string]any{"sessionId": session,
			"targetInfo": map[string]string{"targetId": target, "type": typ, "url": url}}), "")
	}
	attach("S1", "T1", "iframe", "https://pay.example.net/widget")
	attach("W1", "T9", "worker", "https://example.com/sw.js")
	eventually(t, "the frame's setup", func() bool { return len(page.calls("Runtime.evaluate")) == 1 })
	eventually(t, "the worker's release", func() bool { return len(page.calls("Runtime.runIfWaitingForDebugger")) == 2 })
	var s1, w1 []string
	for _, m := range page.calls("") {
		switch m.SessionID {
		case "S1":
			s1 = append(s1, m.Method)
		case "W1":
			w1 = append(w1, m.Method)
		}
	}
	wantS1 := []string{"Page.enable", "Runtime.enable", "Network.enable", "Runtime.addBinding", "Page.addScriptToEvaluateOnNewDocument",
		"Target.setAutoAttach", "Runtime.runIfWaitingForDebugger", "Runtime.evaluate"}
	if !reflect.DeepEqual(s1, wantS1) {
		t.Errorf("iframe setup %v\nwant %v", s1, wantS1)
	}
	if !reflect.DeepEqual(w1, []string{"Runtime.runIfWaitingForDebugger"}) {
		t.Errorf("worker setup %v", w1)
	}
	for _, m := range page.calls("Runtime.addBinding") {
		if m.params()["name"] != "__tbNotify" {
			t.Errorf("binding %v", m.params())
		}
	}
	for _, m := range page.calls("Page.addScriptToEvaluateOnNewDocument") {
		if m.params()["source"] != pageJS {
			t.Error("the frame did not get page.js")
		}
	}
	eventually(t, "the frame's change", func() bool { s, _, _ := state(d); return s >= 1 })
	d.mu.Lock()
	f := d.frames["S1"]
	_, worker := d.frames["W1"]
	d.mu.Unlock()
	if f == nil || f.target != "T1" || f.url != "https://pay.example.net/widget" || f.order != 0 || worker {
		t.Fatalf("frames %+v, worker tracked %v", f, worker)
	}
	d.onEvent("Target.targetInfoChanged", raw(map[string]any{"targetInfo": map[string]string{"targetId": "T1", "url": "https://pay.example.net/step2"}}), "")
	d.onEvent("Target.targetInfoChanged", raw(map[string]any{"targetInfo": map[string]string{"targetId": "T7", "url": "https://x/"}}), "")
	if u := frameURL(d, "S1"); u != "https://pay.example.net/step2" {
		t.Errorf("url after targetInfoChanged = %q", u)
	}
	seq, _, _ := state(d)
	d.onEvent("Page.frameNavigated", raw(map[string]any{"frame": map[string]string{"url": "https://pay.example.net/done"}}), "S1")
	d.onEvent("Page.frameNavigated", raw(map[string]any{"frame": map[string]string{"parentId": "p", "url": "https://inner/"}}), "S1")
	if u := frameURL(d, "S1"); u != "https://pay.example.net/done" {
		t.Errorf("url after the frame navigated = %q", u)
	}
	if s, _, _ := state(d); s != seq+2 {
		t.Errorf("frame navigations: seq %d, want %d", s, seq+2)
	}
	d.onEvent("Target.detachedFromTarget", raw(map[string]string{"sessionId": "S1"}), "")
	d.mu.Lock()
	n := len(d.frames)
	d.mu.Unlock()
	if n != 0 {
		t.Errorf("%d frames after detach", n)
	}
}

// subscribe is Events without the page connection's disconnect watch (for the bare driver).
func subscribe(d *Driver) (<-chan driver.Event, func()) {
	ch := make(chan driver.Event, 128)
	d.mu.Lock()
	d.nextSub++
	id := d.nextSub
	d.subs[id] = ch
	d.mu.Unlock()
	return ch, func() { d.mu.Lock(); delete(d.subs, id); d.mu.Unlock() }
}

// Changes within 30 ms push one "changed" event; each still bumps seq.
func TestChangedThrottlesPushes(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	events, cancel := subscribe(d)
	defer cancel()
	d.changed("")
	d.changed("")
	d.restless()
	if seq, solid, _ := state(d); seq != 3 || solid != 2 {
		t.Errorf("seq %d solid %d", seq, solid)
	}
	n := 0
	for len(events) > 0 {
		if e := <-events; e.Kind == "changed" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d changed events within 30 ms, want 1", n)
	}
	time.Sleep(35 * time.Millisecond)
	d.restless()
	if e := <-events; e.Kind != "changed" || e.Seq != 4 {
		t.Errorf("restless push %+v", e)
	}
}

// After the quiet window with nothing in flight the page settles: one "settled" event per solid change.
func TestQuietWindowSettles(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	events, cancel := subscribe(d)
	defer cancel()
	t0 := time.Now()
	d.changed("")
	e := waitEvent(t, events, "settled")
	if el := time.Since(t0); el < quietWindow {
		t.Errorf("settled after %s, before the %s quiet window", el, quietWindow)
	}
	if e.Seq != 1 {
		t.Errorf("settled seq %d", e.Seq)
	}
	_, solid, settled := state(d)
	if settled != solid {
		t.Errorf("settledSol %d, solid %d", settled, solid)
	}
	d.restless() // a restless change does not unsettle the page
	d.mu.Lock()
	ok := d.isSettled()
	d.mu.Unlock()
	if !ok {
		t.Error("a restless change unsettled the page")
	}
}

// onQuiet: busy states (loading, requests, running animations, recent change, closed) do not settle.
func TestOnQuiet(t *testing.T) {
	old := time.Now().Add(-animGrace - time.Second) // past every grace period
	cases := []struct {
		name    string
		set     func(d *Driver)
		settles bool
	}{
		{"quiet", func(d *Driver) {}, true},
		{"recent change", func(d *Driver) { d.lastEvent = time.Now() }, false},
		{"loading", func(d *Driver) { d.loading = true }, false},
		{"request in flight", func(d *Driver) { d.inflight["r"] = time.Now() }, false},
		{"animation running", func(d *Driver) { d.anims = 1; d.lastEvent = time.Now().Add(-200 * time.Millisecond) }, false},
		{"animation past its grace", func(d *Driver) { d.anims = 1 }, true},
		{"closed", func(d *Driver) { d.closed = true }, false},
		{"already settled", func(d *Driver) { d.settledSol = d.solid }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := newBareDriver()
			events, cancel := subscribe(d)
			defer cancel()
			d.seq, d.solid, d.lastEvent = 5, 3, old
			d.quiet = time.AfterFunc(time.Hour, func() {})
			defer d.quiet.Stop()
			c.set(d)
			d.onQuiet()
			got := len(events) > 0
			if got != c.settles {
				t.Errorf("settled = %v, want %v", got, c.settles)
			}
			if got {
				if e := <-events; e.Kind != "settled" || e.Seq != 5 || d.settledSol != 3 {
					t.Errorf("event %+v, settledSol %d", e, d.settledSol)
				}
			}
		})
	}
}

// A finished animation lets the page settle; while one runs the timer waits out the grace period.
func TestAnimationHoldsThenSettles(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	events, cancel := subscribe(d)
	defer cancel()
	d.onEvent("Runtime.bindingCalled", notify(1, 1), "")
	select {
	case e := <-events:
		if e.Kind == "settled" {
			t.Fatal("settled while an animation runs")
		}
	case <-time.After(quietWindow + 80*time.Millisecond):
	}
	d.onEvent("Runtime.bindingCalled", notify(0, 1), "")
	waitEvent(t, events, "settled")
}

// Settle phase 1: nothing changes within the no-change window.
func TestSettleNoChange(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	t0 := time.Now()
	r, err := d.Settle(context.Background(), 0, 60, time.Second)
	if r != driver.NoChange || err != nil {
		t.Errorf("Settle = %q, %v", r, err)
	}
	if el := time.Since(t0); el < 55*time.Millisecond || el > 900*time.Millisecond {
		t.Errorf("took %s, want about 60 ms", el)
	}
}

// Settle: a change, then quiet → changed.
func TestSettleChanged(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	go func() { time.Sleep(20 * time.Millisecond); d.changed("") }()
	r, err := d.Settle(context.Background(), 0, 500, 3*time.Second)
	if r != driver.Changed || err != nil {
		t.Errorf("Settle = %q, %v", r, err)
	}
	// A change already counted before the call ends phase 1 at once.
	d.changed("")
	if r, _ := d.Settle(context.Background(), 0, 1, 3*time.Second); r != driver.Changed {
		t.Errorf("Settle after an earlier change = %q", r)
	}
}

// Settle: a change that never settles (the page keeps loading) → timeout.
func TestSettleRestless(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	locked(d, func() { d.loading = true })
	d.changed("")
	t0 := time.Now()
	if r, err := d.Settle(context.Background(), 0, 100, 250*time.Millisecond); r != driver.Restless || err != nil {
		t.Errorf("Settle = %q, %v", r, err)
	}
	if el := time.Since(t0); el < 200*time.Millisecond {
		t.Errorf("gave up after %s, before the timeout", el)
	}
	// The change came after the whole timeout: no time left to settle.
	_, solid, _ := state(d)
	go func() { time.Sleep(30 * time.Millisecond); d.changed("") }()
	if r, _ := d.Settle(context.Background(), solid, 200, 10*time.Millisecond); r != driver.Restless {
		t.Errorf("Settle with the timeout spent = %q", r)
	}
}

// A cancelled context ends both phases with the context's error.
func TestSettleContextCancelled(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	r, err := d.Settle(ctx, 0, 5000, 10*time.Second)
	if r != driver.NoChange || err != context.Canceled {
		t.Errorf("phase 1: %q, %v", r, err)
	}
	locked(d, func() { d.loading = true })
	d.changed("")
	ctx, cancel = context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	t0 := time.Now()
	r, err = d.Settle(ctx, 0, 5000, 10*time.Second)
	if r != driver.Restless || err != context.Canceled || time.Since(t0) > 2*time.Second {
		t.Errorf("phase 2: %q, %v after %s", r, err, time.Since(t0))
	}
}

// WaitSettled returns once settled (or at the timeout, without an error); closing ends waits.
func TestWaitSettled(t *testing.T) {
	d := newBareDriver()
	t0 := time.Now()
	if err := d.WaitSettled(context.Background(), time.Second); err != nil || time.Since(t0) > 100*time.Millisecond {
		t.Errorf("an untouched page: %v after %s", err, time.Since(t0))
	}
	d.changed("")
	if err := d.WaitSettled(context.Background(), 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, solid, settled := state(d); settled != solid {
		t.Error("WaitSettled returned before the page settled")
	}
	locked(d, func() { d.loading = true })
	d.changed("")
	t0 = time.Now()
	if err := d.WaitSettled(context.Background(), 80*time.Millisecond); err != nil || time.Since(t0) < 70*time.Millisecond {
		t.Errorf("a loading page: %v after %s", err, time.Since(t0))
	}
	go func() { time.Sleep(20 * time.Millisecond); d.Close() }()
	t0 = time.Now()
	if ok := d.waitUntil(context.Background(), 5*time.Second, func() bool { return false }); ok || time.Since(t0) > 2*time.Second {
		t.Errorf("waitUntil after Close = %v after %s", ok, time.Since(t0))
	}
	if err := d.WaitForApp(context.Background(), "https://x", 10*time.Millisecond); err != nil {
		t.Errorf("WaitForApp = %v", err)
	}
}

// Seq reports every change, restless ones included.
func TestSeq(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	d.changed("")
	d.restless()
	if d.Seq() != 2 {
		t.Errorf("Seq = %d", d.Seq())
	}
}

// Events delivers pushed events and says "disconnected" when the page connection drops.
func TestEventsDisconnected(t *testing.T) {
	page := newFakeCDP(t)
	d := newWebDriver(t, page, nil)
	events, cancel := d.Events()
	defer cancel()
	d.changed("")
	if e := waitEvent(t, events, "changed"); e.Seq != 1 {
		t.Errorf("event %+v", e)
	}
	other, cancel2 := d.Events()
	cancel2()
	cancel2()
	page.dropConns()
	waitEvent(t, events, "disconnected")
	select {
	case e := <-other:
		t.Errorf("a cancelled subscription got %+v", e)
	case <-time.After(20 * time.Millisecond):
	}
}
