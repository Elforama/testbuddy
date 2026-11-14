package orchestrate

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
)

// Job is one flow to run (pinned to a device for --shard-all).
type Job struct {
	Flow    *flow.Flow
	Needs   []Need
	Attempt int
	Seq     bool // part of the executionOrder sequence
	ShardID int
	// DebugName names the job's debug folder: set before the run, unique in it, and kept
	// by the job's retries ("" = the CLI names it from the flow).
	DebugName string
	avoid     string // device serial that failed it last time
	// moves are the devices found lost under this flow before it ran a step, each of which
	// sent it back to the queue for another device (issue #268): a flow tries a device once.
	moves []lostDevice
}

// lostDevice is a device found lost for the run as a job was about to start on it: its
// connection had died, and opening it again failed (Pool.driverForJob).
type lostDevice struct {
	role string
	dev  device.Info
	err  error // the pool's: why the connection was lost, and why opening it again failed
}

// note says, for the run's output and the flow's log, that the flow moved off d.
func (l lostDevice) note() string {
	return fmt.Sprintf("%s (%s): %v; the flow ran no step there and went back to the queue: moved to another device (not a retry)",
		l.dev.Name, l.dev.Serial, l.err)
}

// Result is one finished job.
type Result struct {
	Job      *Job
	Devices  map[string]device.Info // role ("" for single) -> device
	Status   engine.Status
	Err      error
	Duration time.Duration
	Start    time.Time
	Steps    []engine.StepResult
	Runner   *engine.Runner // single-device runs: for failure artifacts
	Videos   []engine.Video // screen recordings of the run (Options.Record)
	// lost: the attempt ran no step, as this device turned out lost for the run when it was
	// opened for it (issue #268). nil = the flow ran, or did not start for another reason.
	lost *lostDevice
}

// runSafely runs a job, turning a crash (a panic) into the job's failure: the run goes on,
// and ends as usual, releasing its devices. A panic in a job's goroutine would otherwise end
// the process at once, with every device's agent still running. run fills in the result as
// it goes: a crash's result keeps the videos of the recordings the run stopped on its way
// out, so the reports list them (issue #107).
func runSafely(run func(*Job, map[string]device.Info, *Result), j *Job, devs map[string]device.Info) (r Result) {
	start := time.Now()
	r = Result{Job: j, Devices: devs, Start: start}
	defer func() {
		if p := recover(); p != nil {
			r = Result{Job: j, Devices: devs, Status: engine.Failed, Start: start, Duration: time.Since(start),
				Err:    fmt.Errorf("tb crashed running this flow (please report it): %v\n%s", p, debug.Stack()),
				Videos: r.Videos}
		}
	}()
	run(j, devs, &r)
	return r
}

// HookCrash is a panic in a Started, Failure or Finished hook, as the flow's error (issue #117). A
// result whose error holds one with Hook "Finished" was not fully reported by its hook: the
// console line, the failure files or the log may be missing, and the caller can make up for
// it once Schedule returns (errors.As finds it).
type HookCrash struct {
	Hook  string // "Started", "Failure" or "Finished"
	Value any    // what the hook panicked with
	Stack []byte
}

func (c *HookCrash) Error() string {
	what := "starting"
	switch c.Hook {
	case "Finished":
		what = "reporting"
	case "Failure":
		what = "saving the failure of"
	}
	return fmt.Sprintf("tb crashed %s this flow (please report it): %v\n%s", what, c.Value, c.Stack)
}

// callHook runs a Started, Failure or Finished hook, turning a crash (a panic) into a *HookCrash: the
// CLI writes the console, the reports and the timeline there, and a panic escaping Schedule
// would end the run with no report and other flows still on their devices (issue #117).
func callHook(hook string, fn func()) (crash *HookCrash) {
	defer func() {
		if p := recover(); p != nil {
			crash = &HookCrash{Hook: hook, Value: p, Stack: debug.Stack()}
		}
	}()
	fn()
	return nil
}

// withCrash is a result whose report crashed: the flow failed, with its own error (if any)
// and the crash (both reachable with errors.Is and errors.As).
func withCrash(r Result, crash *HookCrash) Result {
	r.Status = engine.Failed
	if r.Err != nil {
		r.Err = fmt.Errorf("%w\n%w", r.Err, crash)
	} else {
		r.Err = crash
	}
	return r
}

