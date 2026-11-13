package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/elforama/testbuddy/internal/ci"
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
		// Active network mocks cover the frame from its first request.
		if pats := d.mockPatterns(); len(pats) > 0 {
			d.page.callIn(ctx, session, "Fetch.enable", map[string]any{"patterns": pats}, nil)
		}
	}
	// Workers and other targets just continue.
	d.page.callIn(ctx, session, "Runtime.runIfWaitingForDebugger", nil, nil)
	if typ == "iframe" {
		d.page.callIn(ctx, session, "Runtime.evaluate", map[string]any{"expression": pageJSLate}, nil)
		d.changed("")
	}
}

// frame is a cross-origin iframe attached through its own CDP session.
type frame struct {
	session string
	target  string
	url     string
	order   int
	tab     string // the session of the tab it is in (tabOfLocked)
}

// onEvent handles an event from session ("" is the driven tab's own).
func (d *Driver) onEvent(method string, params json.RawMessage, session string) {
	d.onTabEvent(method, params, session, d.rawSession(session))
}

// onTabEvent handles an event from session ("" is the driven tab's own), which came from raw:
// the session it was sent in, whichever tab is driven by the time it is handled (issue #78).
func (d *Driver) onTabEvent(method string, params json.RawMessage, session, raw string) {
	switch method {
	case "Fetch.requestPaused": // a request a network mock intercepted, in the page or a frame
		// Answered in the tab it paused in, even if the driver has moved to another meanwhile.
		d.onRequestPaused(params, raw)
		return
	case "Page.screencastFrame": // a frame of a screen recording (recording.go)
		d.onScreencastFrame(params, session)
		return
	case "Page.javascriptDialogOpening": // alert, confirm, prompt, beforeunload (dialogs.go)
		d.onDialog(params, raw)
		return
	case "Target.attachedToTarget":
		var e struct {
			SessionID  string `json:"sessionId"`
			TargetInfo struct {
				TargetID string `json:"targetId"`
				Type     string `json:"type"`
				Subtype  string `json:"subtype"`
				URL      string `json:"url"`
				OpenerID string `json:"openerId"`
			} `json:"targetInfo"`
		}
		json.Unmarshal(params, &e)
		if session == rootSession && e.TargetInfo.Type == "page" {
			// A tab the browser attached (tabs.go), or the first tab attached again.
			d.onTabAttached(e.SessionID, e.TargetInfo.TargetID, e.TargetInfo.Subtype, e.TargetInfo.URL, e.TargetInfo.OpenerID)
			return
		}
		d.mu.Lock()
		if e.TargetInfo.Type == "iframe" {
			d.frames[e.SessionID] = &frame{session: e.SessionID, target: e.TargetInfo.TargetID, url: e.TargetInfo.URL,
				order: len(d.frames), tab: d.tabOfLocked(raw)}
		} else if session != rootSession && e.TargetInfo.Type != "page" { // a worker: its requests count in its tab only
			if d.workers == nil {
				d.workers = map[string]string{}
			}
			d.workers[e.SessionID] = d.tabOfLocked(raw)
		}
		d.mu.Unlock()
		go d.attachFrame(e.SessionID, e.TargetInfo.Type, e.TargetInfo.URL)
		return
	case "Target.detachedFromTarget":
		var e struct {
			SessionID string `json:"sessionId"`
		}
		json.Unmarshal(params, &e)
		if d.onTabDetached(e.SessionID) {
			return
		}
		d.mu.Lock()
		delete(d.frames, e.SessionID)
		delete(d.workers, e.SessionID)
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
			if d.inTab(session) {
				d.changed("")
			}
			return
		}
	}
	if session != "" && !d.inTab(session) {
		return // another tab's (or its frames'), or the browser's own
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
			d.ready, d.stale = false, false // a new load (or none) is waited for afresh
			if d.staleT != nil {
				d.staleT.Stop()
			}
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
		if e.Frame.ParentID == "" { // a new main document
			d.mu.Lock()
			d.mainFrame = e.Frame.ID
			d.pageURL = e.Frame.URL
			d.docReady = false
			d.loadPending = nil // the old document's requests end with it
			d.mu.Unlock()
			d.changed(e.Frame.URL)
		}
	case "Page.loadEventFired":
		d.mu.Lock()
		d.loads++
		d.mu.Unlock()
		d.changed("")
	case "Page.domContentEventFired": // the main document's DOM is ready
		d.mu.Lock()
		d.ready, d.docReady = true, true
		d.readies++
		d.mu.Unlock()
		d.changed("")
		d.armStale()
	case "Page.navigatedWithinDocument":
		// A same-document navigation (a history step, pushState, a fragment) on a page whose
		// load never ended starts a "load" that never stops, with no DOMContentLoaded of its
		// own: the document is still the ready one, so it may be given up on as before.
		d.mu.Lock()
		if d.loading && d.docReady {
			d.ready = true
		}
		d.mu.Unlock()
		d.changed("")
		d.armStale()
	case "Network.requestWillBeSent":
		var e struct {
			RequestID   string `json:"requestId"`
			FrameID     string `json:"frameId"`
			Type        string `json:"type"`
			DocumentURL string `json:"documentURL"`
			Request     struct {
				URL string `json:"url"`
			} `json:"request"`
		}
		json.Unmarshal(params, &e)
		if session == "" {
			d.mu.Lock()
			page := d.pageURL
			if page == "" {
				page = e.DocumentURL
			}
			// The main frame's own document (the navigation) is the load itself, not a part of it.
			nav := e.Type == "Document" && (e.FrameID == d.mainFrame || d.mainFrame == "")
			if !nav && holdsLoad(e.Type, e.Request.URL, page) {
				if d.loadPending == nil {
					d.loadPending = map[string]time.Time{}
				}
				d.loadPending[e.RequestID] = time.Now()
			}
			d.mu.Unlock()
		}
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
		d.netChanged()
	case "Network.loadingFinished", "Network.loadingFailed":
		var e struct {
			RequestID string `json:"requestId"`
		}
		json.Unmarshal(params, &e)
		d.mu.Lock()
		_, ok := d.inflight[e.RequestID]
		delete(d.inflight, e.RequestID)
		_, held := d.loadPending[e.RequestID]
		if session == "" {
			delete(d.loadPending, e.RequestID)
		} else {
			held = false
		}
		d.mu.Unlock()
		if ok {
			d.netChanged()
		}
		if held {
			d.armStale() // the load may be given up on once the DOM is quiet again
		}
	}
}

