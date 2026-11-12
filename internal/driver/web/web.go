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
	"encoding/json"
	"errors"
	"fmt"
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
	notes      []string // what the driver did on its own, for the trace (Notes)

	// Network mocks (network.go), under their own lock: requests pause on the read loop.
	mockMu  sync.Mutex
	mocks   []driver.NetworkMock
	fetchOn bool                    // Fetch interception is enabled
	held    map[string]*heldRequest // paused requests waiting out a mock's delay, by session/id

	// A screen recording in progress (recording.go), under its own lock: frames arrive on the
	// read loop.
	recMu sync.Mutex
	rec   *recording
}

var (
	_ driver.Driver      = (*Driver)(nil)
	_ driver.Noter       = (*Driver)(nil)
	_ driver.LostChecker = (*Driver)(nil)
)

// Lost reports the browser gone (driver.LostChecker): its DevTools connection ended (the
// browser exited or crashed). The web driver does not reconnect, so this is for good.
func (d *Driver) Lost() error {
	d.mu.Lock()
	closed := d.closed
	d.mu.Unlock()
	if closed {
		return nil // closed on purpose
	}
	if err := d.page.lost(); err != nil {
		return err
	}
	return d.browser.lost()
}

// MinSize and MaxSize bound a window's width and height. Outside them Chrome's screenshots
// can never come back: headless Chrome gives the page the window's height less 87 pixels, and
// a page 0 high never draws (200 leaves room for a taller window frame); and it captures
// nothing wider or higher than 16384 pixels, on some graphics cards less. Chrome on macOS
// also makes a window at least 500 wide, whatever it is asked for (checkSize says so).
const (
	MinSize = 200
	MaxSize = 10000
)

// ParseSize reads a window size, WIDTHxHEIGHT such as 1280x800: two whole numbers, each from
// MinSize to MaxSize. Spaces around the numbers (an env file's trailing newline) and a
// capital X are fine; anything else is an error, never the default size (issue #151).
func ParseSize(s string) (w, h int, err error) {
	ws, hs, ok := strings.Cut(strings.ToLower(s), "x")
	if ok {
		w, h = sizePart(ws), sizePart(hs)
	}
	if !ok || w == 0 || h == 0 {
		return 0, 0, fmt.Errorf("want WIDTHxHEIGHT, each a whole number from %d to %d, such as 1280x800, not %q", MinSize, MaxSize, s)
	}
	return w, h, nil
}

// sizePart is one side of a window size, or 0 when it is not a whole number from MinSize to
// MaxSize (digits only, no sign, once the spaces around it are trimmed).
func sizePart(s string) int {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 5 || strings.Trim(s, "0123456789") != "" {
		return 0
	}
	n, _ := strconv.Atoi(s)
	if n < MinSize || n > MaxSize {
		return 0
	}
	return n
}

// Open starts a browser for the device. TB_WEB_HEADLESS=1 hides it; TB_WEB_SIZE=WxH sets
// the window size (default 1280x800).
func Open(ctx context.Context, info device.Info) (*Driver, error) {
	chrome := ChromePath()
	if chrome == "" {
		return nil, errors.New("Chrome was not found: install Google Chrome or set TB_CHROME")
	}
	w, h := 1280, 800
	if s := os.Getenv("TB_WEB_SIZE"); s != "" {
		var err error
		if w, h, err = ParseSize(s); err != nil {
			return nil, fmt.Errorf("TB_WEB_SIZE: %v", err)
		}
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
	d.checkSize(ctx, w, h)
	return d, nil
}

// checkSize notes, for the run's trace, a page wider than the window asked for: Chrome on
// macOS makes a window at least 500 wide whatever it is asked for, so --screen-size 390x844
// gives a page 500 wide, which must not go unsaid (issue #151). Only wider counts: the
// window's own frame makes the page smaller than the window (87 pixels less high, headless
// on macOS; side borders on Windows), and that is not a size Chrome refused.
func (d *Driver) checkSize(ctx context.Context, w, h int) {
	var m struct {
		Viewport struct {
			W int `json:"clientWidth"`
			H int `json:"clientHeight"`
		} `json:"cssLayoutViewport"`
	}
	if err := d.page.call(ctx, "Page.getLayoutMetrics", nil, &m); err != nil || m.Viewport.W <= w {
		return
	}
	d.note(fmt.Sprintf("web: asked for a %dx%d window, Chrome made it wider: the page is %dx%d", w, h, m.Viewport.W, m.Viewport.H))
}

func (d *Driver) Platform() string { return "web" }

func (d *Driver) Serial() string { return d.info.Serial }

func (d *Driver) Name() string { return d.info.Name }

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
	d.dropHeld()
	d.discardRecording()
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

// Kill ends Chrome's process at once and removes its profile, without the locks or the
// browser round trips Close waits on: for a daemon stopping when Close did not finish in time.
func (d *Driver) Kill() {
	if d.cmd != nil && d.cmd.Process != nil {
		d.cmd.Process.Kill()
	}
	if d.profile != "" {
		os.RemoveAll(d.profile)
	}
}
