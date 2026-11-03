package orchestrate

import (
	"context"
	"fmt"
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
	avoid   string // device serial that failed it last time
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
}

// Hooks let the CLI observe a run.
type Hooks struct {
	// Options builds engine options for a job on a device (env, reporter, artifacts).
	Options func(job *Job, devs map[string]device.Info) engine.Options
	// Started / Finished report jobs.
	Started  func(job *Job, devs map[string]device.Info)
	Finished func(res Result)
	// Prepare runs once per device before its first flow (e.g. disable animations).
	Prepare func(ctx context.Context, d device.Info, drv driver.Driver)
}

// Schedule runs jobs over the pool, at most maxParallel at a time (0 = one per device). A device that finishes a job takes the next one it can
// serve (a work queue, not a fixed split), so a slow device does not hold up the rest.
// Failed jobs are retried up to retries times, on another device when one is free.
// Sequence jobs run first, in order, one at a time.
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
		for role, d := range devs {
			drv, err := pool.Driver(ctx, d)
			if err != nil {
				res.Status, res.Err = engine.Failed, fmt.Errorf("%s: %w", d.Name, err)
				return res
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
		if len(j.Flow.Config.Devices) > 0 {
			mr := RunMulti(ctx, j.Flow, drivers, opts)
			res.Status, res.Err, res.Duration = mr.Status, mr.Err, mr.Duration
			return res
		}
		r := engine.New(drivers[""], opts)
		fr := r.RunFlow(ctx, j.Flow)
		res.Status, res.Err, res.Duration, res.Steps, res.Runner = fr.Status, fr.Err, fr.Duration, fr.Steps, r
		return res
	}

	// The ordered sequence: one at a time, on the first device that fits.
	for _, j := range seq {
		if err := pool.satisfiable(j.Needs); err != nil {
			r := Result{Job: j, Status: engine.Failed, Err: err, Start: time.Now()}
			results = append(results, r)
			hooks.Finished(r)
			continue
		}
		devs := pool.tryAlloc(j.Needs)
		hooks.Started(j, devs)
		r := runOne(j, devs)
		pool.release(devs)
		results = append(results, r)
		hooks.Finished(r)
		if r.Status == engine.Failed && !continueSeq {
			for _, rj := range rest {
				sr := Result{Job: rj, Status: engine.Skipped, Err: fmt.Errorf("skipped: an ordered flow failed"), Start: time.Now()}
				results = append(results, sr)
				hooks.Finished(sr)
			}
			return results
		}
	}

	// The queue. Multi-device jobs first: they need the most devices at once.
	sort.SliceStable(rest, func(a, b int) bool { return len(rest[a].Needs) > len(rest[b].Needs) })
	pending := rest
	done := make(chan Result)
	running := 0
	for len(pending) > 0 || running > 0 {
		// Start everything that can start now.
		var still []*Job
		for _, j := range pending {
			if running >= maxParallel {
				still = append(still, j)
				continue
			}
			if err := pool.satisfiable(j.Needs); err != nil {
				r := Result{Job: j, Status: engine.Failed, Err: err, Start: time.Now()}
				results = append(results, r)
				hooks.Finished(r)
				continue
			}
			needs := j.Needs
			if j.avoid != "" && running > 0 {
				// prefer another device for a retry; fall back to any once nothing else runs
				needs = withAvoid(needs, j.avoid, pool)
			}
			devs := pool.tryAlloc(needs)
			if devs == nil {
				still = append(still, j)
				continue
			}
			running++
			hooks.Started(j, devs)
			go func(j *Job, devs map[string]device.Info) {
				done <- runOne(j, devs)
			}(j, devs)
		}
		pending = still
		if running == 0 {
			if len(pending) > 0 { // cannot happen: satisfiable jobs always fit an idle pool
				for _, j := range pending {
					r := Result{Job: j, Status: engine.Failed, Err: fmt.Errorf("no device could run it"), Start: time.Now()}
					results = append(results, r)
					hooks.Finished(r)
				}
			}
			break
		}
		// Wait for any job to finish (an event, not a poll).
		r := <-done
		running--
		pool.release(r.Devices)
		if r.Status == engine.Failed && r.Job.Attempt < retries && ctx.Err() == nil {
			retry := *r.Job
			retry.Attempt++
			for _, d := range r.Devices {
				retry.avoid = d.Serial
			}
			hooks.Finished(r)
			pending = append([]*Job{&retry}, pending...)
			continue
		}
		results = append(results, r)
		hooks.Finished(r)
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
