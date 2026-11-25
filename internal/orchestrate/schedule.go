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

type Job struct {
	Flow      *flow.Flow
	Needs     []Need
	Attempt   int
	Seq       bool
	ShardID   int
	DebugName string
	avoid     string
	moves     []lostDevice
}

type lostDevice struct {
	role string
	dev  device.Info
	err  error
}

func (l lostDevice) note() string {
	return fmt.Sprintf("%s (%s): %s; the flow ran no step there and went back to the queue: moved to another device (not a retry)",
		l.dev.Name, l.dev.Serial, strings.TrimSpace(l.err.Error()))
}

func withMoves(r Result) Result {
	if r.Job == nil || len(r.Job.moves) == 0 || r.logged {
		return r
	}
	var b strings.Builder
	for _, m := range r.Job.moves {
		b.WriteString("\nearlier, ")
		b.WriteString(m.note())
	}
	if r.Err != nil {
		r.Err = fmt.Errorf("%w%s", r.Err, b.String())
	} else {
		r.Err = errors.New(strings.TrimPrefix(b.String(), "\n"))
	}
	return r
}

type Result struct {
	Job      *Job
	Devices  map[string]device.Info
	Status   engine.Status
	Err      error
	Duration time.Duration
	Start    time.Time
	Steps    []engine.StepResult
	Runner   *engine.Runner
	Videos   []engine.Video
	Retried  bool
	lost     *lostDevice
	logged   bool
}

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

type HookCrash struct {
	Hook  string
	Value any
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

func callHook(hook string, fn func()) (crash *HookCrash) {
	defer func() {
		if p := recover(); p != nil {
			crash = &HookCrash{Hook: hook, Value: p, Stack: debug.Stack()}
		}
	}()
	fn()
	return nil
}

func withCrash(r Result, crash *HookCrash) Result {
	r.Status = engine.Failed
	if r.Err != nil {
		r.Err = fmt.Errorf("%w\n%w", r.Err, crash)
	} else {
		r.Err = crash
	}
	return r
}

var (
	errStopped    = errors.New("skipped: the run was stopped before this flow ran")
	errNotRetried = errors.New("not retried: the run was stopped")
)

type Hooks struct {
	Options  func(job *Job, devs map[string]device.Info) engine.Options
	Started  func(job *Job, devs map[string]device.Info)
	Finished func(res Result)
	Notice   func(job *Job, d device.Info, msg string)
	Prepare  func(ctx context.Context, d device.Info, drv driver.Driver)
	Failure  func(r Result, drivers map[string]driver.Driver, notes map[string]string)
}

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
	capture := func(r *Result) {
		if hooks.Failure == nil || r.Status != engine.Failed || len(r.Devices) == 0 || ctx.Err() != nil || r.lost != nil {
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
					delete(prepared, d.Serial)
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
	runOne := func(j *Job, devs map[string]device.Info, res *Result) {
		drivers := map[string]driver.Driver{}
		notes := map[string]string{}
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
		res.logged = true
		for _, m := range j.moves {
			opts.Trace.Printf(m.role, 0, "%s", m.note())
		}
		for role, note := range notes {
			opts.Trace.Printf(role, 0, "%s", note)
		}
		if len(j.Flow.Config.Devices) > 0 {
			var mr MultiResult
			defer func() { res.Videos = mr.Videos }()
			runMultiInto(ctx, j.Flow, drivers, opts, &mr)
			res.Status, res.Err, res.Duration = mr.Status, mr.Err, mr.Duration
			return
		}
		r := engine.New(drivers[""], opts)
		defer func() { res.Videos = r.Videos() }()
		fr := r.RunFlow(ctx, j.Flow)
		res.Status, res.Err, res.Duration, res.Steps, res.Runner = fr.Status, fr.Err, fr.Duration, fr.Steps, r
	}

	start := func(j *Job, devs map[string]device.Info) (Result, bool) {
		if crash := callHook("Started", func() { hooks.Started(j, devs) }); crash != nil {
			return Result{Job: j, Devices: devs, Status: engine.Failed, Err: crash, Start: time.Now()}, false
		}
		return Result{}, true
	}
	finish := func(r Result) (Result, bool) {
		r = withMoves(r)
		if r.Status == engine.Failed && ctx.Err() != nil && (r.Err == nil || errors.Is(r.Err, ctx.Err())) {
			r.Err = fmt.Errorf("cancelled: the run was stopped before this flow ran or finished (%w)", ctx.Err())
		}
		if crash := callHook("Finished", func() { hooks.Finished(r) }); crash != nil {
			return withCrash(r, crash), false
		}
		return r, true
	}

	retryOf := func(r Result) *Job {
		if r.Status != engine.Failed || r.Job.Attempt >= retries || ctx.Err() != nil || r.lost != nil {
			return nil
		}
		retry := *r.Job
		retry.Attempt++
		for _, d := range r.Devices {
			retry.avoid = d.Serial
		}
		return &retry
	}

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

	for i, j := range seq {
		var r Result
		reported := false
		var last *Result
		for {
			if ctx.Err() != nil {
				r, reported = notRun(j, last), true
				break
			}
			if err := pool.satisfiable(j.Needs); err != nil {
				r = Result{Job: j, Status: engine.Failed, Err: err, Start: time.Now()}
				break
			}
			var devs map[string]device.Info
			if j.avoid != "" {
				devs = pool.tryAlloc(withAvoid(j.Needs, j.avoid, pool))
			}
			if devs == nil {
				var err error
				if devs, err = pool.allocOrWait(ctx, j.Needs); err != nil || devs == nil {
					if ctx.Err() != nil {
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
				j = moved
				continue
			}
			next := retryOf(r)
			if next == nil {
				break
			}
			r.Retried = true
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

	sort.SliceStable(rest, func(a, b int) bool { return len(rest[a].Needs) > len(rest[b].Needs) })
	pending := rest
	done := make(chan Result)
	running := 0
	launch := func(j *Job, devs map[string]device.Info) {
		running++
		crashed, ok := start(j, devs)
		go func() {
			r := crashed
			if ok {
				r = runSafely(runOne, j, devs)
			}
			capture(&r)
			done <- r
		}()
	}
	lastOf := map[*Job]*Result{}
	for len(pending) > 0 || running > 0 {
		var still []*Job
		for _, j := range pending {
			if ctx.Err() != nil {
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
				j := pending[0]
				if devs, err := pool.allocOrWait(ctx, j.Needs); err == nil && devs != nil {
					pending = pending[1:]
					if ctx.Err() != nil {
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
						if ctx.Err() != nil {
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
		r := <-done
		running--
		pool.release(r.Devices)
		if moved := movedOf(r); moved != nil {
			if last, ok := lastOf[r.Job]; ok {
				lastOf[moved] = last
			}
			pending = append([]*Job{moved}, pending...)
			continue
		}
		retry := retryOf(r)
		r.Retried = retry != nil
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

func withAvoid(needs []Need, avoid string, pool *Pool) []Need {
	if len(needs) != 1 || needs[0].Device != "" {
		return needs
	}
	for _, d := range pool.Devices() {
		if d.Serial != avoid && needs[0].fits(d) {
			n := needs[0]
			n.Device = d.Serial
			return []Need{n}
		}
	}
	return needs
}

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
