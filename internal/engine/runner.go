// Package engine runs flows on a device, around device events.
//
// An element lookup reads the screen once; if the element
// is not there it sleeps until the agent pushes "the UI changed and settled" (or keeps
// changing for longer than a frame budget), then reads again. A screen read is reused for
// as long as the device reports no change since.
package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/js"
	"github.com/elforama/testbuddy/internal/timeline"
	"github.com/elforama/testbuddy/internal/ui"
)

// Status of a command or flow.
type Status string

const (
	Passed  Status = "passed"
	Warned  Status = "warned"
	Skipped Status = "skipped"
	Failed  Status = "failed"
)

// Event tells a Reporter about progress.
type Event struct {
	Device  string // role or device name (multi-device runs)
	Cmd     *flow.Command
	Desc    string
	Depth   int
	Start   bool
	Status  Status
	Err     error
	Elapsed time.Duration
	Log     string // console.log or diagnostic line
	Hint    string // a step that did nothing as written (iOS back): once per flow (Runner.hint)
}

// Reporter receives events. Implementations must be safe for concurrent use.
type Reporter interface{ Event(Event) }

type ReporterFunc func(Event)

func (f ReporterFunc) Event(e Event) { f(e) }

// Options configure a Runner.
type Options struct {
	Env             []flow.KV     // -e values and injected TB_ variables, in order
	LookupTimeout   time.Duration // default 17 s
	OptionalTimeout time.Duration // default 7 s
	ArtifactsDir    string        // where takeScreenshot/startRecording write ("" = cwd)
	Reporter        Reporter
	Role            string // device role in a multi-device flow
	Signals         *Signals
	JS              *js.Engine // shared engine (multi-device); nil = a new one per flow
	Trace           *Trace     // the run's step-by-step log (trace.go); nil = none
	// Timeline records the run for its HTML timeline (timeline.go): each command, the
	// screen after it and its gestures. nil = none (no screenshots are taken).
	Timeline *timeline.Recorder
	// Record records the screen from the flow's start to its end into ArtifactsDir
	// (recording.mp4; recording.go).
	Record bool
	// StallFloor is the shortest device stall a failed step names (stall.go); 0 = 10 s.
	StallFloor time.Duration
	// AwayGrace is how long a lookup waits on SpringBoard alone in front of the flow's app
	// before it fails (away.go); 0 = 5 s, 10 s in CI.
	AwayGrace time.Duration
}

// Runner runs commands on one device.
type Runner struct {
	// scrollStop, when set, is asked by scrollTo after each read: a reason to stop scrolling
	// (the screen is no longer the one it began on, see bringBack), or "".
	scrollStop func(*ui.Screen) string
	Drv        driver.Driver
	opts       Options

	js       *js.Engine
	envStack []map[string]string
	copied   *string
	appID    string          // top-level flow appId (evaluated)
	hinted   map[string]bool // hints already reported in this flow (Runner.hint)
	hints    []string        // hints given since the last top-level step took them (Runner.hint)

	previousGuardedTap *ui.Screen           // tree before a probed iOS tap witnessed on the named button
	previousClick      bool                 // the probe also witnessed the control action
	inputTarget        *driver.ActionTarget // editable field tapped by the preceding command
	lastInteraction    time.Time
	// step is the running step's start and its CI-mode stall allowance (stall.go, issue #243);
	// nil between steps.
	step *stepClock
	// unsettled: a tap that changed the UI returned once the change began; the next command
	// that needs a still screen first waits this long at most for it to settle.
	unsettled time.Duration
	fresh     bool // reads skip the device's own cache (checking for motion)
	// held: the last tap was in a cover drawn over other elements, and this is the screen
	// just before it; the next tap checks that the cover, once out of the tree, is not still
	// drawn over its target (cover.go).
	held *heldCover

	rec      runRecording // the run's recording (recording.go)
	shotSeq  int64        // the device's change counter at the timeline's last screenshot
	shotDone bool         // a timeline screenshot was taken

	notifications string          // how to answer iOS's notifications alert (allow/deny), from launchApp
	dialogsSeen   map[string]bool // Android error dialogs already answered once (dialogs.go)

	mu         sync.Mutex
	screen     *ui.Screen       // last read
	screenOK   bool             // no action since it was read
	screenAt   time.Time        // when screen was read from the device
	screenTook time.Duration    // how long that read took
	history    []screenSnapshot // bounded, owned UI snapshots for target recovery

	recording    bool
	depth        int
	warnedSilent bool

	// frontApp is the last app other than SpringBoard a read showed in front; leftApp, that the
	// flow itself left it since (away.go).
	frontApp string
	leftApp  bool
}