// holdsLoad: a main-frame request that keeps an unfinished load counting however long the
// DOM stays quiet (up to loadTimeout), because it can still change what is on the page: a
// stylesheet or a font from anywhere (fonts often come from a CDN and move text when they
// arrive), and the site's own scripts and frames (an app's bundle builds the page). What
// does not hold it: images, media, beacons and pings, XHR/fetch (requestGrace covers
// those), and third-party scripts and frames (ads and analytics, the usual requests left
// hanging). Issue #79.
func holdsLoad(typ, reqURL, pageURL string) bool {
	switch typ {
	case "Stylesheet", "Font":
		return true
	case "Script", "Document":
		return sameSite(reqURL, pageURL)
	}
	return false
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
func (d *Driver) changed(pkg string) { d.change(pkg, true) }

// netChanged records a request starting or ending: it holds the page like a change, but is
// not the page changing, so it does not put back giving up on a stalled load (domAt): a
// page that polls every second can still be given up on.
func (d *Driver) netChanged() { d.change("", false) }

func (d *Driver) change(pkg string, dom bool) {
	d.mu.Lock()
	d.seq++
	d.solid++
	d.lastEvent = time.Now()
	if dom {
		d.domAt = d.lastEvent
	}
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

// loadGraceNow is how long a ready, quiet page's unfinished load is waited for (loadGrace).
// In CI mode 5 s: on a slow runner a page's own late script can take longer to arrive and
// change the DOM, so a stalled load is given up on later (issue #79).
func loadGraceNow() time.Duration {
	if ci.On() {
		return 5 * time.Second
	}
	return loadGrace
}

// loadBusy: the main frame is loading and that still counts. Once its DOM is ready and has
// been quiet for loadGraceNow, with nothing pending that could still change the page
// (holdsLoad), a load that has not ended (an image that never answers holds the load event
// back) no longer counts (issue #79). A tab being set up or switched to counts as loading
// (issue #78). Caller holds mu.
func (d *Driver) loadBusy() bool { return d.loading && !d.stale || d.opening > 0 }

// staleWait is how long until a ready page's unfinished load may be given up on: the DOM
// quiet for loadGraceNow, and every pending request that holds the load ended or older than
// loadTimeout. 0 or less: now. Caller holds mu.
func (d *Driver) staleWait() time.Duration {
	wait := loadGraceNow() - time.Since(d.domAt)
	for _, t0 := range d.loadPending {
		if w := loadTimeout - time.Since(t0); w > wait {
			wait = w
		}
	}
	return wait
}

// armStale starts the timer that gives up on a ready page's unfinished load (staleWait);
// onStale re-arms it while the DOM changes, and a held request's end calls it again.
func (d *Driver) armStale() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.loading || !d.ready || d.stale || d.closed {
		return
	}
	wait := d.staleWait()
	if wait < time.Millisecond {
		wait = time.Millisecond
	}
	if d.staleT == nil {
		d.staleT = time.AfterFunc(wait, d.onStale)
	} else {
		d.staleT.Reset(wait)
	}
}

func (d *Driver) onStale() {
	d.mu.Lock()
	if !d.loading || !d.ready || d.stale || d.closed {
		d.mu.Unlock()
		return
	}
	grace := loadGraceNow()
	if wait := d.staleWait(); wait > 0 { // the DOM changed since, or a script is still coming
		if d.staleT == nil {
			d.staleT = time.AfterFunc(wait, d.onStale)
		} else {
			d.staleT.Reset(wait)
		}
		d.mu.Unlock()
		return
	}
	d.stale = true
	d.notes = append(d.notes, fmt.Sprintf("web: %s is still loading %s after its page was ready and still; not waiting for it to finish",
		d.pageURL, grace))
	d.cond.Broadcast()
	d.mu.Unlock()
	d.armQuiet() // settles now, if nothing else holds the page
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
	busy := d.loadBusy() || len(d.inflight) > 0 || (d.anims > 0 && since < animGrace)
	if since < quietWindow || busy || d.closed {
		if d.anims > 0 && !d.loadBusy() && len(d.inflight) == 0 && since < animGrace {
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

// SettledNow says whether the page is settled right now, without waiting
// (driver.SettledChecker): no change pending, no navigation, and the browser still connected,
// so every change still reaches seq (issue #80).
func (d *Driver) SettledNow() bool {
	d.mu.Lock()
	ok := !d.closed && d.isSettled()
	d.mu.Unlock()
	return ok && d.page.lost() == nil
}

// isSettled: no solid change pending and no navigation (loadBusy). Caller holds mu.
func (d *Driver) isSettled() bool { return d.settledSol == d.solid && !d.loadBusy() }

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
