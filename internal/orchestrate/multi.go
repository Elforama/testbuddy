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

type MultiResult struct {
	Status   engine.Status
	Err      error
	Duration time.Duration
	Devices  map[string]string
	Videos   []engine.Video
}

func RunMulti(ctx context.Context, f *flow.Flow, drivers map[string]driver.Driver, base engine.Options) (res MultiResult) {
	runMultiInto(ctx, f, drivers, base, &res)
	return res
}

func runMultiInto(ctx context.Context, f *flow.Flow, drivers map[string]driver.Driver, base engine.Options, res *MultiResult) {
	start := time.Now()
	*res = MultiResult{Devices: map[string]string{}}
	shared := js.New("")
	signals := engine.NewSignals()
	runners := map[string]*engine.Runner{}
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
		o.Env = append(append([]flow.KV{}, base.Env...), flow.KV{Key: "TB_ROLE", Value: role.Name})
		r := engine.New(d, o)
		defer func() {
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

func runListSafely(ctx context.Context, r *engine.Runner, cmds []*flow.Command, f *flow.Flow) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("tb crashed running this flow (please report it): %v\n%s", p, debug.Stack())
		}
	}()
	return r.RunList(ctx, cmds, f)
}