func New(drv driver.Driver, opts Options) *Runner {
	if opts.LookupTimeout == 0 {
		opts.LookupTimeout = 17 * time.Second
	}
	if opts.OptionalTimeout == 0 {
		opts.OptionalTimeout = 7 * time.Second
	}
	if opts.Reporter == nil {
		opts.Reporter = ReporterFunc(func(Event) {})
	}
	return &Runner{Drv: drv, opts: opts, lastInteraction: time.Now()}
}

// ---- errors ----

// FlowError is a test failure an optional command downgrades to a warning
// (element not found, assertion false, ...).
type FlowError struct {
	Msg          string
	stallChecked bool // nameStall has decided for it, at the step that failed (issue #237)
}

func (e *FlowError) Error() string { return e.Msg }

func failf(format string, args ...any) error { return &FlowError{Msg: fmt.Sprintf(format, args...)} }

// NotFound is an element lookup that timed out; it carries what was on screen.
type NotFound struct {
	FlowError
	Screen *ui.Screen
	Query  string
	left   bool // bringBack stopped: the screen changed while it scrolled (issue #141)
	// away: SpringBoard, not the flow's app, was in front ("the app switcher
	// (com.apple.springboard) is in front, not ..."); nameAway puts it first in the step's
	// message, once (issue #142).
	away string
}

func isFlowError(err error) bool {
	var me *FlowError
	var nf *NotFound
	return errors.As(err, &me) || errors.As(err, &nf)
}

// ---- env & JS ----

func (r *Runner) jsEngine() *js.Engine {
	if r.js == nil {
		if r.opts.JS != nil {
			r.js = r.opts.JS
		} else {
			r.js = js.New(r.Drv.Platform())
		}
	}
	return r.js
}

func (r *Runner) env() map[string]string {
	if len(r.envStack) == 0 {
		return map[string]string{}
	}
	return r.envStack[len(r.envStack)-1]
}

func (r *Runner) scope() *js.Scope {
	// console.log lines go to this runner's device: a multi-device flow's devices share one
	// engine and may run scripts at the same time.
	return &js.Scope{Env: r.env(), Platform: r.Drv.Platform(), CopiedText: r.copied, Log: r.logLine}
}

// hint says that a step did nothing as written, and what to do instead: in the trace and in
// the top-level step's result (StepResult.Hints: tb do's output) each time, and to the
// Reporter (the console) once per flow, so a flow with many such steps does not repeat it
// there.
func (r *Runner) hint(s string) {
	r.tracef("hint: %s", s)
	r.hints = append(r.hints, s)
	if r.hinted[s] {
		return
	}
	if r.hinted == nil {
		r.hinted = map[string]bool{}
	}
	r.hinted[s] = true
	r.opts.Reporter.Event(Event{Device: r.opts.Role, Hint: s, Depth: r.depth})
}

// takeHints returns the hints given since the last call, for a top-level step's result.
func (r *Runner) takeHints() []string {
	h := r.hints
	r.hints = nil
	return h
}

func (r *Runner) logLine(s string) {
	r.opts.Reporter.Event(Event{Device: r.opts.Role, Log: s})
	r.tracef("log: %s", s)
}

// statusMark is a status's mark in the trace, as on the console.
func statusMark(s Status) string {
	switch s {
	case Passed:
		return "✓"
	case Failed:
		return "✗"
	case Warned:
		return "⚠"
	case Skipped:
		return "○"
	}
	return string(s)
}

// cmdAt is where a command is written, for the trace: " (file.yaml:12)".
func cmdAt(c *flow.Command) string {
	if c.File == "" || c.Line == 0 {
		return ""
	}
	return fmt.Sprintf("  (%s:%d)", filepath.Base(c.File), c.Line)
}

func (r *Runner) pushEnv() {
	cp := map[string]string{}
	for k, v := range r.env() {
		cp[k] = v
	}
	r.envStack = append(r.envStack, cp)
}

func (r *Runner) popEnv() {
	if len(r.envStack) > 0 {
		r.envStack = r.envStack[:len(r.envStack)-1]
	}
}

