package engine

import (
	"context"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// switchFlipWait: how long after its touch an iOS switch has to show its new value before the
// tap is taken as lost (issue #250). A switch that takes a tap flips its value as the touch
// ends, in the app's next frame. The agent confirms a tap only after its lift reached the app,
// so the reads after the reply, which run on the app's main thread too, come after the touch
// the app already has. Long enough that an app busy for most of a second still shows a tap it
// took, so a switch that took one is not toggled twice.
//
// The wait also lasts at least switchAfterReply after the tap's reply: a reply that came late
// (the agent or the app slow) leaves the flip as little time as a prompt one. And a read that
// took over switchSlowRead (the app busy) shows the screen as it started: once, the wait then
// runs switchAfterReply past it.
//
// Known limit: a switch whose value the app sets asynchronously (after a network call, say)
// later than this wait is tapped a second time, and turns back.
const (
	switchFlipWait   = time.Second
	switchAfterReply = 500 * time.Millisecond
	switchSlowRead   = time.Second
)

// switchRetryHold: how long the touch of a switch's second tap is held (issue #250). A stall
// shorter than the hold cannot batch its down and up: the app gets the down when it runs again
// and the up after (measured: 0 of 32 such taps lost with the app held 100 or 150 ms across
// them, against 17 of 20 at 20 ms). Past a scroll view's 150 ms touch delay, so the switch in a
// list gets the down while the finger is still on it; far short of a long press (500 ms).
const switchRetryHold = 200 * time.Millisecond

// switchClasses are the iOS kinds whose tap is checked by their value (issue #250).
var switchClasses = map[string]bool{"Switch": true, "Toggle": true, "CheckBox": true}

// isSwitch says whether an iOS tap on n is checked by n's value: an enabled switch, toggle or
// check box (the agent lists each of these as checkable).
func isSwitch(n *ui.Node) bool {
	return n != nil && n.Enabled && n.Checkable && switchClasses[n.Class]
}

// confirmSwitchTap (iOS, issue #250): a tap on a switch is checked by the switch's value, not
// by "the screen changed". A UISwitch (a SwiftUI Toggle's) ignores a short touch whose down and
// up the app gets in one batch, as when its main thread is busy across the 20 ms touch: the
// touch is delivered and the app answers, but the switch does not move. The press redraws, so
// the agent reports a change all the same. Measured on an iOS 27 simulator, with the app held
// for 150 ms across the touch: 17 of 20 taps on the bench's switch lost (15 of 16 at 300 ms),
// none of 18 on its buttons.
//
// The switch is read again on change events until switchFlipWait after the touch (or
// switchAfterReply after the reply, whichever is later; see switchFlipWait): its value
// moved, it moved or went, or anything else on screen changed (a sheet it opened), and the tap
// stands. Still in place with its old value on an otherwise unchanged screen, it is tapped
// once more at the same point, held switchRetryHold so a stall still going on cannot batch it
// too; whatever that does, the next step decides. A tap that took is
// never sent again within the wait, so a switch is not toggled twice by a late answer.
// A CI allowance (package ci): seen on a CI runner only. A local iPhone 17 Pro simulator
// (iOS 27) lost no switch tap in 65 runs of the a11y values flow, idle or with a second
// simulator running flows, nor in 80 direct taps; only an induced stall of 40 ms or more
// across the touch loses it. Outside CI mode the tap stands, as before.
func (r *Runner) confirmSwitchTap(ctx context.Context, was *ui.Node, before *ui.Screen, x, y int, touchedAt time.Time, o driver.ActionOpts) error {
	if before == nil {
		return nil
	}
	sig := screenSig(before)
	// From the touch or the reply, whichever ends later: a slow reply must not eat the wait.
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
			return nil // the next step reads the screen and says what it finds
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
			return nil // it moved or went: the tap did something
		case n.Checked != was.Checked || n.Value != was.Value:
			return nil // it moved: the tap took
		case screenSig(s) != sig:
			r.tracef("the switch did not move, but the screen did (%s): the tap took effect; not tapping again", screenDiff(before, s))
			return nil
		}
		if took > switchSlowRead && !extended {
			// The app was busy for that read, which shows the screen as it started: once, a
			// little longer for a flip that came during it.
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

// sameSwitch finds was in s: the same kind, id and label, in the same place.
func sameSwitch(s *ui.Screen, was *ui.Node) *ui.Node {
	for _, n := range s.Nodes {
		if n.Class == was.Class && n.ID == was.ID && n.Label() == was.Label() && n.Bounds == was.Bounds && n.Checkable {
			return n
		}
	}
	return nil
}
