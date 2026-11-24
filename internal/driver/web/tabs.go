package web

import (
	"context"
	"fmt"
	"time"
)

type tab struct {
	target  string
	session string
	opener  string
	seq     int
	ready   bool
	closed  bool
	focus   int

	ctx  context.Context
	stop context.CancelFunc
}

const tabTimeout = 10 * time.Second

func newTab(target, session, opener string, seq int) *tab {
	t := &tab{target: target, session: session, opener: opener, seq: seq}
	t.ctx, t.stop = context.WithCancel(context.Background())
	return t
}

type cdpStep struct {
	m string
	p any
}

func tabSetup(pats []map[string]any) []cdpStep {
	steps := []cdpStep{
		{"Page.enable", nil},
		{"Page.setLifecycleEventsEnabled", map[string]any{"enabled": true}},
		{"Runtime.enable", nil},
		{"Network.enable", nil},
		{"Runtime.addBinding", map[string]any{"name": "__tbNotify"}},
		{"Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": pageJS}},
		{"Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true}},
	}
	if len(pats) > 0 {
		steps = append(steps, cdpStep{"Fetch.enable", map[string]any{"patterns": pats}})
	}
	return steps
}

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

func (d *Driver) rawSession(session string) string {
	if session == "" && d.page != nil {
		return d.page.tabSession()
	}
	return session
}

func (d *Driver) curSessionLocked() string {
	if d.cur != nil {
		return d.cur.session
	}
	return ""
}

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
			return false
		}
	}
	return true
}

func (d *Driver) background(raw string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cur == nil || raw == "" {
		return false
	}
	return d.tabOfLocked(raw) != d.cur.session && d.isTabLocked(d.tabOfLocked(raw))
}

func (d *Driver) isTabLocked(session string) bool {
	for _, t := range d.tabs {
		if t.session == session {
			return true
		}
	}
	return false
}

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

func (d *Driver) setUpTab(t *tab, url string) {
	ctx, cancel := context.WithTimeout(t.ctx, tabTimeout)
	defer cancel()
	defer d.doneOpening()
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

func (d *Driver) doneOpening() {
	d.mu.Lock()
	d.opening--
	d.cond.Broadcast()
	d.mu.Unlock()
	d.changed("")
}

func (d *Driver) driveLocked(t *tab) {
	d.cur = t
	d.focusN++
	t.focus = d.focusN
	d.page.setTab(t.session)
	d.inflight = map[string]time.Time{}
	d.loadPending = nil
	d.anims = 0
	d.timers, d.softReqs = nil, nil
	d.stale = false
	if d.staleT != nil {
		d.staleT.Stop()
	}
	d.cssMap = nil
	d.frameHosts = nil
}

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
		d.page.callIn(ctx, t.session, "Fetch.disable", nil, nil)
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

func (d *Driver) curURLLocked() string {
	if d.pageURL != "" {
		return d.pageURL
	}
	return "the tab it drives"
}