// define evaluates every value first, then assigns them all (siblings don't see each other).
func (r *Runner) define(kvs []flow.KV) error {
	vals := make([]string, len(kvs))
	for i, kv := range kvs {
		v, err := r.eval(kv.Value)
		if err != nil {
			return err
		}
		vals[i] = v
	}
	if len(r.envStack) == 0 {
		r.envStack = append(r.envStack, map[string]string{})
	}
	env := r.envStack[len(r.envStack)-1]
	for i, kv := range kvs {
		env[kv.Key] = vals[i]
	}
	return nil
}

// eval interpolates ${...} in s.
func (r *Runner) eval(s string) (string, error) {
	if !strings.Contains(s, "${") {
		return s, nil
	}
	return r.jsEngine().Interpolate(s, r.scope())
}

// ---- running ----

// FlowResult is the outcome of one flow.
type FlowResult struct {
	Name     string
	File     string
	Status   Status
	Err      error
	Duration time.Duration
	Steps    []StepResult
	Videos   []Video // the screen recordings of the run (Options.Record)
}

// StepResult is one top-level command's outcome.
type StepResult struct {
	Desc     string
	Status   Status
	Err      string
	Duration time.Duration
	Hints    []string // what the step (or a step inside it) did not do as written (Runner.hint)
}

// RunFlow runs a flow file from the start: env, onFlowStart, commands,
// onFlowComplete.
func (r *Runner) RunFlow(ctx context.Context, f *flow.Flow) (res FlowResult) {
	start := time.Now()
	res = FlowResult{Name: f.Name(), File: f.Path}
	r.js = nil
	r.envStack = nil
	r.copied = nil
	r.hinted, r.hints = nil, nil
	r.lastInteraction = time.Now()
	r.invalidate()
	r.resetFront()
	r.resetNetworkMocks()
	defer r.resetNetworkMocks()
	r.resetDialogs()
	defer r.resetDialogs()
	r.timelineStart(ctx)
	r.rec = runRecording{}
	r.recordStart(ctx)
	// The recordings stop on every way out of the flow, a crash (a panic) too, and their
	// videos are the result's; a crash's result is runSafely's, which reads them from the
	// runner (issue #107). Each stop is done once: both clear their flag before stopping.
	// Each has its own defer, so a stop that crashes does not skip the other; the run's
	// recording stops last, after the flow's.
	defer func() {
		res.Videos = r.Videos()
		res.Duration = time.Since(start)
	}()
	defer r.recordStop()
	defer r.stopFlowRecording(ctx)

	r.pushEnv()
	err := r.define(r.opts.Env)
	if err == nil {
		err = r.define(f.Config.Env)
	}
	if err == nil {
		r.appID, err = r.eval(f.Config.AppID)
	}
	if d, ok := r.Drv.(interface{ SetWebViewDevTools(string) }); ok && err == nil && f.Config.AndroidWebViewDevTools() {
		d.SetWebViewDevTools(r.appID)
		defer d.SetWebViewDevTools("")
	}
	if err == nil && len(f.Config.OnFlowStart) > 0 {
		err = r.runList(ctx, f.Config.OnFlowStart, f, nil)
	}
	if err == nil {
		err = r.runList(ctx, f.Commands, f, &res.Steps)
	}
	if len(f.Config.OnFlowComplete) > 0 && ctx.Err() == nil {
		if cerr := r.runList(ctx, f.Config.OnFlowComplete, f, nil); err == nil {
			err = cerr
		}
	}
	res.Err = err
	if err != nil {
		res.Status = Failed
	} else {
		res.Status = Passed
	}
	return res
}

// Begin prepares a flow run without running commands (multi-device flows drive each
// device's command lists themselves). extraEnv (a role's env) comes after the CLI env and
// the flow's, so it wins.
func (r *Runner) Begin(f *flow.Flow, extraEnv []flow.KV) error {
	r.envStack = nil
	r.copied = nil
	r.hinted, r.hints = nil, nil
	r.lastInteraction = time.Now()
	r.invalidate()
	r.resetFront()
	r.resetNetworkMocks()
	r.resetDialogs()
	r.timelineStart(context.Background())
	r.rec = runRecording{}
	r.recordStart(context.Background())
	r.pushEnv()
	if err := r.define(r.opts.Env); err != nil {
		return err
	}
	if err := r.define(f.Config.Env); err != nil {
		return err
	}
	if err := r.define(extraEnv); err != nil {
		return err
	}
	var err error
	r.appID, err = r.eval(f.Config.AppID)
	return err
}

