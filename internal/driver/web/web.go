package web

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"sync/atomic"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
)

//go:embed page.js
var pageJS string

const (
	quietWindow       = 40 * time.Millisecond
	typingQuietWindow = 120 * time.Millisecond
	softCap           = 1500 * time.Millisecond
	lateGrace         = 150 * time.Millisecond
	lateCap           = 5 * time.Second
	focusQuietFloor   = 150 * time.Millisecond
	animGrace         = 4000 * time.Millisecond
	requestGrace      = 2 * time.Second
	loadTimeout       = 30 * time.Second
	loadGrace         = 2 * time.Second
)

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
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
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

type Driver struct {
	info     device.Info
	cmd      *exec.Cmd
	exited   atomic.Bool
	lifeline io.Closer
	waited   chan struct{}
	stderr   io.Closer
	profile  string
	page     *cdp
	browser  *cdp

	mu          sync.Mutex
	cond        *sync.Cond
	seq         int64
	solid       int64
	settledSol  int64
	lastEvent   time.Time
	lastPush    time.Time
	anims       int
	timers      map[string]int
	timerAt     time.Time
	softReqs    map[string]bool
	softFrom    time.Time
	quietFor    time.Duration
	reqEndAt    time.Time
	late        bool
	lateQuiet   time.Time
	quietUntil  time.Time
	loading     bool
	ready       bool
	docReady    bool
	readies     int64
	stale       bool
	staleT      *time.Timer
	domAt       time.Time
	pageURL     string
	loadPending map[string]time.Time
	inflight    map[string]time.Time
	quiet       *time.Timer
	loads       int64
	mainFrame   string
	frames      map[string]*frame
	cssMap      []int
	frameHosts  map[int]int
	subs        map[int]chan driver.Event
	nextSub     int
	w, h        int
	offline     bool
	closed      bool
	notes       []string
	dialogs     driver.DialogAnswer
	dialogLog   []driver.Dialog
	dialogN     int

	tabs          []*tab
	cur           *tab
	claimed       map[string]bool
	opening       int
	focusN        int
	tabSeq        int
	workers       map[string]string
	closingOthers bool

	mockMu  sync.Mutex
	mocks   []driver.NetworkMock
	fetchOn bool
	held    map[string]*heldRequest

	recMu   sync.Mutex
	rec     *recording
	lastRec *recording
}

var (
	_ driver.Driver      = (*Driver)(nil)
	_ driver.Noter       = (*Driver)(nil)
	_ driver.LostChecker = (*Driver)(nil)
)

func (d *Driver) Lost() error {
	d.mu.Lock()
	closed := d.closed
	d.mu.Unlock()
	if closed {
		return nil
	}
	if err := d.page.lost(); err != nil {
		return err
	}
	return d.browser.lost()
}

