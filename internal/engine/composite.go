package engine

import (
	"context"
	"fmt"
	"math"

	"github.com/elforama/testbuddy/internal/flow"
)

func (r *Runner) runFlowCmd(ctx context.Context, c *flow.Command, f *flow.Flow) (Status, error) {
	ok, err := r.evalCondition(ctx, c.When, r.adjusted(r.opts.OptionalTimeout))
	if err != nil {
		return "", err
	}
	if !ok {
		r.tracef("condition not met (%s): skipped", describeCondition(c.When))
		return Skipped, nil
	}
	r.pushEnv()
	defer r.popEnv()
	if err := r.define(c.Env); err != nil {
		return "", err
	}
	if c.SubFlow != nil {
		sub := c.SubFlow
		if err := r.define(sub.Config.Env); err != nil {
			return "", err
		}
		var err error
		if len(sub.Config.OnFlowStart) > 0 {
			err = r.runList(ctx, sub.Config.OnFlowStart, sub, nil)
		}
		if err == nil {
			err = r.runList(ctx, sub.Commands, sub, nil)
		}
		if len(sub.Config.OnFlowComplete) > 0 {
			if cerr := r.runList(ctx, sub.Config.OnFlowComplete, sub, nil); err == nil {
				err = cerr
			}
		}
		return "", err
	}
	return "", r.runList(ctx, c.Commands, f, nil)
}

func (r *Runner) repeat(ctx context.Context, c *flow.Command, f *flow.Flow) (Status, error) {
	limit := math.MaxInt
	if c.Times != "" {
		t, err := r.eval(c.Times)
		if err != nil {
			return "", err
		}
		if limit, err = flow.RepeatTimes(t); err != nil {
			if t != c.Times {
				return "", fmt.Errorf("invalid times %q (from %q): expected a number, 0 or more", t, c.Times)
			}
			return "", err
		}
	}
	n := 0
	for n < limit {
		if c.While != nil {
			ok, err := r.evalCondition(ctx, c.While, r.adjusted(r.opts.OptionalTimeout))
			if err != nil {
				return "", err
			}
			if !ok {
				r.tracef("repeat: the while condition no longer holds after %d times", n)
				break
			}
		}
		r.pushEnv()
		err := r.runList(ctx, c.Commands, f, nil)
		r.popEnv()
		if err != nil {
			return "", err
		}
		n++
	}
	if n == 0 {
		return Skipped, nil
	}
	return "", nil
}

func (r *Runner) retry(ctx context.Context, c *flow.Command, f *flow.Flow) (Status, error) {
	max := 1
	if c.MaxRetries != "" {
		t, err := r.eval(c.MaxRetries)
		if err != nil {
			return "", err
		}
		if max, err = flow.MaxRetries(t); err != nil {
			if t != c.MaxRetries {
				return "", fmt.Errorf("invalid maxRetries %q (from %q): expected a number, 0 or more", t, c.MaxRetries)
			}
			return "", err
		}
	}
	max = min(max, 3)
	var last error
	for attempt := 0; attempt <= max; attempt++ {
		r.pushEnv()
		err := r.define(c.Env)
		if err == nil {
			if c.SubFlow != nil {
				sub := c.SubFlow
				err = r.define(sub.Config.Env)
				if err == nil && len(sub.Config.OnFlowStart) > 0 {
					err = r.runList(ctx, sub.Config.OnFlowStart, sub, nil)
				}
				if err == nil {
					err = r.runList(ctx, sub.Commands, sub, nil)
				}
				if len(sub.Config.OnFlowComplete) > 0 {
					if cerr := r.runList(ctx, sub.Config.OnFlowComplete, sub, nil); err == nil {
						err = cerr
					}
				}
			} else {
				err = r.runList(ctx, c.Commands, f, nil)
			}
		}
		r.popEnv()
		if err == nil {
			return "", nil
		}
		if !isFlowError(err) {
			return "", err
		}
		last = err
		if attempt < max {
			r.tracef("retry: attempt %d of %d failed (%v): trying again", attempt+1, max+1, err)
		}
	}
	return "", last
}
