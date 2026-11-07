// Package web drives a Chrome tab through the Chrome DevTools Protocol, as a Test Buddy
// device. Web flows (`url:` in the config) run on it.
//
// Event-driven like the mobile agents: an in-page script (page.js) pushes a CDP event on
// every DOM mutation, input, scroll and animation edge; navigation and network events come
// from CDP itself. The driver turns these into "changed" and "settled" (no change for the
// quiet window, no navigation or XHR/fetch in flight, no finite CSS animation running).
package web

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

//go:embed page.js
var pageJS string

const (
	quietWindow = 120 * time.Millisecond
	// A running finite animation (a CSS transition or animation; endless ones are not counted)
	// holds "settled" at most this long past the last event: a 1.5 s slide-in is waited out.
	animGrace    = 4000 * time.Millisecond
	requestGrace = 2 * time.Second // a first-party XHR/fetch counts as "busy" at most this long
	loadTimeout  = 30 * time.Second
)

// ChromePath finds Chrome or Chromium (TB_CHROME overrides).
func ChromePath() string {
	if p := os.Getenv("TB_CHROME"); p != "" {
		return p
	}
	var cands []string
	switch runtime.GOOS {
	case "darwin":
		cands = []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary"}
	case "windows":
		cands = []string{`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`}
		if local := os.Getenv("LOCALAPPDATA"); local != "" { // a per-user install
			cands = append(cands, filepath.Join(local, "Google", "Chrome", "Application", "chrome.exe"))
		}
	}
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	for _, n := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

// activePortURL reads the browser's DevTools address from the profile's DevToolsActivePort
// file (the port, then the browser target's path), or "" until Chrome has written it.
func activePortURL(profile string) string {
	b, err := os.ReadFile(filepath.Join(profile, "DevToolsActivePort"))
	if err != nil {
		return ""
	}
	lines := strings.Fields(string(b))
	if len(lines) < 2 || !strings.HasPrefix(lines[1], "/devtools/browser/") {
		return ""
	}
	if _, err := strconv.Atoi(lines[0]); err != nil {
		return ""
	}
	return "ws://127.0.0.1:" + lines[0] + lines[1]
}

// Driver is one browser (its own profile) with one tab.
type Driver struct {
	info    device.Info
	cmd     *exec.Cmd
	profile string
	page    *cdp
	browser *cdp

	mu         sync.Mutex
	cond       *sync.Cond
	seq        int64     // every change, restless ones included (screen reads key on it)
	solid      int64     // changes that hold the page unsettled
	settledSol int64     // the solid count when the page last settled
	lastEvent  time.Time // the latest solid change
	lastPush   time.Time
	anims      int
	loading    bool
	inflight   map[string]time.Time
	quiet      *time.Timer
	loads      int64 // load events seen
	mainFrame  string
	frames     map[string]*frame // cross-origin iframes by CDP session id
	cssMap     []int             // main-document node index -> screen node index (last Screen)
	subs       map[int]chan driver.Event
	nextSub    int
	w, h       int
	offline    bool
	closed     bool
}

var _ driver.Driver = (*Driver)(nil)

// Open starts a browser for the device. TB_WEB_HEADLESS=1 hides it; TB_WEB_SIZE=WxH sets
// the window size (default 1280x800).
func Open(ctx context.Context, info device.Info) (*Driver, error) {
	chrome := ChromePath()
	if chrome == "" {
		return nil, errors.New("Chrome was not found: install Google Chrome or set TB_CHROME")
	}
	w, h := 1280, 800
	if s := os.Getenv("TB_WEB_SIZE"); s != "" {
		fmt.Sscanf(strings.ReplaceAll(s, "x", " "), "%d %d", &w, &h)
	}
	profile, err := os.MkdirTemp("", "tb-chrome-")
	if err != nil {
		return nil, err
	}
	// A test browser must not put its own UI over the page: no password manager (it shows a
	// "change your password" warning for common passwords like "secret", blocking clicks),
	// no translate bar, no sign-in prompts.
	os.MkdirAll(filepath.Join(profile, "Default"), 0o755)
	os.WriteFile(filepath.Join(profile, "Default", "Preferences"), []byte(`{"credentials_enable_service":false,`+
		`"profile":{"password_manager_enabled":false,"password_manager_leak_detection":false},`+
		`"translate":{"enabled":false},"autofill":{"profile_enabled":false,"credit_card_enabled":false}}`), 0o644)
	args := []string{"--remote-debugging-port=0", "--user-data-dir=" + profile, "--no-first-run",
		"--no-default-browser-check", "--disable-background-networking", "--disable-sync",
		"--disable-component-update",
		"--disable-features=Translate,OptimizationHints,MediaRouter,PasswordLeakDetection,PasswordManagerOnboarding,AutofillServerCommunication",
		"--disable-save-password-bubble", "--disable-notifications",
		"--disable-popup-blocking", "--password-store=basic", "--use-mock-keychain",
		"--disable-background-timer-throttling", "--disable-renderer-backgrounding",
		"--disable-backgrounding-occluded-windows",
		fmt.Sprintf("--window-size=%d,%d", w, h)}
	if v := os.Getenv("TB_WEB_HEADLESS"); info.Headless || v == "1" || v == "true" {
		args = append(args, "--headless=new")
	}
	args = append(args, "about:blank")
	cmd := exec.Command(chrome, args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		os.RemoveAll(profile)
		return nil, err
	}
	d := &Driver{info: info, cmd: cmd, profile: profile, inflight: map[string]time.Time{}, subs: map[int]chan driver.Event{},
		frames: map[string]*frame{}, w: w, h: h}
	d.cond = sync.NewCond(&d.mu)
	fail := func(err error) (*Driver, error) {
		d.Close()
		return nil, err
	}

	// Chrome prints "DevTools listening on ws://127.0.0.1:PORT/devtools/browser/ID", and
	// writes the port and path to DevToolsActivePort in its profile. Either will do: on
	// Windows the stderr line does not always arrive.
	wsURL := make(chan string, 2)
	var stderrMu sync.Mutex
	var stderrTail []string
	go func() {
		sc := bufio.NewScanner(stderr)
		re := regexp.MustCompile(`DevTools listening on (ws://\S+)`)
		for sc.Scan() {
			if m := re.FindStringSubmatch(sc.Text()); m != nil {
				select {
				case wsURL <- m[1]:
				default:
				}
			}
			stderrMu.Lock()
			stderrTail = append(stderrTail, sc.Text())
			if len(stderrTail) > 5 {
				stderrTail = stderrTail[1:]
			}
			stderrMu.Unlock()
		}
	}()
	portFileDone := make(chan struct{})
	defer close(portFileDone)
	go func() {
		for {
			select {
			case <-portFileDone:
				return
			case <-time.After(100 * time.Millisecond):
			}
			if ws := activePortURL(profile); ws != "" {
				select {
				case wsURL <- ws:
				default:
				}
				return
			}
		}
	}()
	var browserWS string
	select {
	case browserWS = <-wsURL:
	case <-time.After(45 * time.Second):
		stderrMu.Lock()
		tail := strings.Join(stderrTail, "\n")
		stderrMu.Unlock()
		msg := "Chrome did not start (no DevTools endpoint within 45 s)"
		if tail != "" {
			msg += "; Chrome said:\n" + tail
		}
		return fail(errors.New(msg))
	case <-ctx.Done():
		return fail(ctx.Err())
	}
	u, _ := url.Parse(browserWS)
	if d.browser, err = dialCDP(ctx, browserWS, nil); err != nil {
		return fail(err)
	}
	// The page target Chrome opened with about:blank.
	var pageWS string
	for i := 0; i < 50 && pageWS == ""; i++ {
		resp, err := http.Get("http://" + u.Host + "/json/list")
		if err == nil {
			var targets []struct {
				Type string `json:"type"`
				WS   string `json:"webSocketDebuggerUrl"`
			}
			json.NewDecoder(resp.Body).Decode(&targets)
			resp.Body.Close()
			for _, t := range targets {
				if t.Type == "page" {
					pageWS = t.WS
					break
				}
			}
		}
		if pageWS == "" {
			time.Sleep(20 * time.Millisecond) // browser start-up only
		}
	}
	if pageWS == "" {
		return fail(errors.New("Chrome opened no page"))
	}
	if d.page, err = dialCDP(ctx, pageWS, d.onEvent); err != nil {
		return fail(err)
	}
	for _, step := range []struct {
		m string
		p any
	}{
		{"Page.enable", nil},
		{"Page.setLifecycleEventsEnabled", map[string]any{"enabled": true}},
		{"Runtime.enable", nil},
		{"Network.enable", nil},
		{"Runtime.addBinding", map[string]any{"name": "__tbNotify"}},
		{"Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": pageJS}},
		// Cross-origin iframes run in their own process: attach to each as it appears.
		{"Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true}},
		{"Target.setDiscoverTargets", map[string]any{"discover": true}},
	} {
		if err := d.page.call(ctx, step.m, step.p, nil); err != nil {
			return fail(err)
		}
	}
	var tree struct {
		FrameTree struct {
			Frame struct {
				ID string `json:"id"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	if err := d.page.call(ctx, "Page.getFrameTree", nil, &tree); err == nil {
		d.mainFrame = tree.FrameTree.Frame.ID
	}
	d.eval(ctx, pageJS, nil)
	return d, nil
}

// ---- events ----

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

// ---- page access ----

func (d *Driver) eval(ctx context.Context, expr string, out any) error {
	var r struct {
		Result struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := d.page.call(ctx, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true, "awaitPromise": true}, &r); err != nil {
		return err
	}
	if r.Exception != nil {
		msg := r.Exception.Exception.Description
		if msg == "" {
			msg = r.Exception.Text
		}
		return fmt.Errorf("page script: %s", msg)
	}
	if out != nil && len(r.Result.Value) > 0 {
		return json.Unmarshal(r.Result.Value, out)
	}
	return nil
}

type rawNode struct {
	D  int    `json:"d"`
	B  [4]int `json:"b"`
	C  string `json:"c"`
	T  string `json:"t"`
	ID string `json:"id"`
	CD string `json:"cd"`
	H  string `json:"h"`
	V  string `json:"v"`
	R  string `json:"r"`
	F  int    `json:"f"`

	Frame *struct {
		Src  string `json:"src"`
		X, Y int
	} `json:"frame"`

	main bool // from the main document (spliceFrames)
}

// snapshotIn evaluates the helper's snapshot in a frame session.
func (d *Driver) snapshotIn(ctx context.Context, session string) ([]rawNode, error) {
	var r struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	expr := "window.__tb ? window.__tb.snapshot() : ''"
	if err := d.page.callIn(ctx, session, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true}, &r); err != nil {
		return nil, err
	}
	if r.Result.Value == "" {
		d.page.callIn(ctx, session, "Runtime.evaluate", map[string]any{"expression": pageJS}, nil)
		if err := d.page.callIn(ctx, session, "Runtime.evaluate", map[string]any{"expression": "window.__tb.snapshot()", "returnByValue": true}, &r); err != nil {
			return nil, err
		}
	}
	var snap struct {
		Nodes []rawNode `json:"nodes"`
	}
	err := json.Unmarshal([]byte(r.Result.Value), &snap)
	return snap.Nodes, err
}

// frameFor finds the attached session for an iframe placeholder.
func (d *Driver) frameFor(src string, used map[string]bool) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	best, bestOrder := "", 1<<30
	for id, f := range d.frames {
		if used[id] {
			continue
		}
		if f.url == src {
			return id
		}
		if (f.url == "" || origin(f.url) == origin(src)) && f.order < bestOrder {
			best, bestOrder = id, f.order
		}
	}
	return best
}

// spliceFrames replaces cross-origin iframe placeholders' (empty) content with the frame's
// own snapshot, shifted to where the frame sits on the page.
func (d *Driver) spliceFrames(ctx context.Context, nodes []rawNode) []rawNode {
	has := false
	for _, n := range nodes {
		if n.Frame != nil {
			has = true
			break
		}
	}
	if !has {
		for i := range nodes {
			nodes[i].main = true
		}
		return nodes
	}
	out := make([]rawNode, 0, len(nodes))
	used := map[string]bool{}
	for _, n := range nodes {
		n.main = true
		out = append(out, n)
		if n.Frame == nil {
			continue
		}
		sess := d.frameFor(n.Frame.Src, used)
		if sess == "" {
			continue
		}
		used[sess] = true
		inner, err := d.snapshotIn(ctx, sess)
		if err != nil {
			continue
		}
		for _, c := range inner {
			c.D += n.D + 1
			c.B = [4]int{c.B[0] + n.Frame.X, c.B[1] + n.Frame.Y, c.B[2] + n.Frame.X, c.B[3] + n.Frame.Y}
			c.Frame = nil
			out = append(out, c)
		}
	}
	return out
}

func (d *Driver) Screen(ctx context.Context) (*ui.Screen, error) {
	seq := d.Seq()
	var js string
	err := d.eval(ctx, "window.__tb ? window.__tb.snapshot() : null", &js)
	if err == nil && js == "" {
		// a document that predates the helper (about:blank): install it now
		d.eval(ctx, pageJS, nil)
		err = d.eval(ctx, "window.__tb.snapshot()", &js)
	}
	if err != nil {
		return nil, err
	}
	var snap struct {
		W, H    int
		URL     string
		Title   string
		Loading bool
		Nodes   []rawNode
	}
	if err := json.Unmarshal([]byte(js), &snap); err != nil {
		return nil, err
	}
	d.mu.Lock()
	if snap.W > 0 {
		d.w, d.h = snap.W, snap.H
	}
	d.mu.Unlock()
	snap.Nodes = d.spliceFrames(ctx, snap.Nodes)
	cssMap := make([]int, 0, len(snap.Nodes))
	for i, n := range snap.Nodes {
		if n.main {
			cssMap = append(cssMap, i)
		}
	}
	d.mu.Lock()
	d.cssMap = cssMap
	d.mu.Unlock()
	win := &ui.Window{ID: 1, Type: "app", Package: snap.URL, Title: snap.Title, Active: true, Focused: true,
		Bounds: ui.Rect{R: snap.W, B: snap.H}}
	for _, r := range snap.Nodes {
		hint := r.H
		if r.V != "" {
			hint = "" // the placeholder only counts while the field is empty
		}
		n := &ui.Node{Depth: r.D, Bounds: ui.Rect{L: r.B[0], T: r.B[1], R: r.B[2], B: r.B[3]}, Class: r.C,
			Text: r.T, ID: r.ID, Desc: r.CD, Hint: hint, Value: r.V,
			Clickable: r.F&1 != 0, Enabled: r.F&2 != 0, Focused: r.F&4 != 0, Scrollable: r.F&8 != 0,
			Checked: r.F&16 != 0, Selected: r.F&32 != 0, Checkable: r.F&64 != 0, Password: r.F&128 != 0,
			Editable: r.F&256 != 0, ShowingHint: r.F&2048 != 0, Visible: true}
		if r.R != "" && n.Class != "" {
			n.Class += "[" + r.R + "]"
		}
		win.Nodes = append(win.Nodes, n)
	}
	s := &ui.Screen{Platform: "web", Width: snap.W, Height: snap.H, Seq: seq, Windows: []*ui.Window{win}}
	s.Link()
	return s, nil
}

// QueryCSS returns the indices (into the last Screen's nodes) of the elements a CSS
// selector matches.
func (d *Driver) QueryCSS(ctx context.Context, sel string) ([]int, error) {
	b, _ := json.Marshal(sel)
	var js string
	if err := d.eval(ctx, "window.__tb ? window.__tb.css("+string(b)+") : '[]'", &js); err != nil {
		return nil, err
	}
	var idx []int
	if err := json.Unmarshal([]byte(js), &idx); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	out := idx[:0]
	for _, i := range idx {
		if i >= 0 && i < len(d.cssMap) {
			out = append(out, d.cssMap[i])
		}
	}
	return out, nil
}

// CoveredAt says what a click at (x, y) would reach instead of the screen node at index (a
// veil, a dialog's backdrop, a toast over it), or "" when it reaches the node. Nodes inside
// frames are not checked ("").
func (d *Driver) CoveredAt(ctx context.Context, index, x, y int) (string, error) {
	d.mu.Lock()
	snap := -1
	for i, n := range d.cssMap {
		if n == index {
			snap = i
			break
		}
	}
	d.mu.Unlock()
	if snap < 0 {
		return "", nil
	}
	var what string
	err := d.eval(ctx, fmt.Sprintf("window.__tb && window.__tb.covered ? window.__tb.covered(%d, %d, %d) : ''", snap, x, y), &what)
	return what, err
}

// ---- identity ----

func (d *Driver) Platform() string { return "web" }
func (d *Driver) Serial() string   { return d.info.Serial }
func (d *Driver) Name() string     { return d.info.Name }
func (d *Driver) Size() (int, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.w, d.h
}

func (d *Driver) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	d.cond.Broadcast()
	d.mu.Unlock()
	if d.browser != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		d.browser.call(ctx, "Browser.close", nil, nil)
		cancel()
		d.browser.close()
	}
	if d.page != nil {
		d.page.close()
	}
	if d.cmd != nil && d.cmd.Process != nil {
		done := make(chan struct{})
		go func() { d.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			d.cmd.Process.Kill()
		}
	}
	if d.profile != "" {
		os.RemoveAll(d.profile)
	}
	return nil
}

// ---- input ----

func (d *Driver) mouse(ctx context.Context, typ string, x, y float64, clicks int, extra map[string]any) error {
	p := map[string]any{"type": typ, "x": x, "y": y, "button": "left", "clickCount": clicks}
	if typ == "mouseMoved" {
		p["button"] = "none"
	}
	if typ == "mousePressed" {
		p["buttons"] = 1
	}
	for k, v := range extra {
		p[k] = v
	}
	return d.page.call(ctx, "Input.dispatchMouseEvent", p, nil)
}

func defaults(o driver.ActionOpts) driver.ActionOpts {
	if o.NoChangeMs == 0 {
		o.NoChangeMs = 500
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	return o
}

// act runs an input and then waits (on events) for the page to react.
func (d *Driver) act(ctx context.Context, o driver.ActionOpts, do func() error) (driver.ActionResult, error) {
	o = defaults(o)
	d.mu.Lock()
	since := d.solid
	d.mu.Unlock()
	t0 := time.Now()
	if err := do(); err != nil {
		return driver.ActionResult{}, err
	}
	res := driver.ActionResult{InjectMs: int(time.Since(t0).Milliseconds())}
	if o.React && !o.NoSettle {
		// Only until the page starts reacting: the caller waits for the rest when it next
		// needs a still page.
		t1 := time.Now()
		res.Settle = driver.NoChange
		if d.waitUntil(ctx, time.Duration(o.NoChangeMs)*time.Millisecond, func() bool { return d.solid != since }) {
			res.Settle = driver.Changed
		}
		res.SettleMs = int(time.Since(t1).Milliseconds())
	} else if !o.NoSettle {
		t1 := time.Now()
		r, err := d.Settle(ctx, since, o.NoChangeMs, o.Timeout)
		if err != nil {
			return res, err
		}
		res.Settle, res.SettleMs = r, int(time.Since(t1).Milliseconds())
	}
	res.Seq = d.Seq()
	return res, nil
}

func (d *Driver) Tap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	return d.act(ctx, o, func() error {
		fx, fy := float64(x), float64(y)
		if err := d.mouse(ctx, "mouseMoved", fx, fy, 0, nil); err != nil {
			return err
		}
		if err := d.mouse(ctx, "mousePressed", fx, fy, 1, nil); err != nil {
			return err
		}
		return d.mouse(ctx, "mouseReleased", fx, fy, 1, nil)
	})
}

func (d *Driver) DoubleTap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	return d.act(ctx, o, func() error {
		fx, fy := float64(x), float64(y)
		for c := 1; c <= 2; c++ {
			if err := d.mouse(ctx, "mousePressed", fx, fy, c, nil); err != nil {
				return err
			}
			if err := d.mouse(ctx, "mouseReleased", fx, fy, c, nil); err != nil {
				return err
			}
		}
		return nil
	})
}

func (d *Driver) LongPress(ctx context.Context, x, y int, hold time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	return d.act(ctx, o, func() error {
		fx, fy := float64(x), float64(y)
		if err := d.mouse(ctx, "mousePressed", fx, fy, 1, nil); err != nil {
			return err
		}
		select { // the hold is the gesture
		case <-time.After(hold):
		case <-ctx.Done():
			return ctx.Err()
		}
		return d.mouse(ctx, "mouseReleased", fx, fy, 1, nil)
	})
}

// Swipe scrolls what is under the start point by the swipe's distance (a wheel event), the
// way a person scrolls a web page. A drag (ActionOpts.Drag: scrollUntilVisible, clearing a
// cover) scrolls that element itself instead (scrollBy, instant): Chrome may apply a wheel
// event's scroll later than the event, after the screen was read for the next step, and a tap
// then landed on the row that scrolled in under it (bench/clicks.py: "meant Item 30, hit Item
// 34", one wheel step). A point over a frame, or nothing that scrolls there, falls back to
// the wheel.
func (d *Driver) Swipe(ctx context.Context, x1, y1, x2, y2 int, dur time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	return d.act(ctx, o, func() error {
		// A swipe that starts on something made to be dragged (a range slider, a draggable
		// item, a handle whose cursor says so) is a mouse drag: scrolling would not move it.
		var draggable bool
		if err := d.eval(ctx, fmt.Sprintf(dragTargetJS, x1, y1), &draggable); err == nil && draggable {
			return d.mouseDrag(ctx, x1, y1, x2, y2, dur)
		}
		if o.Drag {
			var res string
			expr := fmt.Sprintf(scrollByJS, x1, y1, x1-x2, y1-y2)
			if err := d.eval(ctx, expr, &res); err == nil && res == "moved" {
				return nil
			}
		}
		return d.mouse(ctx, "mouseWheel", float64(x1), float64(y1), 0, map[string]any{
			"deltaX": float64(x1 - x2), "deltaY": float64(y1 - y2), "button": "none"})
	})
}

// dragTargetJS: whether the element under (x, y) is made to be dragged: a range input, a
// slider, a draggable element, or one whose cursor (or an ancestor's, up to 3 levels) offers a
// drag (grab, move, resize).
const dragTargetJS = `(function (x, y) {
  let el = document.elementFromPoint(x, y);
  while (el && el.shadowRoot) { const i = el.shadowRoot.elementFromPoint(x, y); if (!i || i === el) break; el = i; }
  for (let n = el, k = 0; n && n.nodeType === 1 && k < 4; n = n.parentElement, k++) {
    if (n.tagName === 'INPUT' && n.type === 'range') return true;
    const role = (n.getAttribute('role') || '').toLowerCase();
    if (role === 'slider' || n.getAttribute('draggable') === 'true') return true;
    const c = getComputedStyle(n).cursor;
    if (/^(grab|grabbing|move|[nsewc]+-resize|ew-resize|ns-resize|col-resize|row-resize)$/.test(c)) return true;
  }
  return false;
})(%d, %d)`

// mouseDrag presses at (x1, y1), moves to (x2, y2) in steps over dur (at least 200 ms), and
// releases there.
func (d *Driver) mouseDrag(ctx context.Context, x1, y1, x2, y2 int, dur time.Duration) error {
	if dur < 200*time.Millisecond {
		dur = 200 * time.Millisecond
	}
	const steps = 12
	if err := d.mouse(ctx, "mouseMoved", float64(x1), float64(y1), 0, nil); err != nil {
		return err
	}
	if err := d.mouse(ctx, "mousePressed", float64(x1), float64(y1), 1, nil); err != nil {
		return err
	}
	for i := 1; i <= steps; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(dur / steps):
		}
		x := float64(x1) + float64(x2-x1)*float64(i)/steps
		y := float64(y1) + float64(y2-y1)*float64(i)/steps
		if err := d.mouse(ctx, "mouseMoved", x, y, 0, map[string]any{"button": "left", "buttons": 1}); err != nil {
			return err
		}
	}
	return d.mouse(ctx, "mouseReleased", float64(x2), float64(y2), 1, nil)
}

// scrollByJS scrolls the nearest scrollable element under (x, y) (else the page) by (dx, dy)
// at once. "moved", "still" (nothing scrolled) or "frame" (the point is over a frame).
const scrollByJS = `(function (x, y, dx, dy) {
  const at = document.elementFromPoint(x, y);
  if (!at) return 'still';
  if (at.tagName === 'IFRAME' || at.tagName === 'FRAME') return 'frame';
  const scrolls = (el) => {
    const s = getComputedStyle(el);
    return (dy !== 0 && /(auto|scroll|overlay)/.test(s.overflowY) && el.scrollHeight > el.clientHeight) ||
      (dx !== 0 && /(auto|scroll|overlay)/.test(s.overflowX) && el.scrollWidth > el.clientWidth);
  };
  let el = at;
  while (el && el !== document.body && el !== document.documentElement && !scrolls(el)) el = el.parentElement;
  const t = (!el || el === document.body || el === document.documentElement) ? (document.scrollingElement || document.documentElement) : el;
  const before = t.scrollLeft + ',' + t.scrollTop;
  if (typeof window !== 'undefined' && window.__tb && window.__tb.acted) window.__tb.acted();
  t.scrollBy({ left: dx, top: dy, behavior: 'instant' });
  return t.scrollLeft + ',' + t.scrollTop !== before ? 'moved' : 'still';
})(%d, %d, %d, %d)`

type keyDef struct {
	key, code string
	vk        int
	text      string
}

var keys = map[string]keyDef{
	"enter":     {"Enter", "Enter", 13, "\r"},
	"backspace": {"Backspace", "Backspace", 8, ""},
	"tab":       {"Tab", "Tab", 9, ""},
	"escape":    {"Escape", "Escape", 27, ""},
	"home":      {"Home", "Home", 36, ""},
}

func (d *Driver) key(ctx context.Context, k keyDef) error {
	down := map[string]any{"type": "keyDown", "key": k.key, "code": k.code, "windowsVirtualKeyCode": k.vk}
	if k.text != "" {
		down["text"] = k.text
	} else {
		down["type"] = "rawKeyDown"
	}
	if err := d.page.call(ctx, "Input.dispatchKeyEvent", down, nil); err != nil {
		return err
	}
	return d.page.call(ctx, "Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": k.key, "code": k.code, "windowsVirtualKeyCode": k.vk}, nil)
}

func (d *Driver) TypeText(ctx context.Context, text string, o driver.ActionOpts) (driver.ActionResult, error) {
	if o.NoChangeMs == 0 {
		o.NoChangeMs = 300
	}
	return d.act(ctx, o, func() error {
		for _, r := range text {
			s := string(r)
			if r == '\n' {
				if err := d.key(ctx, keys["enter"]); err != nil {
					return err
				}
				continue
			}
			if err := d.page.call(ctx, "Input.dispatchKeyEvent", map[string]any{"type": "keyDown", "text": s, "unmodifiedText": s, "key": s}, nil); err != nil {
				return err
			}
			if err := d.page.call(ctx, "Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": s}, nil); err != nil {
				return err
			}
		}
		return nil
	})
}

func (d *Driver) EraseText(ctx context.Context, n int, o driver.ActionOpts) (driver.ActionResult, error) {
	if o.NoChangeMs == 0 {
		o.NoChangeMs = 300
	}
	return d.act(ctx, o, func() error {
		// No more presses than the field holds.
		length := -1 // unknown if the probe fails: press all n
		d.eval(ctx, `(() => { const e = document.activeElement; return e && 'value' in e ? e.value.length : (e && e.isContentEditable ? e.innerText.length : -1) })()`, &length)
		if length >= 0 && length < n {
			n = length
		}
		for i := 0; i < n; i++ {
			if err := d.key(ctx, keys["backspace"]); err != nil {
				return err
			}
		}
		return nil
	})
}

func (d *Driver) PressKey(ctx context.Context, key string, o driver.ActionOpts) (driver.ActionResult, error) {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "back" {
		return d.act(ctx, o, func() error { return d.back(ctx) })
	}
	def, ok := keys[k]
	if !ok {
		return driver.ActionResult{}, fmt.Errorf("key %q is not supported on web (Enter, Backspace, Tab, Escape, Home, Back)", key)
	}
	return d.act(ctx, o, func() error { return d.key(ctx, def) })
}

func (d *Driver) back(ctx context.Context) error {
	var h struct {
		CurrentIndex int `json:"currentIndex"`
		Entries      []struct {
			ID int `json:"id"`
		} `json:"entries"`
	}
	if err := d.page.call(ctx, "Page.getNavigationHistory", nil, &h); err != nil {
		return err
	}
	if h.CurrentIndex <= 0 {
		return nil
	}
	return d.page.call(ctx, "Page.navigateToHistoryEntry", map[string]any{"entryId": h.Entries[h.CurrentIndex-1].ID}, nil)
}

func (d *Driver) HideKeyboard(ctx context.Context) error { return nil }

func (d *Driver) Screenshot(ctx context.Context, scale float64, format string) ([]byte, error) {
	if format == "" {
		format = "png"
	}
	w, h := d.Size()
	p := map[string]any{"format": format}
	if format == "jpeg" {
		p["quality"] = 80
	}
	if scale > 0 && scale < 1 {
		p["clip"] = map[string]any{"x": 0, "y": 0, "width": w, "height": h, "scale": scale}
	}
	var r struct {
		Data string `json:"data"`
	}
	if err := d.page.call(ctx, "Page.captureScreenshot", p, &r); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(r.Data)
}

// ---- navigation ("apps" are URLs) ----

func isURL(s string) bool {
	return strings.Contains(s, "://") || strings.HasPrefix(s, "about:") || strings.HasPrefix(s, "data:")
}

// navigate opens the URL and returns once the page has loaded (a load event), or after
// loadTimeout with whatever loaded.
func (d *Driver) navigate(ctx context.Context, link string) error {
	d.mu.Lock()
	before := d.loads
	d.mu.Unlock()
	var r struct {
		ErrorText string `json:"errorText"`
		LoaderID  string `json:"loaderId"`
	}
	if err := d.page.call(ctx, "Page.navigate", map[string]any{"url": link}, &r); err != nil {
		return err
	}
	if r.ErrorText != "" {
		return fmt.Errorf("could not open %s: %s", link, r.ErrorText)
	}
	if r.LoaderID == "" { // same-document navigation: no load event
		return nil
	}
	d.waitUntil(ctx, loadTimeout, func() bool { return d.loads > before })
	return nil
}

func (d *Driver) LaunchApp(ctx context.Context, appID string, o driver.LaunchOpts) error {
	if !isURL(appID) {
		return fmt.Errorf("%q is not a URL the browser can open (a web flow needs `url: https://...`)", appID)
	}
	if o.ClearState {
		if err := d.ClearState(ctx, appID); err != nil {
			return err
		}
	}
	return d.navigate(ctx, appID)
}

func (d *Driver) StopApp(ctx context.Context, appID string) error {
	return d.navigate(ctx, "about:blank")
}
func (d *Driver) KillApp(ctx context.Context, appID string) error {
	return d.navigate(ctx, "about:blank")
}

func origin(link string) string {
	u, err := url.Parse(link)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func (d *Driver) ClearState(ctx context.Context, appID string) error {
	o := origin(appID)
	if o == "" {
		return nil
	}
	return d.page.call(ctx, "Storage.clearDataForOrigin", map[string]any{"origin": o, "storageTypes": "all"}, nil)
}

func (d *Driver) ClearKeychain(ctx context.Context) error { return nil }

func (d *Driver) OpenLink(ctx context.Context, link, appID string, autoVerify, browser bool) error {
	return d.navigate(ctx, link)
}

var webPermissions = map[string][]string{
	"location": {"geolocation"}, "camera": {"videoCapture"}, "microphone": {"audioCapture"},
	"notifications": {"notifications"}, "clipboard": {"clipboardReadWrite", "clipboardSanitizedWrite"},
	"sensors": {"sensors"}, "midi": {"midi"},
}

func (d *Driver) SetPermissions(ctx context.Context, appID string, perms map[string]string) error {
	o := origin(appID)
	if o == "" {
		return nil
	}
	var grant []string
	for name, v := range perms {
		list := webPermissions[strings.ToLower(name)]
		if strings.EqualFold(name, "all") {
			for _, l := range webPermissions {
				list = append(list, l...)
			}
		}
		if strings.EqualFold(v, "allow") {
			grant = append(grant, list...)
		}
	}
	if err := d.browser.call(ctx, "Browser.resetPermissions", map[string]any{}, nil); err != nil {
		return err
	}
	if len(grant) == 0 {
		return nil
	}
	if err := d.browser.call(ctx, "Browser.grantPermissions", map[string]any{"origin": o, "permissions": grant}, nil); err != nil {
		// some permission names differ across Chrome versions: grant one by one
		for _, p := range grant {
			d.browser.call(ctx, "Browser.grantPermissions", map[string]any{"origin": o, "permissions": []string{p}}, nil)
		}
	}
	return nil
}

func (d *Driver) SetLocation(ctx context.Context, lat, lon float64) error {
	return d.page.call(ctx, "Emulation.setGeolocationOverride", map[string]any{"latitude": lat, "longitude": lon, "accuracy": 10}, nil)
}

func (d *Driver) SetOrientation(ctx context.Context, orientation string) error { return nil }

func (d *Driver) SetAirplaneMode(ctx context.Context, on bool) error {
	d.mu.Lock()
	d.offline = on
	d.mu.Unlock()
	return d.page.call(ctx, "Network.emulateNetworkConditions", map[string]any{
		"offline": on, "latency": 0, "downloadThroughput": -1, "uploadThroughput": -1}, nil)
}

func (d *Driver) ToggleAirplaneMode(ctx context.Context) error {
	d.mu.Lock()
	on := !d.offline
	d.mu.Unlock()
	return d.SetAirplaneMode(ctx, on)
}

func (d *Driver) SetDarkMode(ctx context.Context, on bool) error {
	v := "light"
	if on {
		v = "dark"
	}
	return d.page.call(ctx, "Emulation.setEmulatedMedia", map[string]any{
		"features": []map[string]string{{"name": "prefers-color-scheme", "value": v}}}, nil)
}

func (d *Driver) DarkMode(ctx context.Context) (bool, error) {
	var dark bool
	err := d.eval(ctx, "matchMedia('(prefers-color-scheme: dark)').matches", &dark)
	return dark, err
}

func (d *Driver) AddMedia(ctx context.Context, paths []string) error {
	return errors.New("addMedia is not supported on web")
}

func (d *Driver) InstallApp(ctx context.Context, path string) error {
	return errors.New("installing apps is not supported on web")
}

func (d *Driver) Foreground(ctx context.Context) (string, error) {
	var href string
	err := d.eval(ctx, "location.href", &href)
	return href, err
}

func (d *Driver) WaitForApp(ctx context.Context, appID string, timeout time.Duration) error {
	return d.WaitSettled(ctx, timeout)
}

func (d *Driver) StartRecording(ctx context.Context, path string) error {
	return errors.New("screen recording is not supported on web yet")
}

func (d *Driver) StopRecording(ctx context.Context) error { return nil }