const (
	MinSize = 200
	MaxSize = 10000
)

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
	cmd, stderr, lifeline, err := startBrowser(ctx, chrome, args, profile)
	if err != nil {
		os.RemoveAll(profile)
		return nil, err
	}
	d := &Driver{info: info, cmd: cmd, lifeline: lifeline, stderr: stderr, profile: profile,
		inflight: map[string]time.Time{}, subs: map[int]chan driver.Event{}, frames: map[string]*frame{}, w: w, h: h}
	var watchdogExited chan struct{}
	if lifeline != nil {
		d.waited = make(chan struct{})
		watchdogExited = d.waited
		go func() { cmd.Wait(); close(d.waited) }()
	}
	d.cond = sync.NewCond(&d.mu)
	fail := func(err error) (*Driver, error) {
		d.Close()
		return nil, err
	}

	wsURL := make(chan string, 2)
	var stderrMu sync.Mutex
	var stderrTail []string
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		defer stderr.Close()
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
	said := func(msg string) error {
		stderrMu.Lock()
		tail := strings.TrimRight(strings.Join(stderrTail, "\n"), "\n")
		stderrMu.Unlock()
		if tail != "" {
			msg += "; Chrome said:\n" + tail
		}
		return errors.New(msg)
	}
	var browserWS string
	select {
	case browserWS = <-wsURL:
	case <-stderrDone:
		select {
		case browserWS = <-wsURL:
		default:
			return fail(said("Chrome exited before it opened its DevTools endpoint"))
		}
	case <-watchdogExited:
		select {
		case <-stderrDone:
		case <-time.After(time.Second):
		}
		return fail(said("Chrome exited before it opened its DevTools endpoint"))
	case <-time.After(45 * time.Second):
		return fail(said("Chrome did not start (no DevTools endpoint within 45 s)"))
	case <-ctx.Done():
		return fail(ctx.Err())
	}
	u, _ := url.Parse(browserWS)
	if d.browser, err = dialCDP(ctx, browserWS, nil); err != nil {
		return fail(err)
	}
	var pageWS, pageID string
	for i := 0; i < 50 && pageWS == ""; i++ {
		resp, err := http.Get("http://" + u.Host + "/json/list")
		if err == nil {
			var targets []struct {
				ID   string `json:"id"`
				Type string `json:"type"`
				WS   string `json:"webSocketDebuggerUrl"`
			}
			json.NewDecoder(resp.Body).Decode(&targets)
			resp.Body.Close()
			for _, t := range targets {
				if t.Type == "page" {
					pageWS, pageID = t.WS, t.ID
					break
				}
			}
		}
		if pageWS == "" {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if pageWS == "" {
		return fail(errors.New("Chrome opened no page"))
	}
	if d.page, err = dialTabs(ctx, browserWS, d.onTabEvent); err != nil {
		return fail(err)
	}
	if err := d.attachFirstTab(ctx, pageID); err != nil {
		return fail(err)
	}
	for _, step := range tabSetup(nil) {
		if err := d.page.call(ctx, step.m, step.p, nil); err != nil {
			return fail(err)
		}
	}
	for _, step := range []cdpStep{
		{"Target.setDiscoverTargets", map[string]any{"discover": true}},
		{"Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true,
			"filter": []map[string]any{{"type": "page"}}}},
	} {
		if err := d.page.callIn(ctx, rootSession, step.m, step.p, nil); err != nil {
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
		waited := d.waited
		if waited == nil {
			waited = make(chan struct{})
			go func() { d.cmd.Wait(); close(waited) }()
		}
		select {
		case <-waited:
			if d.lifeline == nil {
				sweepGroup(d.cmd.Process)
			}
			d.exited.Store(true)
		case <-time.After(3 * time.Second):
			killBrowser(d)
			select {
			case <-waited:
				d.exited.Store(true)
			case <-time.After(time.Second):
			}
		}
	}
	if d.lifeline != nil {
		d.lifeline.Close()
	}
	d.closeStderr()
	if d.profile != "" {
		os.RemoveAll(d.profile)
	}
	return nil
}

func (d *Driver) closeStderr() {
	if d.stderr != nil {
		d.stderr.Close()
	}
}

func (d *Driver) Kill() {
	if d.cmd != nil && d.cmd.Process != nil && !d.exited.Load() {
		killBrowser(d)
	}
	d.closeStderr()
	if d.profile != "" {
		os.RemoveAll(d.profile)
	}
}

var killBrowser = func(d *Driver) {
	if d.lifeline == nil {
		killProcGroup(d.cmd.Process)
		return
	}
	d.lifeline.Close()
	select {
	case <-d.waited:
	case <-time.After(2 * time.Second):
		d.cmd.Process.Kill()
		select {
		case <-d.waited:
		case <-time.After(time.Second):
		}
	}
}

func startBrowser(ctx context.Context, chrome string, args []string, profile string) (cmd *exec.Cmd, stderr io.ReadCloser, lifeline io.WriteCloser, err error) {
	start := func(cmd *exec.Cmd) (io.ReadCloser, error) {
		r, w, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		cmd.Stderr = w
		err = cmd.Start()
		w.Close()
		if err != nil {
			r.Close()
			return nil, err
		}
		return r, nil
	}
	if self := watchdogPath(); self != "" {
		cmd = exec.Command(self, append([]string{watchdogArg, profile, chrome}, args...)...)
		cmd.Env = append(os.Environ(), watchdogEnv+"="+watchdogProto)
		cmd.SysProcAttr = ownGroup()
		if lifeline, err = cmd.StdinPipe(); err != nil {
			return nil, nil, nil, err
		}
		ready, err := cmd.StdoutPipe()
		if err != nil {
			return nil, nil, nil, err
		}
		if stderr, err = start(cmd); err == nil {
			started, silent := watchdogStarted(ctx, ready)
			if started {
				return cmd, stderr, lifeline, nil
			}
			lifeline.Close()
			done := make(chan struct{})
			go func() { cmd.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				cmd.Process.Kill()
				<-done
			}
			stderr.Close()
			if silent != nil {
				return nil, nil, nil, silent
			}
		}
	}
	cmd = exec.Command(chrome, args...)
	cmd.SysProcAttr = ownGroup()
	if stderr, err = start(cmd); err != nil {
		return nil, nil, nil, err
	}
	return cmd, stderr, nil, nil
}

func watchdogStarted(ctx context.Context, ready io.Reader) (started bool, silent error) {
	answer := make(chan bool, 1)
	go func() {
		line, _ := bufio.NewReader(ready).ReadString('\n')
		pid, ok := strings.CutPrefix(strings.TrimSpace(line), watchdogReady+" ")
		_, err := strconv.Atoi(pid)
		answer <- ok && err == nil
	}()
	select {
	case ok := <-answer:
		return ok, nil
	case <-time.After(watchdogAnswer):
		return false, fmt.Errorf("Chrome did not start: its watchdog did not answer within %v", watchdogAnswer)
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

const watchdogAnswer = 10 * time.Second
