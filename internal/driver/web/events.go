package web

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// attachFrame sets up a cross-origin iframe's session: the same change events and snapshot
// helper as the page.
func (d *Driver) attachFrame(session, typ, url string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if typ == "iframe" {
		for _, step := range []struct {
			m string
			p any
		}{
			{"Page.enable", nil},
			{"Runtime.enable", nil},
			{"Network.enable", nil},
			{"Runtime.addBinding", map[string]any{"name": "__tbNotify"}},
			{"Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": pageJS}},
			{"Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true}},
		} {
			d.page.callIn(ctx, session, step.m, step.p, nil)
		}
	}
	// Workers and other targets just continue.
	d.page.callIn(ctx, session, "Runtime.runIfWaitingForDebugger", nil, nil)
	if typ == "iframe" {
		d.page.callIn(ctx, session, "Runtime.evaluate", map[string]any{"expression": pageJS}, nil)
		d.changed("")
	}
}

// frame is a cross-origin iframe attached through its own CDP session.
type frame struct {
	session string
	target  string
	url     string
	order   int
}

func (d *Driver) onEvent(method string, params json.RawMessage, session string) {
	switch method {
	case "Target.attachedToTarget":
		var e struct {
			SessionID  string `json:"sessionId"`
			TargetInfo struct {
				TargetID string `json:"targetId"`
				Type     string `json:"type"`
				URL      string `json:"url"`
			} `json:"targetInfo"`
		}
		json.Unmarshal(params, &e)
		if e.TargetInfo.Type == "iframe" {
			d.mu.Lock()
			d.frames[e.SessionID] = &frame{session: e.SessionID, target: e.TargetInfo.TargetID, url: e.TargetInfo.URL, order: len(d.frames)}
			d.mu.Unlock()
		}
		go d.attachFrame(e.SessionID, e.TargetInfo.Type, e.TargetInfo.URL)
		return
	case "Target.detachedFromTarget":
		var e struct {
			SessionID string `json:"sessionId"`
		}
		json.Unmarshal(params, &e)
		d.mu.Lock()
		delete(d.frames, e.SessionID)
		d.mu.Unlock()
		d.changed("")
		return
	case "Target.targetInfoChanged":
		var e struct {
			TargetInfo struct {
				TargetID string `json:"targetId"`
				URL      string `json:"url"`
			} `json:"targetInfo"`
		}
		json.Unmarshal(params, &e)
		d.mu.Lock()
		for _, f := range d.frames {
			if f.target == e.TargetInfo.TargetID {
				f.url = e.TargetInfo.URL
			}
		}
		d.mu.Unlock()
		return
	case "Page.frameNavigated":
		if session != "" { // a frame navigated inside its own session: remember its url
			var e struct {
				Frame struct {
					ParentID string `json:"parentId"`
					URL      string `json:"url"`
				} `json:"frame"`
			}
			json.Unmarshal(params, &e)
			d.mu.Lock()
			if f := d.frames[session]; f != nil && e.Frame.ParentID == "" {
				f.url = e.Frame.URL
			}
			d.mu.Unlock()
			d.changed("")
			return
		}
	}
	if session != "" && method != "Runtime.bindingCalled" && method != "Network.requestWillBeSent" &&
		method != "Network.loadingFinished" && method != "Network.loadingFailed" {
		return // other frame-session events: only its changes and requests matter
	}
	switch method {
	case "Runtime.bindingCalled":
		var e struct {
			Name    string `json:"name"`
			Payload string `json:"payload"`
		}
		json.Unmarshal(params, &e)
		if e.Name != "__tbNotify" {
			return
		}
		p := struct {
			A int `json:"a"`
			S int `json:"s"`
		}{S: 1}
		json.Unmarshal([]byte(e.Payload), &p)
		d.mu.Lock()
		d.anims = p.A
		d.mu.Unlock()
		if p.S == 0 {
			d.restless()
		} else {
			d.changed("")
		}
	case "Page.frameStartedLoading", "Page.frameStoppedLoading":
		var e struct {
			FrameID string `json:"frameId"`
		}
		json.Unmarshal(params, &e)
		d.mu.Lock()
		main := e.FrameID == d.mainFrame || d.mainFrame == ""
		if main {
			d.loading = method == "Page.frameStartedLoading"
		}
		d.mu.Unlock()
		if main {
			d.changed("")
		}
	case "Page.frameNavigated":
		var e struct {
			Frame struct {
				ID       string `json:"id"`
				ParentID string `json:"parentId"`
				URL      string `json:"url"`
			} `json:"frame"`
		}
		json.Unmarshal(params, &e)
		if e.Frame.ParentID == "" {
			d.mu.Lock()
			d.mainFrame = e.Frame.ID
			d.mu.Unlock()
			d.changed(e.Frame.URL)
		}
	case "Page.loadEventFired":
		d.mu.Lock()
		d.loads++
		d.mu.Unlock()
		d.changed("")
	case "Page.navigatedWithinDocument", "Page.domContentEventFired", "Page.javascriptDialogOpening":
		d.changed("")
	case "Network.requestWillBeSent":
		var e struct {
			RequestID   string `json:"requestId"`
			Type        string `json:"type"`
			DocumentURL string `json:"documentURL"`
			Request     struct {
				URL string `json:"url"`
			} `json:"request"`
		}
		json.Unmarshal(params, &e)
		// The page's own API calls mean it is working; third-party analytics and beacons
		// (often left open) do not.
		if (e.Type != "XHR" && e.Type != "Fetch") || !sameSite(e.Request.URL, e.DocumentURL) {
			return
		}
		d.mu.Lock()
		d.inflight[e.RequestID] = time.Now()
		d.mu.Unlock()
		id := e.RequestID
		time.AfterFunc(requestGrace, func() { // a request that never ends stops counting
			d.mu.Lock()
			_, ok := d.inflight[id]
			delete(d.inflight, id)
			d.mu.Unlock()
			if ok {
				d.armQuiet()
			}
		})
		d.changed("")
	case "Network.loadingFinished", "Network.loadingFailed":
		var e struct {
			RequestID string `json:"requestId"`
		}
		json.Unmarshal(params, &e)
		d.mu.Lock()
		_, ok := d.inflight[e.RequestID]
		delete(d.inflight, e.RequestID)
		d.mu.Unlock()
		if ok {
			d.changed("")
		}
	}
}

