package engine

import (
	"context"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

const (
	switchFlipWait   = time.Second
	switchAfterReply = 500 * time.Millisecond
	switchSlowRead   = time.Second
)

const switchRetryHold = 200 * time.Millisecond

var switchClasses = map[string]bool{"Switch": true, "Toggle": true, "CheckBox": true}

func isSwitch(n *ui.Node) bool {
	return n != nil && n.Enabled && n.Checkable && switchClasses[n.Class]
}

func (r *Runner) confirmSwitchTap(ctx context.Context, was *ui.Node, before *ui.Screen, x, y int, touchedAt time.Time, o driver.ActionOpts) error {
	if before == nil {
		return nil
	}
	sig := screenSig(before)
	deadline := touchedAt.Add(switchFlipWait)
	if d := time.Now().Add(switchAfterReply); d.After(deadline) {
		deadline = d
	}
	extended := false
	events, stop := r.Drv.Events()
	defer stop()
	for {
		seq := r.Drv.Seq()
		r.invalidate()
		s, err := r.read(ctx)
		if err != nil {
			return nil
		}
		r.mu.Lock()
		took := r.screenTook
		if r.screen != s {
			took = 0
		}
		r.mu.Unlock()
		n := sameSwitch(s, was)
		switch {
		case n == nil:
			return nil
		case n.Checked != was.Checked || n.Value != was.Value:
			return nil
		case screenSig(s) != sig:
			r.tracef("the switch did not move, but the screen did (%s): the tap took effect; not tapping again", screenDiff(before, s))
			return nil
		}
		if took > switchSlowRead && !extended {
			extended = true
			if d := time.Now().Add(switchAfterReply); d.After(deadline) {
				deadline = d
			}
		}
		if !time.Now().Before(deadline) {
			break
		}
		r.waitAny(ctx, events, seq, deadline)
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	r.tracef("the switch did not move in %s and nothing else on screen changed: the app did not take the touch; tapping it again, held %s", since(touchedAt), switchRetryHold)
	r.invalidate()
	r.mark("tap", [2]int{x, y})
	o.Hold = switchRetryHold
	res, err := r.Drv.Tap(ctx, x, y, o)
	if err != nil {
		return err
	}
	r.tracef("tap at (%d, %d) again: %s", x, y, actionDesc(res))
	return nil
}

func sameSwitch(s *ui.Screen, was *ui.Node) *ui.Node {
	for _, n := range s.Nodes {
		if n.Class == was.Class && n.ID == was.ID && n.Label() == was.Label() && n.Bounds == was.Bounds && n.Checkable {
			return n
		}
	}
	return nil
}
