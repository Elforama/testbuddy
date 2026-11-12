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
}

// runSafely runs a job, turning a crash (a panic) into the job's failure: the run goes on,
// and ends as usual, releasing its devices. A panic in a job's goroutine would otherwise end
// the process at once, with every device's agent still running.
func runSafely(run func(*Job, map[string]device.Info) Result, j *Job, devs map[string]device.Info) (r Result) {
	start := time.Now()
	defer func() {
		if p := recover(); p != nil {
			r = Result{Job: j, Devices: devs, Status: engine.Failed, Start: start, Duration: time.Since(start),
				Err: fmt.Errorf("tb crashed running this flow (please report it): %v\n%s", p, debug.Stack())}
		}
	}()
	return run(j, devs)
}

// HookCrash is a panic in a Started or Finished hook, as the flow's error (issue #117). A
// result whose error holds one with Hook "Finished" was not fully reported by its hook: the
// console line, the failure files or the log may be missing, and the caller can make up for
// it once Schedule returns (errors.As finds it).
type HookCrash struct {
	Hook  string // "Started" or "Finished"
	Value any    // what the hook panicked with
	Stack []byte
}

func (c *HookCrash) Error() string {
	what := "starting"
	if c.Hook == "Finished" {
		what = "reporting"
	}
	return fmt.Sprintf("tb crashed %s this flow (please report it): %v\n%s", what, c.Value, c.Stack)
}

// callHook runs a Started or Finished hook, turning a crash (a panic) into a *HookCrash: the
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
	// Prepare runs once per device before its first flow (e.g. disable animations).
	Prepare func(ctx context.Context, d device.Info, drv driver.Driver)
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
	prepared := map[string]bool{}
	var prepMu sync.Mutex
	runOne := func(j *Job, devs map[string]device.Info) Result {
		res := Result{Job: j, Devices: devs, Start: time.Now()}
		drivers := map[string]driver.Driver{}
		notes := map[string]string{} // role -> the device was lost and opened again
		for role, d := range devs {
			drv, note, err := pool.driverForJob(ctx, d)
			if err != nil {
				res.Status, res.Err = engine.Failed, fmt.Errorf("%s: %w", d.Name, err)
				return res
			}
			if note != "" {
				notes[role] = note
				if hooks.Notice != nil {
					hooks.Notice(j, d, note)
				}
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
		for role, note := range notes {
			opts.Trace.Printf(role, 0, "%s", note)
		}
		if len(j.Flow.Config.Devices) > 0 {
			mr := RunMulti(ctx, j.Flow, drivers, opts)
			res.Status, res.Err, res.Duration, res.Videos = mr.Status, mr.Err, mr.Duration, mr.Videos
			return res
		}
		r := engine.New(drivers[""], opts)
		fr := r.RunFlow(ctx, j.Flow)
		res.Status, res.Err, res.Duration, res.Steps, res.Runner, res.Videos = fr.Status, fr.Err, fr.Duration, fr.Steps, r, fr.Videos
		return res
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

	// The ordered sequence: one at a time, on the first device that fits. A failed flow is
	// retried (as in the queue) before the sequence goes on, so only its last attempt
	// decides whether the sequence stops.
	for i, j := range seq {
		var r Result
		reported := false // Finished already ran for r (and crashed)
		for {
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
					if err == nil {
						err = fmt.Errorf("no device could run it")
					}
					r = Result{Job: j, Status: engine.Failed, Err: err, Start: time.Now()}
					break
				}
			}
			if crashed, ok := start(j, devs); ok {
				r = runSafely(runOne, j, devs)
			} else {
				r = crashed
			}
			pool.release(devs)
			next := retryOf(r)
			if next == nil {
				break
			}
			// this attempt; the retry is reported again
			if fr, ok := finish(r); !ok {
				r, reported = fr, true
				break
			}
			j = next
		}
		if !reported {
			r, _ = finish(r)
		}
		results = append(results, r)
		if r.Status == engine.Failed && !continueSeq {
			// every flow after it is reported skipped: the rest of the sequence, then the queue
			for _, rj := range append(append([]*Job{}, seq[i+1:]...), rest...) {
				sr, _ := finish(Result{Job: rj, Status: engine.Skipped, Err: fmt.Errorf("skipped: an ordered flow failed"), Start: time.Now()})
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
			if !ok {
				done <- crashed
				return
			}
			done <- runSafely(runOne, j, devs)
		}()
	}
	for len(pending) > 0 || running > 0 {
		// Start everything that can start now.
		var still []*Job
		for _, j := range pending {
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
					launch(j, devs)
					continue
				} else {
					if err == nil {
						err = fmt.Errorf("no device could run it")
					}
					for _, j := range pending {
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
		retry := retryOf(r)
		r, ok := finish(r)
		if retry != nil && ok {
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
