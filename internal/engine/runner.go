// Package engine runs flows on a device, around device events.
//
// Waiting never polls the device. An element lookup reads the screen once; if the element
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
}

// Runner runs commands on one device.
type Runner struct {
	Drv  driver.Driver
	opts Options

	js       *js.Engine
	envStack []map[string]string
	copied   *string
	appID    string // top-level flow appId (evaluated)

	lastInteraction time.Time
	// unsettled: a tap that changed the UI returned once the change began; the next command
	// that needs a still screen first waits this long at most for it to settle.
	unsettled time.Duration

	notifications string          // how to answer iOS's notifications alert (allow/deny), from launchApp
	dialogsSeen   map[string]bool // Android error dialogs already answered once (dialogs.go)

	mu       sync.Mutex
	screen   *ui.Screen // last read
	screenOK bool       // no action since it was read

	recording    bool
	depth        int
	warnedSilent bool
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
type FlowError struct{ Msg string }

func (e *FlowError) Error() string { return e.Msg }

func failf(format string, args ...any) error { return &FlowError{Msg: fmt.Sprintf(format, args...)} }

// NotFound is an element lookup that timed out; it carries what was on screen.
type NotFound struct {
	FlowError
	Screen *ui.Screen
	Query  string
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
		r.js.Log = func(s string) { r.opts.Reporter.Event(Event{Device: r.opts.Role, Log: s}) }
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
	return &js.Scope{Env: r.env(), Platform: r.Drv.Platform(), CopiedText: r.copied}
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
}

// StepResult is one top-level command's outcome.
type StepResult struct {
	Desc     string
	Status   Status
	Err      string
	Duration time.Duration
}

// RunFlow runs a flow file from the start: env, onFlowStart, commands,
// onFlowComplete.
func (r *Runner) RunFlow(ctx context.Context, f *flow.Flow) (res FlowResult) {
	start := time.Now()
	res = FlowResult{Name: f.Name(), File: f.Path}
	r.js = nil
	r.envStack = nil
	r.copied = nil
	r.lastInteraction = time.Now()
	r.invalidate()

	r.pushEnv()
	err := r.define(r.opts.Env)
	if err == nil {
		err = r.define(f.Config.Env)
	}
	if err == nil {
		r.appID, err = r.eval(f.Config.AppID)
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
	if r.recording {
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		r.Drv.StopRecording(c)
		cancel()
		r.recording = false
	}
	res.Duration = time.Since(start)
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
	r.lastInteraction = time.Now()
	r.invalidate()
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

// End stops a recording left running.
func (r *Runner) End() {
	if r.recording {
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		r.Drv.StopRecording(c)
		cancel()
		r.recording = false
	}
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
	f := &flow.Flow{Config: flow.Config{AppID: appID}}
	for _, c := range cmds {
		start := time.Now()
		status, err := r.runCommand(ctx, c, f)
		if onStep != nil {
			sr := StepResult{Desc: r.describe(c), Status: status, Duration: time.Since(start)}
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
	for _, c := range cmds {
		if err := ctx.Err(); err != nil {
			return err
		}
		start := time.Now()
		status, err := r.runCommand(ctx, c, f)
		if steps != nil {
			sr := StepResult{Desc: r.describe(c), Status: status, Duration: time.Since(start)}
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
	start := time.Now()
	if r.unsettled > 0 && !worksWhileUnsettled[c.Name] {
		r.Drv.WaitSettled(ctx, r.unsettled)
		r.unsettled = 0
	}
	r.depth++
	status, err := r.exec(ctx, c, f)
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
	r.opts.Reporter.Event(Event{Device: r.opts.Role, Cmd: c, Desc: r.evaluatedDesc(c, desc), Depth: r.depth, Status: status, Err: err, Elapsed: time.Since(start)})
	if status == Failed && err == nil {
		err = errors.New("failed")
	}
	return status, err
}

// worksWhileUnsettled: commands that may start while the UI is still reacting to a tap. An
// assertion looks for its element as the screen comes in (it waits on change events), and a
// tap waits for a still screen itself before it acts (findStable). Every other command first
// waits for the UI to settle, as after a classic tap.
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
	s, err := r.Drv.Screen(ctx)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.screen, r.screenOK = s, true
	r.mu.Unlock()
	return s, nil
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