// SetEnv defines variables for later commands (agents' -e values).
func (r *Runner) SetEnv(kvs []flow.KV) error {
	if len(kvs) == 0 {
		return nil
	}
	if len(r.envStack) == 0 {
		r.pushEnv()
	}
	return r.define(kvs)
}

// SetAppID sets the default appId (a device role's appId in a multi-device flow).
func (r *Runner) SetAppID(id string) { r.appID = id }

// RunList runs commands in the current flow context, stopping at the first failure.
func (r *Runner) RunList(ctx context.Context, cmds []*flow.Command, f *flow.Flow) error {
	return r.runList(ctx, cmds, f, nil)
}

// End stops recordings left running (the flow's, then the run's: Videos) and drops the
// flow's network mocks and dialog answer.
func (r *Runner) End() {
	// each in its own defer: one that crashes does not skip the others (issue #107)
	defer r.recordStop()
	defer r.stopFlowRecording(context.Background())
	defer r.resetDialogs()
	r.resetNetworkMocks()
}

// RunCommands runs loose commands (an agent's `tb do`) with the flow context of appID.
func (r *Runner) RunCommands(ctx context.Context, cmds []*flow.Command, appID string, onStep func(StepResult)) error {
	if len(r.envStack) == 0 {
		r.pushEnv()
		r.define(r.opts.Env)
	}
	r.appID = appID
	// An agent's think time between commands is not the app's: lookups count from now.
	r.lastInteraction = time.Now()
	// The app in front is the batch's to see (a person may have pressed home in between).
	r.resetFront()
	f := &flow.Flow{Config: flow.Config{AppID: appID}}
	for i, c := range cmds {
		start := time.Now()
		r.takeHints()
		status, err := r.runCommand(tapAssertionContext(ctx, cmds, i), c, f)
		hints := r.takeHints()
		if onStep != nil {
			sr := StepResult{Desc: r.describe(c), Status: status, Duration: time.Since(start), Hints: hints}
			if err != nil {
				sr.Err = err.Error()
			}
			onStep(sr)
		}
		if status == Failed {
			return err
		}
	}
	return nil
}

func (r *Runner) runList(ctx context.Context, cmds []*flow.Command, f *flow.Flow, steps *[]StepResult) error {
	for i, c := range cmds {
		if err := ctx.Err(); err != nil {
			return err
		}
		start := time.Now()
		if steps != nil {
			r.takeHints()
		}
		status, err := r.runCommand(tapAssertionContext(ctx, cmds, i), c, f)
		if steps != nil {
			sr := StepResult{Desc: r.describe(c), Status: status, Duration: time.Since(start), Hints: r.takeHints()}
			if err != nil {
				sr.Err = err.Error()
			}
			*steps = append(*steps, sr)
		}
		if status == Failed {
			return err
		}
	}
	return nil
}

// runCommand runs one command and reports it. It returns the command's status, and the
// error for a failure (or the warning's cause).
func (r *Runner) runCommand(ctx context.Context, c *flow.Command, f *flow.Flow) (Status, error) {
	desc := r.describe(c)
	r.opts.Reporter.Event(Event{Device: r.opts.Role, Cmd: c, Desc: desc, Depth: r.depth, Start: true})
	step := r.opts.Timeline.Begin(r.opts.Role, r.depth, c.Name, desc, c.File, c.Line)
	r.opts.Trace.command(r.opts.Role, r.depth, "▶ %s%s", desc, cmdAt(c))
	start := time.Now()
	if r.unsettled > 0 && !worksWhileUnsettled[c.Name] {
		t := time.Now()
		r.Drv.WaitSettled(ctx, r.unsettled)
		r.unsettled = 0
		if time.Since(t) >= 50*time.Millisecond {
			r.opts.Trace.Printf(r.opts.Role, r.depth+1, "waited %s for the last action's change to settle", since(t))
		}
	}
	r.depth++
	outer := r.step
	r.step = &stepClock{start: start}
	status, err := r.exec(ctx, c, f)
	r.traceNotes()
	if leavesApp(c) {
		r.leftApp = true
	}
	err = nameAway(err)
	err = r.nameStall(c, err, start)
	r.step = outer
	r.depth--
	if err != nil && status == "" {
		status = Failed
		var inv *invalidField
		if (c.Optional || selOptional(c)) && isFlowError(err) && !errors.As(err, &inv) {
			status = Warned
		}
	}
	if status == "" {
		status = Passed
	}
	if c.Mutating() && status != Skipped {
		r.lastInteraction = time.Now()
	}
	final := r.evaluatedDesc(c, desc)
	r.timelineEnd(ctx, step, c, final, status, err)
	r.opts.Reporter.Event(Event{Device: r.opts.Role, Cmd: c, Desc: final, Depth: r.depth, Status: status, Err: err, Elapsed: time.Since(start)})
	if err != nil {
		r.opts.Trace.command(r.opts.Role, r.depth, "%s %s  %s: %v", statusMark(status), final, since(start), err)
	} else {
		r.opts.Trace.command(r.opts.Role, r.depth, "%s %s  %s", statusMark(status), final, since(start))
	}
	if status == Failed && err == nil {
		err = errors.New("failed")
	}
	return status, err
}

