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

	"github.com/elforama/testbuddy/internal/annotate"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/js"
	"github.com/elforama/testbuddy/internal/timeline"
	"github.com/elforama/testbuddy/internal/ui"
	"github.com/elforama/testbuddy/internal/wire"
)

type Status string

const (
	Passed  Status = "passed"
	Warned  Status = "warned"
	Skipped Status = "skipped"
	Failed  Status = "failed"
)

type Event struct {
	Device  string
	Cmd     *flow.Command
	Desc    string
	Depth   int
	Start   bool
	Status  Status
	Err     error
	Elapsed time.Duration
	Log     string
	Hint    string
}

type Reporter interface{ Event(Event) }

type ReporterFunc func(Event)

func (f ReporterFunc) Event(e Event) { f(e) }

type Options struct {
	FromStep        int
	Env             []flow.KV
	LookupTimeout   time.Duration
	OptionalTimeout time.Duration
	ArtifactsDir    string
	Reporter        Reporter
	Role            string
	Signals         *Signals
	JS              *js.Engine
	Trace           *Trace
	Timeline        *timeline.Recorder
	Record          bool
	Annotate        bool
	AnnotateEvents  func(role string, since, start, end time.Time) []AnnotateEvent
	AnnotateStep    time.Duration
	StallFloor      time.Duration
	AwayGrace       time.Duration
}

type Runner struct {
	flowBegan  time.Time
	scrollStop func(*ui.Screen) string
	Drv        driver.Driver
	opts       Options

	js         *js.Engine
	envStack   []map[string]string
	copied     *string
	appID      string
	hinted     map[string]bool
	hints      []string
	dialogMark int

	previousGuardedTap *ui.Screen
	previousClick      bool
	inputTarget        *driver.ActionTarget
	lastInteraction    time.Time
	step               *stepClock
	unsettled          time.Duration
	fresh              bool
	firstRead          bool
	stepsDone          int
	freshAfter         int
	screenFresh        bool
	held               *heldCover

	rec             runRecording
	lastTouch       time.Time
	annotateWait    func(context.Context, time.Duration)
	annotateBurn    func(ctx context.Context, path string, d annotate.Drawing) error
	annotateChanges func(ctx context.Context, path string) ([]time.Duration, error)
	annotateMarks   func(ctx context.Context, path string) ([]time.Duration, error)
	shotSeq         int64
	shotDone        bool

	notifications string
	dialogsSeen   map[string]bool

	mu         sync.Mutex
	screen     *ui.Screen
	screenOK   bool
	screenAt   time.Time
	screenTook time.Duration
	history    []screenSnapshot
	seen       *ui.Screen
	ctxKey     string
	ctxReady   bool
	ctxWin     int
	ctxRebuilt bool

	recording    bool
	depth        int
	warnedSilent bool

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
	r := &Runner{Drv: drv, opts: opts, lastInteraction: time.Now()}
	r.markDialogs()
	return r
}

type FlowError struct {
	Msg          string
	stallChecked bool
	assertion    bool
}

func (e *FlowError) Error() string { return e.Msg }

func failf(format string, args ...any) error { return &FlowError{Msg: fmt.Sprintf(format, args...)} }

func assertionFalsef(format string, args ...any) error {
	return &FlowError{Msg: "Assertion is false: " + fmt.Sprintf(format, args...), assertion: true}
}

type NotFound struct {
	FlowError
	Screen *ui.Screen
	Query  string
	ID     string
	left   bool
	away   string
	moved  bool
	blind  bool
}

const (
	ReasonNotFound        = "not_found"
	ReasonScreenChanged   = "screen_changed"
	ReasonAssertionFailed = "assertion_failed"
	ReasonOther           = "other"
)

func Reason(err error) string {
	var nf *NotFound
	var fe *FlowError
	switch {
	case errors.As(err, &nf):
		switch {
		case nf.moved || nf.left:
			return ReasonScreenChanged
		case nf.assertion:
			return ReasonAssertionFailed
		case nf.blind:
			return ReasonOther
		}
		return ReasonNotFound
	case errors.As(err, &fe) && fe.assertion:
		return ReasonAssertionFailed
	}
	return ReasonOther
}