// Why a flow did not run, or was not run again, once the run was cancelled (issue #189).
var (
	errStopped    = errors.New("skipped: the run was stopped before this flow ran")
	errNotRetried = errors.New("not retried: the run was stopped")
)

// Hooks let the CLI observe a run.
type Hooks struct {
	// Options builds engine options for a job on a device (env, reporter, artifacts).
	Options func(job *Job, devs map[string]device.Info) engine.Options
	// Started / Finished report jobs. A panic in either is contained: Started's fails the
	// job without running it; Finished's makes that result final (no retry follows) and
	// failed, naming the crash, in what Schedule returns.
	Started  func(job *Job, devs map[string]device.Info)
	Finished func(res Result)
	// Notice reports something the pool did on its own that the run's output must show: a
	// device whose connection was lost for good was opened again for the job. nil = none.
	Notice func(job *Job, d device.Info, msg string)
	// Prepare runs on a device before its first flow (e.g. disable animations), and again
	// before the next flow after the device was opened again (a new agent, maybe a device
	// that restarted: issue #101).
	Prepare func(ctx context.Context, d device.Info, drv driver.Driver)
	// Failure reads what a failed attempt left on its devices (a screenshot, the screen)
	// while the attempt still holds them, so no other flow runs there meanwhile (issue #101).
	// drivers maps each role to the driver the attempt had, or, if that one lost its
	// connection for good, to the device opened again (within a minute: driverForFailure); a
	// device the attempt never opened, or that could not be opened again, is left out. notes
	// says, by role, that the screen is read after a reopen, or why it cannot be read. It runs
	// before Finished, on the attempt's goroutine, and not once the run is cancelled. A panic
	// in it is contained: the attempt fails, naming it. nil = nothing.
	Failure func(r Result, drivers map[string]driver.Driver, notes map[string]string)
}