// sameSite reports whether two URLs share a site (the registrable domain), so
// api.example.com counts as example.com's own. IP hosts must match exactly.
func sameSite(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return site(ua.Hostname()) == site(ub.Hostname())
}

// site is a host's registrable domain: the label before its public suffix. Without the
// public suffix list this approximates: a two-letter country code with a common second
// level (co.uk, com.au, ne.jp) is a suffix too, else the suffix is the last label.
func site(h string) string {
	h = strings.TrimSuffix(strings.ToLower(h), ".")
	if net.ParseIP(h) != nil {
		return h
	}
	parts := strings.Split(h, ".")
	keep := 2
	if n := len(parts); n >= 3 && len(parts[n-1]) == 2 && secondLevel[parts[n-2]] {
		keep = 3
	}
	if len(parts) > keep {
		parts = parts[len(parts)-keep:]
	}
	return strings.Join(parts, ".")
}

// secondLevel are the second-level labels country codes commonly register under.
var secondLevel = map[string]bool{"co": true, "com": true, "net": true, "org": true, "gov": true,
	"edu": true, "ac": true, "or": true, "ne": true, "go": true, "gob": true, "nic": true, "mil": true}

// restless records a change from an element that never stops changing: the screen did
// change (seq), but it does not hold the page unsettled.
func (d *Driver) restless() {
	d.mu.Lock()
	d.seq++
	push := time.Since(d.lastPush) >= 30*time.Millisecond
	if push {
		d.lastPush = time.Now()
	}
	seq := d.seq
	d.cond.Broadcast()
	d.mu.Unlock()
	if push {
		d.emit(driver.Event{Kind: "changed", Seq: seq})
	}
}

// changed records a UI change: bump seq, nudge subscribers, restart the quiet window.
func (d *Driver) changed(pkg string) {
	d.mu.Lock()
	d.seq++
	d.solid++
	d.lastEvent = time.Now()
	push := time.Since(d.lastPush) >= 30*time.Millisecond
	if push {
		d.lastPush = d.lastEvent
	}
	seq := d.seq
	d.cond.Broadcast()
	d.mu.Unlock()
	if push {
		d.emit(driver.Event{Kind: "changed", Seq: seq, Pkg: pkg})
	}
	d.armQuiet()
}

