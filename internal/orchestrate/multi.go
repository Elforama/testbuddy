// Package orchestrate runs flows across devices: a work queue over a device pool for
// suites, and multi-device flows where each device role runs its own commands.
package orchestrate

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/js"
)

// MultiResult is a multi-device flow's outcome.
type MultiResult struct {
	Status   engine.Status
	Err      error
	Duration time.Duration
	Devices  map[string]string // role -> device name
	Videos   []engine.Video    // screen recordings (Options.Record), by role
}

// RunMulti runs a multi-device flow. drivers maps each role to its device.
//
// Top-level steps run in order. A `- alice: [...]` step runs on alice; a `parallel:` step
// runs each role's commands at the same time and waits for all (the first failure cancels
// the others). Devices coordinate with `signal` / `waitForSignal`. All devices share one
// JavaScript `output` object, so a value one device reads can be typed on another.
func RunMulti(ctx context.Context, f *flow.Flow, drivers map[string]driver.Driver, base engine.Options) (res MultiResult) {
	runMultiInto(ctx, f, drivers, base, &res)
	return res
}

// runMultiInto is RunMulti, into res: when the flow crashes (a panic), res still has what the
// run did, its videos among them, for the crash's result (issue #107).
func runMultiInto(ctx context.Context, f *flow.Flow, drivers map[string]driver.Driver, base engine.Options, res *MultiResult) {
	start := time.Now()
	*res = MultiResult{Devices: map[string]string{}}
	shared := js.New("")
	signals := engine.NewSignals()
	runners := map[string]*engine.Runner{}
	// Every runner is ended on every way out (a role that fails to start, a cancelled run, a
	// crash, the flow's end): Begin starts its recording before anything can fail (issue
	// #40). Each runner has its own defer, so one that fails to end does not keep the others
	// from ending; this one, deferred first, runs last and lists the videos role by role.
	videos := map[string][]engine.Video{}
	defer func() {
		for _, role := range f.Config.Devices {
			res.Videos = append(res.Videos, videos[role.Name]...)
		}
	}()
	for _, role := range f.Config.Devices {
		d := drivers[role.Name]
		if d == nil {
			res.Status, res.Err = engine.Failed, fmt.Errorf("no device for role %q", role.Name)
			return
		}
		res.Devices[role.Name] = d.Name()
		o := base
		o.Role = role.Name
		o.Signals = signals
		o.JS = shared
		// TB_ROLE before the flow's env, so it can use it; the role's env last, so it wins
		o.Env = append(append([]flow.KV{}, base.Env...), flow.KV{Key: "TB_ROLE", Value: role.Name})
		r := engine.New(d, o)
		defer func() {
			// Ending a runner stops its recordings: collect its videos after.
			r.End()
			videos[role.Name] = r.Videos()
		}()
		if err := r.Begin(f, role.Env); err != nil {
			res.Status, res.Err = engine.Failed, err
			return
		}
		if role.AppID != "" {
			r.SetAppID(role.AppID)
		}
		runners[role.Name] = r
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	all := func(cmds []*flow.Command) error {
		if len(cmds) == 0 {
			return nil
		}
		blocks := map[string][]*flow.Command{}
		for name := range runners {
			blocks[name] = cmds
		}
		return parallel(ctx, runners, blocks, f)
	}

	err := all(f.Config.OnFlowStart)
	if err == nil {
		err = runSteps(ctx, f.Commands, runners, f)
	}
	if cerr := all(f.Config.OnFlowComplete); err == nil {
		err = cerr
	}
	res.Duration = time.Since(start)
	res.Err = err
	res.Status = engine.Passed
	if err != nil {
		res.Status = engine.Failed
	}
}

func runSteps(ctx context.Context, cmds []*flow.Command, runners map[string]*engine.Runner, f *flow.Flow) error {
	for _, c := range cmds {
		if err := ctx.Err(); err != nil {
			return err
		}
		var err error
		switch {
		case c.Name == "on":
			r := runners[c.On]
			if r == nil {
				return fmt.Errorf("%s:%d: unknown device role %q", c.File, c.Line, c.On)
			}
			err = r.RunList(ctx, c.Commands, f)
		case c.Name == "parallel":
			err = parallel(ctx, runners, c.Parallel, f)
		default:
			// A plain command: fine when there is a single device, or for commands that do
			// not touch a device (scripts, signals).
			r, ok := soleRunner(runners)
			if !ok && deviceFree(c) {
				r, ok = anyRunner(runners), true
			}
			if !ok {
				return fmt.Errorf("%s:%d: %s: say which device runs it, e.g. `- alice: [%s ...]`", c.File, c.Line, c.Name, c.Name)
			}
			err = r.RunList(ctx, []*flow.Command{c}, f)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func deviceFree(c *flow.Command) bool {
	switch c.Name {
	case "runScript", "evalScript", "assertTrue", "signal", "waitForSignal", "setClipboard":
		return true
	}
	return false
}

func soleRunner(rs map[string]*engine.Runner) (*engine.Runner, bool) {
	if len(rs) != 1 {
		return nil, false
	}
	for _, r := range rs {
		return r, true
	}
	return nil, false
}

func anyRunner(rs map[string]*engine.Runner) *engine.Runner {
	for _, r := range rs {
		return r
	}
	return nil
}

// parallel runs each role's commands concurrently; the first failure cancels the rest.
func parallel(ctx context.Context, runners map[string]*engine.Runner, blocks map[string][]*flow.Command, f *flow.Flow) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	for role, cmds := range blocks {
		r := runners[role]
		if r == nil {
			return fmt.Errorf("unknown device role %q", role)
		}
		wg.Add(1)
		go func(role string, r *engine.Runner, cmds []*flow.Command) {
			defer wg.Done()
			if err := runListSafely(ctx, r, cmds, f); err != nil {
				mu.Lock()
				if first == nil && !errors.Is(err, context.Canceled) {
					first = fmt.Errorf("%s: %w", role, err)
					cancel()
				}
				mu.Unlock()
			}
		}(role, r, cmds)
	}
	wg.Wait()
	if first == nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return first
}

// runListSafely runs a role's commands on their own goroutine (a parallel: block, or the
// flow's onFlowStart / onFlowComplete), turning a crash (a panic) into the role's error: a
// panic there would end the process at once, before the flow's deferred End calls stop the
// devices' recordings, with every device's agent still running (issue #107).
func runListSafely(ctx context.Context, r *engine.Runner, cmds []*flow.Command, f *flow.Flow) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("tb crashed running this flow (please report it): %v\n%s", p, debug.Stack())
		}
	}()
	return r.RunList(ctx, cmds, f)
}
