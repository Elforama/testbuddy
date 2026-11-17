package engine

import (
	"context"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

// optionalQuietBudget is how long an optional lookup (an optional tap's target, an optional
// copyTextFrom) waits on a still screen before it gives up (issue #278). Maestro's optional
// lookup waits its whole budget (7 s, Options.OptionalTimeout) whatever the screen does, so a
// known-absent fallback cost 7 s each time. tb gives up sooner only when the screen shows
// nothing is coming:
//
//   - the device has reported no change, and the reads have shown none, for this long since
//     the lookup began or since the last change;
//   - a read past every cache (Runner.reread) then shows the same screen, so a device that does
//     not report its changes (see changes.SilentAfter) cannot pass for a still one.
//
// While the screen keeps changing (a spinner, a page loading, a dialog sliding in) the quiet
// never starts, and the lookup waits the whole optional budget, as Maestro's: the late dialog
// that is the usual reason for `optional: true` is still found. What it gives up on is an
// element that appears after the screen has stood still for this long, with nothing reported
// in between: a dialog that comes from a slow network reply on an idle screen. A flow that
// must wait for one says so with extendedWaitUntil or a runFlow `when:` with a timeout.
const optionalQuietBudget = 2 * time.Second

// quietWatch tells an optional lookup when the screen has stood still for optionalQuietBudget.
// It spans the whole lookup of one step's target, however many waits that takes (a tap looks
// for keyboardGrace, then again). nil (a lookup that is not optional) never ends a lookup early.
type quietWatch struct {
	since     time.Time // when the screen was last seen to change (the lookup's start at first)
	seq       int64     // the change counter of the last read
	sig       uint64    // the signature of the last read
	confirmed bool      // the read in hand is the fresh one that checks the quiet
}

// optionalLookup runs look as the lookup of an optional step's target when optional is set:
// it ends once the screen has stood still for optionalQuietBudget (issue #278).
func (r *Runner) optionalLookup(optional bool, look func()) {
	if optional {
		r.quiet = &quietWatch{since: time.Now(), seq: -1}
		defer func() { r.quiet = nil }()
	}
	look()
}

// seen takes each read: a change reported (the counter moved) or read (the screen differs)
// starts the quiet again.
func (q *quietWatch) seen(s *ui.Screen) {
	if q == nil || s == nil {
		return
	}
	sig := s.Signature()
	if q.seq >= 0 && (s.Seq != q.seq || sig != q.sig) {
		q.since = time.Now()
		q.confirmed = false
	}
	q.seq, q.sig = s.Seq, sig
}

// wake is when the quiet would be long enough: the lookup's wait wakes for it.
func (q *quietWatch) wake(deadline time.Time) time.Time {
	if q == nil {
		return deadline
	}
	if at := q.since.Add(optionalQuietBudget); at.Before(deadline) {
		return at
	}
	return deadline
}

// over reports whether the lookup may give up on the read in hand: the screen has been still
// for the budget, as a read past every cache confirmed. When the quiet is long enough but not
// yet confirmed, it takes that fresh read and says so (again): the lookup then matches on it,
// and asks again.
func (q *quietWatch) over(ctx context.Context, r *Runner) (done, again bool, err error) {
	if q == nil || time.Now().Before(q.since.Add(optionalQuietBudget)) {
		return false, false, nil
	}
	if q.confirmed {
		return true, false, nil
	}
	q.confirmed = true
	if _, err := r.reread(ctx); err != nil {
		return false, false, err
	}
	return false, true, nil
}