// Schedule runs jobs over the pool, at most maxParallel at a time (0 = one per device). A device that finishes a job takes the next one it can
// serve (a work queue, not a fixed split), so a slow device does not hold up the rest.
// Failed jobs are retried up to retries times, on another device when one is free.
// Sequence jobs run first, in order, one at a time, with the same retries and crash
// recovery.
func Schedule(ctx context.Context, pool *Pool, jobs []*Job, retries, maxParallel int, continueSeq bool, hooks Hooks) []Result {
	if maxParallel <= 0 {
		maxParallel = len(pool.Devices())
	}
	var results []Result
	var seq, rest []*Job
	for _, j := range jobs {
		if j.Seq {
			seq = append(seq, j)
		} else {
			rest = append(rest, j)
		}
	}
	prepared := map[string]bool{} // devices Prepare ran on since they were last opened
	var prepMu sync.Mutex
	// driverFor is the driver of d, which j holds: a device whose connection was lost for good
	// is opened again (the run's output is told), and Prepare runs again before its next flow.
	driverFor := func(j *Job, d device.Info) (driver.Driver, string, error) {
		drv, note, err := pool.driverForJob(ctx, d)
		if err == nil && note != "" {
			prepMu.Lock()
			delete(prepared, d.Serial)
			prepMu.Unlock()
			if hooks.Notice != nil {
				hooks.Notice(j, d, note)
			}
		}
		return drv, note, err
	}
	// capture runs Failure for a failed attempt, before its devices are released.
	capture := func(r *Result) {
		if hooks.Failure == nil || r.Status != engine.Failed || len(r.Devices) == 0 || ctx.Err() != nil || r.lost != nil {
			// (an attempt that met a lost device ran no step: its screens say nothing of it)
			return
		}
		drivers, notes := map[string]driver.Driver{}, map[string]string{}
		for role, d := range r.Devices {
			drv, note, err := pool.driverForFailure(ctx, d)
			switch {
			case err != nil:
				notes[role] = "the screen could not be read: " + err.Error()
			case drv == nil:
			default:
				drivers[role] = drv
				if note != "" {
					notes[role] = note + "; the screen was read after tb opened the device again"
					prepMu.Lock()
					delete(prepared, d.Serial) // a new agent: Prepare runs again on it
					prepMu.Unlock()
					if hooks.Notice != nil {
						hooks.Notice(r.Job, d, note)
					}
				}
			}
		}
		if crash := callHook("Failure", func() { hooks.Failure(*r, drivers, notes) }); crash != nil {
			*r = withCrash(*r, crash)
		}
	}
	// runOne runs a job into res (runSafely's: a crash's result keeps the videos, issue #107).
	runOne := func(j *Job, devs map[string]device.Info, res *Result) {
		drivers := map[string]driver.Driver{}
		notes := map[string]string{} // role -> the device was lost and opened again
		for role, d := range devs {
			drv, note, err := driverFor(j, d)
			if err != nil {
				res.Status, res.Err = engine.Failed, fmt.Errorf("%s: %w", d.Name, err)
				if ctx.Err() == nil && pool.isLost(d.Serial) {
					res.lost = &lostDevice{role: role, dev: d, err: err}
				}
				return
			}
			if note != "" {
				notes[role] = note
			}
			prepMu.Lock()
			first := !prepared[d.Serial]
			prepared[d.Serial] = true
			prepMu.Unlock()
			if first && hooks.Prepare != nil {
				hooks.Prepare(ctx, d, drv)
			}
			drivers[role] = drv
		}
		opts := hooks.Options(j, devs)
		for _, m := range j.moves {
			opts.Trace.Printf(m.role, 0, "%s", m.note())
		}
		for role, note := range notes {
			opts.Trace.Printf(role, 0, "%s", note)
		}
		if len(j.Flow.Config.Devices) > 0 {
			var mr MultiResult
			// runMultiInto has stopped the recordings when it returns, or when it crashes
			defer func() { res.Videos = mr.Videos }()
			runMultiInto(ctx, j.Flow, drivers, opts, &mr)
			res.Status, res.Err, res.Duration = mr.Status, mr.Err, mr.Duration
			return
		}
		r := engine.New(drivers[""], opts)
		// RunFlow has stopped the recordings when it returns, or when it crashes
		defer func() { res.Videos = r.Videos() }()
		fr := r.RunFlow(ctx, j.Flow)
		res.Status, res.Err, res.Duration, res.Steps, res.Runner = fr.Status, fr.Err, fr.Duration, fr.Steps, r
	}

	// start reports a job started; false (with the job's failed result) when the hook crashed.
	start := func(j *Job, devs map[string]device.Info) (Result, bool) {
		if crash := callHook("Started", func() { hooks.Started(j, devs) }); crash != nil {
			return Result{Job: j, Devices: devs, Status: engine.Failed, Err: crash, Start: time.Now()}, false
		}
		return Result{}, true
	}
	// finish reports a finished attempt, and returns it as Schedule's result: failed, naming
	// the crash, when the hook crashed (false then: the attempt is final, not retried).
	finish := func(r Result) (Result, bool) {
		if r.Status == engine.Failed && ctx.Err() != nil && (r.Err == nil || errors.Is(r.Err, ctx.Err())) {
			// "context canceled" alone does not say what happened (issue #117)
			r.Err = fmt.Errorf("cancelled: the run was stopped before this flow ran or finished (%w)", ctx.Err())
		}
		if crash := callHook("Finished", func() { hooks.Finished(r) }); crash != nil {
			return withCrash(r, crash), false
		}
		return r, true
	}

	// retryOf is the next attempt of a finished job, or nil when it is final: it passed, it
	// has had its retries, or the run was cancelled. The retry prefers another device.
	retryOf := func(r Result) *Job {
		if r.Status != engine.Failed || r.Job.Attempt >= retries || ctx.Err() != nil {
			return nil
		}
		retry := *r.Job
		retry.Attempt++
		for _, d := range r.Devices {
			retry.avoid = d.Serial
		}
		return &retry
	}

	// movedOf is the job again, back in the queue for another device, when its attempt ran no
	// step because a device turned out lost for the run (issue #268): it costs no retry, and
	// the run's output and the flow's log say so. nil when the attempt is the flow's result: no
	// device that fits is left (the error names the lost one, as before), or the run was
	// cancelled. A lost device serves nothing for the rest of the run (tryAlloc), so a flow
	// tries each device at most once, and moving ends; the check on moves keeps it so.
	movedOf := func(r Result) *Job {
		if r.lost == nil || ctx.Err() != nil || pool.satisfiable(r.Job.Needs) != nil {
			return nil
		}
		for _, m := range r.Job.moves {
			if m.dev.Serial == r.lost.dev.Serial {
				return nil
			}
		}
		next := *r.Job
		next.moves = append(append([]lostDevice(nil), r.Job.moves...), *r.lost)
		if hooks.Notice != nil {
			hooks.Notice(r.Job, r.lost.dev, r.lost.note())
		}
		return &next
	}

	// notRun is the result of a job the run's cancel kept from starting (issue #189): it is
	// not launched, so Started does not run for it and nothing opens its log, timeline or
	// failure files. A flow that never ran is skipped; Finished reports it, as it reports the
	// flows an ordered failure skips (a skipped result costs the CLI no device read). A retry
	// that never ran leaves the flow its last attempt, which did run and fail: Finished has
	// reported that one already, so it is not called again.
	notRun := func(j *Job, last *Result) Result {
		if last != nil {
			r := *last
			if r.Err != nil {
				r.Err = fmt.Errorf("%w\n%s", r.Err, errNotRetried)
			} else {
				r.Err = errNotRetried
			}
			return r
		}
		r, _ := finish(Result{Job: j, Status: engine.Skipped, Err: errStopped, Start: time.Now()})
		return r
	}

	// The ordered sequence: one at a time, on the first device that fits. A failed flow is
	// retried (as in the queue) before the sequence goes on, so only its last attempt
	// decides whether the sequence stops.
	for i, j := range seq {
		var r Result
		reported := false // Finished already ran for r (and crashed)
		var last *Result  // the attempt before this one, reported already (a retry)
		for {
			if ctx.Err() != nil {
				// the run was stopped: no further attempt starts (issue #189)
				r, reported = notRun(j, last), true
				break
			}
			if err := pool.satisfiable(j.Needs); err != nil {
				r = Result{Job: j, Status: engine.Failed, Err: err, Start: time.Now()}
				break
			}
			var devs map[string]device.Info
			if j.avoid != "" {
				// nothing else runs during the sequence: another device if one is free, else any
				devs = pool.tryAlloc(withAvoid(j.Needs, j.avoid, pool))
			}
			if devs == nil {
				var err error
				if devs, err = pool.allocOrWait(ctx, j.Needs); err != nil || devs == nil {
					if ctx.Err() != nil {
						// stopped while waiting for a device another process held (issue #189)
						r, reported = notRun(j, last), true
						break
					}
					if err == nil {
						err = fmt.Errorf("no device could run it")
					}
					r = Result{Job: j, Status: engine.Failed, Err: err, Start: time.Now()}
					break
				}
			}
			if ctx.Err() != nil {
				// stopped as the device was taken (the wait tries the device first): give it
				// back, unused (issue #189)
				pool.release(devs)
				r, reported = notRun(j, last), true
				break
			}
			if crashed, ok := start(j, devs); ok {
				r = runSafely(runOne, j, devs)
			} else {
				r = crashed
			}
			capture(&r)
			pool.release(devs)
			if moved := movedOf(r); moved != nil {
				j = moved // not reported: the flow's outcome is the moved attempt's
				continue
			}
			next := retryOf(r)
			if next == nil {
				break
			}
			// this attempt; the retry is reported again
			fr, ok := finish(r)
			if !ok {
				r, reported = fr, true
				break
			}
			last = &fr
			j = next
		}
		if !reported {
			r, _ = finish(r)
		}
		results = append(results, r)
		if ctx.Err() != nil || (r.Status == engine.Failed && !continueSeq) {
			// every flow after it is reported skipped: the rest of the sequence, then the
			// queue; none of them is launched once the run is stopped (issue #189)
			why := fmt.Errorf("skipped: an ordered flow failed")
			if ctx.Err() != nil {
				why = errStopped
			}
			for _, rj := range append(append([]*Job{}, seq[i+1:]...), rest...) {
				sr, _ := finish(Result{Job: rj, Status: engine.Skipped, Err: why, Start: time.Now()})
				results = append(results, sr)
			}
			return results
		}
	}

	// The queue. Multi-device jobs first: they need the most devices at once.
	sort.SliceStable(rest, func(a, b int) bool { return len(rest[a].Needs) > len(rest[b].Needs) })
	pending := rest
	done := make(chan Result)
	running := 0
	// launch runs a job on its devices; its result comes back on done. Started runs here, on
	// Schedule's goroutine, as Finished does.
	launch := func(j *Job, devs map[string]device.Info) {
		running++
		crashed, ok := start(j, devs)
		go func() {
			r := crashed
			if ok {
				r = runSafely(runOne, j, devs)
			}
			capture(&r) // the devices are released once r is back
			done <- r
		}()
	}
	lastOf := map[*Job]*Result{} // a queued retry -> the attempt before it, reported already
	for len(pending) > 0 || running > 0 {
		// Start everything that can start now.
		var still []*Job
		for _, j := range pending {
			if ctx.Err() != nil {
				// the run was stopped: what has not started is not launched (issue #189);
				// the flows running finish their cancellation below
				results = append(results, notRun(j, lastOf[j]))
				continue
			}
			if running >= maxParallel {
				still = append(still, j)
				continue
			}
			if err := pool.satisfiable(j.Needs); err != nil {
				r, _ := finish(Result{Job: j, Status: engine.Failed, Err: err, Start: time.Now()})
				results = append(results, r)
				continue
			}
			var devs map[string]device.Info
			if j.avoid != "" {
				// prefer another device for a retry; fall back to any once nothing else runs
				devs = pool.tryAlloc(withAvoid(j.Needs, j.avoid, pool))
				if devs == nil && running == 0 {
					devs = pool.tryAlloc(j.Needs)
				}
			} else {
				devs = pool.tryAlloc(j.Needs)
			}
			if devs == nil {
				still = append(still, j)
				continue
			}
			launch(j, devs)
		}
		pending = still
		if running == 0 {
			if len(pending) > 0 {
				// Nothing of this run's holds a device: what is left waits for devices other
				// processes hold (until WaitForDevice runs out), then fails naming them.
				j := pending[0]
				if devs, err := pool.allocOrWait(ctx, j.Needs); err == nil && devs != nil {
					pending = pending[1:]
					if ctx.Err() != nil {
						// stopped as the device was taken (the wait tries the device first):
						// give it back, unused (issue #189)
						pool.release(devs)
						results = append(results, notRun(j, lastOf[j]))
						continue
					}
					launch(j, devs)
					continue
				} else {
					if err == nil {
						err = fmt.Errorf("no device could run it")
					}
					for _, j := range pending {
						if ctx.Err() != nil { // stopped while waiting (issue #189)
							results = append(results, notRun(j, lastOf[j]))
							continue
						}
						r, _ := finish(Result{Job: j, Status: engine.Failed, Err: err, Start: time.Now()})
						results = append(results, r)
					}
				}
			}
			break
		}
		// Wait for any job to finish (an event, not a poll).
		r := <-done
		running--
		pool.release(r.Devices)
		if moved := movedOf(r); moved != nil {
			// back to the queue for another device, unreported (issue #268)
			if last, ok := lastOf[r.Job]; ok {
				lastOf[moved] = last
			}
			pending = append([]*Job{moved}, pending...)
			continue
		}
		retry := retryOf(r)
		r, ok := finish(r)
		if retry != nil && ok {
			lastOf[retry] = &r
			pending = append([]*Job{retry}, pending...)
			continue
		}
		results = append(results, r)
	}
	return results
}

// withAvoid steers a single-device retry away from the device that failed it.
func withAvoid(needs []Need, avoid string, pool *Pool) []Need {
	if len(needs) != 1 || needs[0].Device != "" {
		return needs
	}
	for _, d := range pool.Devices() {
		if d.Serial != avoid && needs[0].fits(d) {
			n := needs[0]
			n.Device = d.Serial
			// only if that device exists; tryAlloc returns nil while it is busy, and the
			// job waits for the next finished one
			return []Need{n}
		}
	}
	return needs
}

// DeviceLabel names the devices a result ran on.
func DeviceLabel(devs map[string]device.Info) string {
	if d, ok := devs[""]; ok && len(devs) == 1 {
		return d.Name
	}
	var parts []string
	for role, d := range devs {
		parts = append(parts, role+"="+d.Name)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}
