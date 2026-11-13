package web

// Tabs (issue #78). A link with target=_blank, or a script's window.open, opens a new tab,
// and a person's attention follows it: so does the driver. It drives one tab at a time,
// through the tab's flattened session on a browser-level connection (cdp.setTab): calls and
// events with session "" are the driven tab's, as they were when the driver was connected to
// its one tab directly.
//
// The browser attaches every new tab paused before it runs anything (Target.setAutoAttach
// on the browser target, waitForDebuggerOnStart), so the driver sets it up like the first
// one (events, the page helper, active network mocks) before its first request, makes it
// the tab it drives, and only then lets it run. Until then the page counts as loading
// (opening), so the tap that opened the tab waits for it. Of tabs opened at once, the one
// opened last is driven, whichever is set up first.
//
// When the driven tab closes (a popup that closes itself, or a tap on its own close button)
// the driver goes back to the tab that opened it, or else to the open tab it drove last. A
// tab being left keeps running; its events no longer count. launchApp closes every tab but
// the first, so a flow starts with one tab.

import (
	"context"
	"fmt"
	"time"
)

// tab is one of the browser's tabs.
type tab struct {
	target  string // its target id
	session string // its session on the driver's connection
	opener  string // the target id of the tab that opened it ("" for the first)
	seq     int    // the order it was attached in (the first tab is 0)
	ready   bool   // set up: it can be driven
	closed  bool
	focus   int // when it was last made the driven tab (focusN)

	// ctx bounds the calls made to set the tab up or switch to it: it ends when the tab
	// closes, so a setup left waiting on a tab that is gone does not hold the page busy.
	ctx  context.Context
	stop context.CancelFunc
}

// tabTimeout bounds setting a tab up, or switching back to one.
const tabTimeout = 10 * time.Second

// newTab is a tab with its context (tab.ctx).
func newTab(target, session, opener string, seq int) *tab {
	t := &tab{target: target, session: session, opener: opener, seq: seq}
	t.ctx, t.stop = context.WithCancel(context.Background())
	return t
}

// cdpStep is one setup command.
type cdpStep struct {
	m string
	p any
}

// tabSetup is how a tab is set up: navigation and network events, the change binding and the
// page helper in every document, its cross-origin iframes attached as they appear, and the
// network mocks' patterns (pats, when any mock is active) intercepted from its first request.
func tabSetup(pats []map[string]any) []cdpStep {
	steps := []cdpStep{
		{"Page.enable", nil},
		{"Page.setLifecycleEventsEnabled", map[string]any{"enabled": true}},
		{"Runtime.enable", nil},
		{"Network.enable", nil},
		{"Runtime.addBinding", map[string]any{"name": "__tbNotify"}},
		{"Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": pageJS}},
		// Cross-origin iframes run in their own process: attach to each as it appears.
		{"Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true}},
	}
	if len(pats) > 0 {
		steps = append(steps, cdpStep{"Fetch.enable", map[string]any{"patterns": pats}})
	}
	return steps
}

// attachFirstTab attaches the driver's connection to the tab Chrome opened (target id) and
// drives it.
func (d *Driver) attachFirstTab(ctx context.Context, id string) error {
	d.mu.Lock()
	d.claimed = map[string]bool{id: true}
	d.mu.Unlock()
	var r struct {
		SessionID string `json:"sessionId"`
	}
	if err := d.page.callIn(ctx, rootSession, "Target.attachToTarget", map[string]any{"targetId": id, "flatten": true}, &r); err != nil {
		return err
	}
	t := newTab(id, r.SessionID, "", 0)
	t.ready = true
	d.mu.Lock()
	d.tabs, d.cur = []*tab{t}, t
	d.focusN++
	t.focus = d.focusN
	d.mu.Unlock()
	d.page.setTab(r.SessionID)
	return nil
}

// rawSession is the session an event came from: "" is the driven tab's own.
func (d *Driver) rawSession(session string) string {
	if session == "" && d.page != nil {
		return d.page.tabSession()
	}
	return session
}