// worksWhileUnsettled: commands that may start while the UI is still reacting to a tap. An
// assertion looks for its element as the screen comes in (it waits on change events), and a
// tap waits for a still screen itself before it acts (findStable; a tap at a point, in tap).
// Every other command first waits for the UI to settle, as after a classic tap.
var worksWhileUnsettled = map[string]bool{
	"assertVisible": true, "assertNotVisible": true, "extendedWaitUntil": true,
	"tapOn": true, "doubleTapOn": true, "longPressOn": true,
}

// evaluatedDesc shows ${...} values in a finished command's description.
// Commands whose scripts have side effects are left as written (evaluating twice would
// run them twice).
func (r *Runner) evaluatedDesc(c *flow.Command, desc string) string {
	if !strings.Contains(desc, "${") || c.Label != "" {
		return desc
	}
	switch c.Name {
	case "evalScript", "runScript", "assertTrue", "repeat", "retry", "runFlow", "extendedWaitUntil":
		return desc
	}
	if v, err := r.jsEngine().Interpolate(desc, r.scope()); err == nil {
		return v
	}
	return desc
}

func selOptional(c *flow.Command) bool {
	if c.Selector != nil && c.Selector.Optional {
		return true
	}
	return c.From != nil && c.From.Optional
}

type invalidField struct{ msg string }

func (e *invalidField) Error() string { return e.msg }

// ---- screen cache ----

// invalidate forgets the last read: an action may have changed the screen.
func (r *Runner) invalidate() {
	r.mu.Lock()
	r.screenOK = false
	r.mu.Unlock()
}

// read returns the screen, reusing the last read while the device reports no change.
func (r *Runner) read(ctx context.Context) (*ui.Screen, error) {
	r.mu.Lock()
	s, ok := r.screen, r.screenOK
	r.mu.Unlock()
	if ok && s != nil && s.Seq == r.Drv.Seq() {
		return s, nil
	}
	var err error
	readAt := time.Now()
	if fr, ok := r.Drv.(freshReader); ok && r.fresh {
		s, err = fr.ScreenFresh(ctx)
	} else {
		s, err = r.Drv.Screen(ctx)
	}
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.rememberScreen(s)
	r.screen, r.screenOK, r.screenAt = s, true, time.Now()
	r.screenTook = r.screenAt.Sub(readAt)
	r.mu.Unlock()
	r.noteFront(s)
	return s, nil
}

// screenAge is how long ago the screen in hand was read from the device.
func (r *Runner) screenAge() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return time.Since(r.screenAt)
}

// freshReader: a driver whose reads may come from a cache that misses some motion (Android's
// node cache misses views moved by translation) reads past it on request.
type freshReader interface {
	ScreenFresh(ctx context.Context) (*ui.Screen, error)
}

// LastScreen is the most recent read (for failure reports).
func (r *Runner) LastScreen() *ui.Screen {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.screen
}

func (r *Runner) artifactPath(sub, path, ext string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || strings.HasSuffix(path, "/") || strings.HasSuffix(path, ".") {
		return "", failf("invalid path %q", path)
	}
	path += ext
	if r.opts.ArtifactsDir != "" && !filepath.IsAbs(path) {
		base := filepath.Join(r.opts.ArtifactsDir, sub)
		full := filepath.Join(base, path)
		if !strings.HasPrefix(full, base+string(filepath.Separator)) {
			return "", failf("path %q escapes the artifacts folder", path)
		}
		path = full
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, nil
}