func (d *Driver) armQuiet() {
	d.mu.Lock()
	defer d.mu.Unlock()
	wait := quietWindow - time.Since(d.lastEvent)
	if wait < time.Millisecond {
		wait = time.Millisecond
	}
	if d.quiet == nil {
		d.quiet = time.AfterFunc(wait, d.onQuiet)
	} else {
		d.quiet.Reset(wait)
	}
}

func (d *Driver) onQuiet() {
	d.mu.Lock()
	since := time.Since(d.lastEvent)
	busy := d.loading || len(d.inflight) > 0 || (d.anims > 0 && since < animGrace)
	if since < quietWindow || busy || d.closed {
		if d.anims > 0 && !d.loading && len(d.inflight) == 0 && since < animGrace {
			d.quiet.Reset(animGrace - since)
		}
		d.mu.Unlock()
		return // the event that ends the busy state re-arms the timer
	}
	if d.settledSol == d.solid {
		d.mu.Unlock()
		return
	}
	d.settledSol = d.solid
	seq := d.seq
	d.cond.Broadcast()
	d.mu.Unlock()
	d.emit(driver.Event{Kind: "settled", Seq: seq})
}

func (d *Driver) emit(e driver.Event) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, ch := range d.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

func (d *Driver) Events() (<-chan driver.Event, func()) {
	ch := make(chan driver.Event, 128)
	d.mu.Lock()
	d.nextSub++
	id := d.nextSub
	d.subs[id] = ch
	d.mu.Unlock()
	stopped := make(chan struct{})
	go func() {
		select {
		case <-d.page.done:
			d.mu.Lock()
			if _, ok := d.subs[id]; ok {
				select {
				case ch <- driver.Event{Kind: "disconnected"}:
				default:
				}
			}
			d.mu.Unlock()
		case <-stopped:
		}
	}()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			d.mu.Lock()
			delete(d.subs, id)
			d.mu.Unlock()
			close(stopped)
		})
	}
}

func (d *Driver) Seq() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.seq
}

// waitUntil blocks on the condition variable until pred() or the timeout. Broadcasts come
// from events; a timer wakes it at the deadline.
func (d *Driver) waitUntil(ctx context.Context, timeout time.Duration, pred func() bool) bool {
	deadline := time.Now().Add(timeout)
	t := time.AfterFunc(timeout, func() { d.mu.Lock(); d.cond.Broadcast(); d.mu.Unlock() })
	defer t.Stop()
	stop := context.AfterFunc(ctx, func() { d.mu.Lock(); d.cond.Broadcast(); d.mu.Unlock() })
	defer stop()
	d.mu.Lock()
	defer d.mu.Unlock()
	for !pred() {
		if !time.Now().Before(deadline) || ctx.Err() != nil || d.closed {
			return false
		}
		d.cond.Wait()
	}
	return true
}

func (d *Driver) WaitSettled(ctx context.Context, timeout time.Duration) error {
	d.waitUntil(ctx, timeout, d.isSettled)
	return nil
}

// isSettled: no solid change pending and no navigation. Caller holds mu.
func (d *Driver) isSettled() bool { return d.settledSol == d.solid && !d.loading }

// Settle waits for the page to react to an action taken when the solid change count was
// since (see solidCount).
func (d *Driver) Settle(ctx context.Context, since int64, noChangeMs int, timeout time.Duration) (driver.SettleResult, error) {
	noChange := time.Duration(noChangeMs) * time.Millisecond
	start := time.Now()
	// Phase 1: wait for a solid change, up to the no-change window.
	changed := d.waitUntil(ctx, noChange, func() bool { return d.solid != since })
	if !changed {
		return driver.NoChange, ctx.Err()
	}
	left := timeout - time.Since(start)
	if left <= 0 {
		return driver.Restless, nil
	}
	if d.waitUntil(ctx, left, d.isSettled) {
		return driver.Changed, nil
	}
	return driver.Restless, ctx.Err()
}