// curSessionLocked is the driven tab's session ("" without tabs: a driver on one page's
// own connection). Caller holds mu.
func (d *Driver) curSessionLocked() string {
	if d.cur != nil {
		return d.cur.session
	}
	return ""
}

// tabOfLocked is the session of the tab an event's raw session belongs to: the driven tab
// for "", a frame's or a worker's tab, or the session itself (a tab's own). Caller holds mu.
func (d *Driver) tabOfLocked(raw string) string {
	if raw == "" {
		return d.curSessionLocked()
	}
	if f := d.frames[raw]; f != nil {
		return f.tab
	}
	if tab, ok := d.workers[raw]; ok {
		return tab
	}
	return raw
}

// inTab reports whether events from a session other than "" count as the driven tab's: a
// frame or a worker in it, or a session the driver knows nothing about (as before tabs were
// followed: such a session was taken as the page's). Unknown sessions include a page the
// browser attached a second time (the first tab, by its auto-attach), which is let go at
// once and enables no events. Not counted: the browser's own events, another tab's, and a
// frame's or worker's in another tab.
func (d *Driver) inTab(session string) bool {
	if session == rootSession {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	cur := d.curSessionLocked()
	if f := d.frames[session]; f != nil {
		return f.tab == cur
	}
	if tab, ok := d.workers[session]; ok {
		return tab == cur
	}
	for _, t := range d.tabs {
		if t.session == session {
			return false // the driven tab's own events arrive with session ""
		}
	}
	return true
}

// background reports whether raw (an event's own session) is in a tab the driver does not
// drive: that tab's own session, or a frame or worker in it.
func (d *Driver) background(raw string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cur == nil || raw == "" {
		return false
	}
	return d.tabOfLocked(raw) != d.cur.session && d.isTabLocked(d.tabOfLocked(raw))
}

// isTabLocked reports whether session is an open tab's. Caller holds mu.
func (d *Driver) isTabLocked(session string) bool {
	for _, t := range d.tabs {
		if t.session == session {
			return true
		}
	}
	return false
}

// onTabAttached takes a page the browser attached: a new tab, paused until it is set up. A
// page that is not a tab (prerendering) or one already driven (the first tab, attached again)
// is let run and left alone. It runs on the read loop: the setup runs on its own.
func (d *Driver) onTabAttached(session, target, subtype, url, opener string) {
	d.mu.Lock()
	if d.closed || d.claimed[target] || subtype != "" {
		again := d.claimed[target]
		d.mu.Unlock()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), tabTimeout)
			defer cancel()
			d.page.callIn(ctx, session, "Runtime.runIfWaitingForDebugger", nil, nil)
			if again {
				d.page.callIn(ctx, rootSession, "Target.detachFromTarget", map[string]any{"sessionId": session}, nil)
			}
		}()
		return
	}
	if d.claimed == nil {
		d.claimed = map[string]bool{}
	}
	d.claimed[target] = true
	d.tabSeq++
	t := newTab(target, session, opener, d.tabSeq)
	d.tabs = append(d.tabs, t)
	d.opening++
	d.mu.Unlock()
	d.changed(url)
	go d.setUpTab(t, url)
}