func isFlowError(err error) bool {
	var me *FlowError
	var nf *NotFound
	return errors.As(err, &me) || errors.As(err, &nf)
}

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
	return &js.Scope{Env: r.env(), Platform: r.Drv.Platform(), CopiedText: r.copied, Log: r.logLine}
}

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

func (r *Runner) takeHints() []string {
	h := r.hints
	r.hints = nil
	return h
}

func (r *Runner) logLine(s string) {
	r.opts.Reporter.Event(Event{Device: r.opts.Role, Log: s})
	r.tracef("log: %s", s)
}

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

func (r *Runner) eval(s string) (string, error) {
	if !strings.Contains(s, "${") {
		return s, nil
	}
	return r.jsEngine().Interpolate(s, r.scope())
}

type FlowResult struct {
	Name     string
	File     string
	Status   Status
	Err      error
	Duration time.Duration
	Steps    []StepResult
	Videos   []Video
}

type StepResult struct {
	Desc     string
	Status   Status
	Err      string
	Duration time.Duration
	Hints    []string
	Reason   string
}

func (r *Runner) RunFlow(ctx context.Context, f *flow.Flow) (res FlowResult) {
	start := time.Now()
	r.flowBegan = start
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
	r.markDialogs()
	r.timelineStart(ctx)
	r.rec = runRecording{}
	r.recordStart(ctx)
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
	if err == nil && len(f.Config.OnFlowStart) > 0 && r.opts.FromStep <= 1 {
		err = r.runList(ctx, f.Config.OnFlowStart, f, nil)
	}
	cmds := f.Commands
	if n := r.opts.FromStep; err == nil && n > 1 && n <= len(cmds) {
		left := fmt.Sprintf("steps 1 to %d", n-1)
		if len(f.Config.OnFlowStart) > 0 {
			left = "onFlowStart and " + left
		}
		r.opts.Trace.Printf(r.opts.Role, 0, "--from %d: %s not run", n, left)
		for _, c := range cmds[:n-1] {
			desc := r.describe(c)
			r.opts.Reporter.Event(Event{Device: r.opts.Role, Cmd: c, Desc: desc + " (before --from)", Status: Skipped})
			res.Steps = append(res.Steps, StepResult{Desc: desc, Status: Skipped})
		}
		cmds = cmds[n-1:]
	}
	if err == nil {
		err = r.runList(ctx, cmds, f, &res.Steps)
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

func (r *Runner) Begin(f *flow.Flow, extraEnv []flow.KV) error {
	r.flowBegan = time.Now()
	r.envStack = nil
	r.copied = nil
	r.hinted, r.hints = nil, nil
	r.lastInteraction = time.Now()
	r.invalidate()
	r.resetFront()
	r.resetNetworkMocks()
	r.resetDialogs()
	r.markDialogs()
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

func (r *Runner) SetEnv(kvs []flow.KV) error {
	if len(kvs) == 0 {
		return nil
	}
	if len(r.envStack) == 0 {
		r.pushEnv()
	}
	return r.define(kvs)
}

func (r *Runner) ScopeEnv(batches ...[]flow.KV) (func(), error) {
	if len(r.envStack) == 0 {
		r.pushEnv()
		r.define(r.opts.Env)
	}
	r.pushEnv()
	for _, kvs := range batches {
		if err := r.define(kvs); err != nil {
			r.popEnv()
			return nil, err
		}
	}
	return r.popEnv, nil
}

func (r *Runner) Eval(s string) (string, error) { return r.eval(s) }

func (r *Runner) SetAppID(id string) { r.appID = id }

func (r *Runner) RunList(ctx context.Context, cmds []*flow.Command, f *flow.Flow) error {
	return r.runList(ctx, cmds, f, nil)
}

func (r *Runner) End() {
	defer r.recordStop()
	defer r.stopFlowRecording(context.Background())
	defer r.resetDialogs()
	r.resetNetworkMocks()
}

func (r *Runner) RunCommands(ctx context.Context, cmds []*flow.Command, appID string, onStep func(StepResult)) error {
	if len(r.envStack) == 0 {
		r.pushEnv()
		r.define(r.opts.Env)
	}
	r.appID = appID
	r.lastInteraction = time.Now()
	r.resetFront()
	f := &flow.Flow{Config: flow.Config{AppID: appID}}
	defer func() { r.seen = nil }()
	for i, c := range cmds {
		if i > 0 || !tapsByLookup(c.Name) {
			r.seen = nil
		}
		start := time.Now()
		r.takeHints()
		status, err := r.runCommand(tapAssertionContext(ctx, cmds, i), c, f)
		hints := r.takeHints()
		if onStep != nil {
			sr := StepResult{Desc: r.describe(c), Status: status, Duration: time.Since(start), Hints: hints}
			if err != nil {
				sr.Err = err.Error()
			}
			if status == Failed {
				sr.Reason = Reason(err)
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
	if actsForDialogs(c) {
		r.markDialogs()
	}
	outer := r.step
	r.step = &stepClock{start: start}
	pending := r.firstRead
	r.firstRead = false
	status, err := r.exec(ctx, c, f)
	r.firstRead = pending
	r.stepsDone++
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
	r.holdForViewer(ctx, c, status, start)
	r.noteStep(c, final, status, err, start)
	if status == Failed && err == nil {
		err = errors.New("failed")
	}
	return status, err
}

var worksWhileUnsettled = map[string]bool{
	"assertVisible": true, "assertNotVisible": true, "extendedWaitUntil": true,
	"tapOn": true, "doubleTapOn": true, "longPressOn": true,
}

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
	return c.From != nil && c.From.Optional || c.To != nil && c.To.Optional
}

type invalidField struct{ msg string }

func (e *invalidField) Error() string { return e.msg }

func (r *Runner) invalidate() {
	r.mu.Lock()
	r.screenOK = false
	r.mu.Unlock()
}

func (r *Runner) read(ctx context.Context) (*ui.Screen, error) {
	fr, past := r.Drv.(freshReader)
	wasFirst := r.firstRead
	first := wasFirst && past
	r.firstRead = false
	r.mu.Lock()
	s, ok := r.screen, r.screenOK
	servesFirst := r.screenFresh && r.freshAfter == r.stepsDone
	r.mu.Unlock()
	if ok && s != nil && s.Seq == r.Drv.Seq() && (!first || servesFirst) {
		return s, nil
	}
	var err error
	readAt := time.Now()
	fresh := past && (r.fresh || first)
	if fresh {
		s, err = fr.ScreenFresh(ctx)
	} else {
		s, err = r.Drv.Screen(ctx)
	}
	if err != nil {
		r.firstRead = wasFirst
		return nil, err
	}
	r.mu.Lock()
	r.rememberScreen(s)
	r.screen, r.screenOK, r.screenAt = s, true, time.Now()
	r.screenFresh, r.freshAfter = fresh, r.stepsDone
	r.screenTook = r.screenAt.Sub(readAt)
	r.mu.Unlock()
	r.noteFront(s)
	return s, nil
}

const readRetryPause = 100 * time.Millisecond

func (r *Runner) readUntil(ctx context.Context, deadline time.Time) (*ui.Screen, error) {
	for {
		s, err := r.read(ctx)
		var agent *wire.AgentError
		if err == nil || !errors.As(err, &agent) || ctx.Err() != nil || !time.Now().Add(readRetryPause).Before(deadline) {
			return s, err
		}
		r.tracef("screen read failed (%v): reading again", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(readRetryPause):
		}
	}
}

func (r *Runner) verdictRead() { r.firstRead = true }

func (r *Runner) screenAge() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return time.Since(r.screenAt)
}

func (r *Runner) reread(ctx context.Context) (*ui.Screen, error) {
	return r.rereadUntil(ctx, time.Time{})
}

func (r *Runner) rereadUntil(ctx context.Context, deadline time.Time) (*ui.Screen, error) {
	r.invalidate()
	was := r.fresh
	r.fresh = true
	defer func() { r.fresh = was }()
	return r.readUntil(ctx, deadline)
}

type freshReader interface {
	ScreenFresh(ctx context.Context) (*ui.Screen, error)
}

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