// setUpTab sets a new tab up, drives it (unless a tab opened after it is driven already), and
// lets it run.
func (d *Driver) setUpTab(t *tab, url string) {
	ctx, cancel := context.WithTimeout(t.ctx, tabTimeout)
	defer cancel()
	defer d.doneOpening()
	// The setup is sent, in order, before the tab runs: a paused tab answers some of it only
	// once it runs. The replies are waited for after.
	steps := tabSetup(d.mockPatterns())
	var replies []func(any) error
	for _, step := range steps {
		replies = append(replies, d.page.send(ctx, t.session, step.m, step.p))
	}
	d.mu.Lock()
	t.ready = true
	drive := !t.closed && !d.closed && (d.cur == nil || d.cur.closed || d.cur.seq < t.seq)
	var prev *tab
	if drive {
		prev = d.cur
		d.driveLocked(t)
		// A new tab: what it loads, it loads from here on, and its events say so.
		d.mainFrame, d.pageURL = "", url
		d.loading, d.ready, d.docReady = false, false, false
		d.notes = append(d.notes, "web: the page opened a new tab: tb drives it now")
	}
	d.mu.Unlock()
	resumed := d.page.send(ctx, t.session, "Runtime.runIfWaitingForDebugger", nil)
	for i, wait := range replies {
		if err := wait(nil); err != nil {
			d.tabFailed(t, steps[i].m, err)
		}
	}
	resumed(nil)
	if !drive {
		return
	}
	d.page.callIn(ctx, t.session, "Page.bringToFront", nil, nil)
	d.moveRecording(ctx, prev, t)
	d.changed(url)
}

// tabFailed notes a setup step that failed in a tab still open (one that closed meanwhile
// fails them all, and that is no news).
func (d *Driver) tabFailed(t *tab, step string, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if t.closed || d.closed {
		return
	}
	what := ""
	if step == "Fetch.enable" {
		what = ": network mocks may not apply in it"
	}
	d.notes = append(d.notes, fmt.Sprintf("web: setting up a tab failed (%s: %v)%s", step, err, what))
}

// doneOpening ends a tab's setup or a switch back: the page no longer counts as loading for it.
func (d *Driver) doneOpening() {
	d.mu.Lock()
	d.opening--
	d.cond.Broadcast()
	d.mu.Unlock()
	d.changed("")
}

// driveLocked makes t the driven tab and forgets the page state of the one before (what is
// in flight, animations, a load, the last screen's node map): t's is read or reported anew.
// Caller holds mu.
func (d *Driver) driveLocked(t *tab) {
	d.cur = t
	d.focusN++
	t.focus = d.focusN
	d.page.setTab(t.session)
	d.inflight = map[string]time.Time{}
	d.loadPending = nil
	d.anims = 0
	d.stale = false
	if d.staleT != nil {
		d.staleT.Stop()
	}
	d.cssMap = nil
}

// onTabDetached handles a tab's session ending (the tab closed), and reports whether session
// was a tab's. Calls still waiting on the tab end. When it was the driven tab, the driver goes
// back to the tab that opened it, or else to the open tab it drove last. It runs on the read
// loop.
func (d *Driver) onTabDetached(session string) bool {
	d.mu.Lock()
	var t *tab
	for i, x := range d.tabs {
		if x.session == session && session != "" {
			t = x
			d.tabs = append(d.tabs[:i:i], d.tabs[i+1:]...)
			break
		}
	}
	if t == nil {
		d.mu.Unlock()
		return false
	}
	t.closed = true
	t.stop()
	for id, f := range d.frames {
		if f.tab == session {
			delete(d.frames, id)
		}
	}
	for id, tab := range d.workers {
		if tab == session {
			delete(d.workers, id)
		}
	}
	var next *tab
	if d.cur == t {
		next = d.nextTabLocked(t)
		if next != nil {
			d.opening++
		} else {
			d.notes = append(d.notes, "web: the last tab closed")
		}
	}
	d.mu.Unlock()
	if next != nil {
		go d.switchBack(next)
	}
	d.changed("")
	return true
}

// nextTabLocked is the tab to drive once t closed: its opener if open, else the open tab
// driven last. Caller holds mu.
func (d *Driver) nextTabLocked(t *tab) *tab {
	var last *tab
	for _, x := range d.tabs {
		if !x.ready {
			continue
		}
		if x.target == t.opener {
			return x
		}
		if last == nil || x.focus > last.focus {
			last = x
		}
	}
	return last
}

// switchBack drives a tab that was open all along: its page state is read now, and the
// network mocks active now apply to it.
func (d *Driver) switchBack(t *tab) {
	ctx, cancel := context.WithTimeout(t.ctx, tabTimeout)
	defer cancel()
	defer d.doneOpening()
	var tree struct {
		FrameTree struct {
			Frame struct {
				ID  string `json:"id"`
				URL string `json:"url"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	if err := d.page.callIn(ctx, t.session, "Page.getFrameTree", nil, &tree); err != nil {
		d.tabFailed(t, "Page.getFrameTree", err)
	}
	var rs struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	d.page.callIn(ctx, t.session, "Runtime.evaluate", map[string]any{"expression": "document.readyState", "returnByValue": true}, &rs)
	if pats := d.mockPatterns(); len(pats) > 0 {
		if err := d.page.callIn(ctx, t.session, "Fetch.enable", map[string]any{"patterns": pats}, nil); err != nil {
			d.tabFailed(t, "Fetch.enable", err)
		}
	} else {
		d.page.callIn(ctx, t.session, "Fetch.disable", nil, nil) // mocks cleared while it was left; an error: none were on
	}
	d.page.callIn(ctx, t.session, "Page.bringToFront", nil, nil)
	d.mu.Lock()
	if t.closed || d.closed {
		d.mu.Unlock()
		return
	}
	prev := d.cur
	d.driveLocked(t)
	url := tree.FrameTree.Frame.URL
	d.mainFrame, d.pageURL = tree.FrameTree.Frame.ID, url
	d.loading = rs.Result.Value != "complete"
	d.ready = rs.Result.Value != "loading"
	d.docReady = d.ready
	if !d.closingOthers {
		d.notes = append(d.notes, fmt.Sprintf("web: the tab tb drove closed: tb drives %s again", url))
	}
	d.mu.Unlock()
	d.moveRecording(ctx, prev, t)
	d.changed(url)
	d.armStale()
}

// moveRecording moves a screen recording in progress from the tab left (if still open) to
// the tab driven now. The screencast of the tab driven now starts only here: what that tab
// painted before (a new tab's first paint, while it was set up and let run) may be missing
// from the video, which shows the tab left until then.
func (d *Driver) moveRecording(ctx context.Context, from, to *tab) {
	d.recMu.Lock()
	rec := d.rec
	d.recMu.Unlock()
	if rec == nil || from == to {
		return
	}
	if from != nil && !from.closed {
		d.page.callIn(ctx, from.session, "Page.stopScreencast", nil, nil)
	}
	d.page.callIn(ctx, to.session, "Page.startScreencast", d.screencastParams(), nil)
}

// closeOtherTabs closes every tab but the first (the oldest open), and waits, a few seconds
// at most, until the driver drives that one: launchApp starts a flow in one tab. The trace
// says how many it closed, not each switch.
func (d *Driver) closeOtherTabs(ctx context.Context) {
	d.mu.Lock()
	if len(d.tabs) < 2 {
		d.mu.Unlock()
		return
	}
	keep := d.tabs[0]
	others := append([]*tab(nil), d.tabs[1:]...)
	d.closingOthers = true
	d.mu.Unlock()
	for _, t := range others {
		d.page.callIn(ctx, rootSession, "Target.closeTarget", map[string]any{"targetId": t.target}, nil)
	}
	back := d.waitUntil(ctx, 5*time.Second, func() bool { return d.cur == keep && d.opening == 0 })
	d.mu.Lock()
	d.closingOthers = false
	note := fmt.Sprintf("web: launchApp closed %d other tab(s)", len(others))
	if !back {
		note += fmt.Sprintf("; tb was not back on the first tab within 5 s, and goes on in %s", d.curURLLocked())
	}
	d.notes = append(d.notes, note)
	d.mu.Unlock()
}

// curURLLocked names the driven tab for a note. Caller holds mu.
func (d *Driver) curURLLocked() string {
	if d.pageURL != "" {
		return d.pageURL
	}
	return "the tab it drives"
}
