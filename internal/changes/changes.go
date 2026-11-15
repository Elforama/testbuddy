// Package changes is how tb waits for the screen to change: the one rule for what a device
// event says about the screen (News), and the wait between two reads of the screen that
// rests on it (Watch), with its silence safety net. The runner's waits (lookups, waits for an
// element to go) and the session's `tb watch` share it, so they keep that rule alike (issues
// #285, #288).
package changes

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// News: e reports a change after the read at seq, so the screen may differ from that read.
// It is the one test of what an event says about the screen. An event for seq or earlier says
// nothing new: the settle of a change already read (iOS's agent sends one once the change tb
// read goes quiet), one buffered before the read, one about something else. Only news proves
// the device is reporting the screen (issue #285).
func News(e driver.Event, seq int64) bool { return e.Seq > seq }

// RestlessAfter: when the UI keeps changing without ever going quiet (a ticker, a
// countdown), a wait reads anyway after this long, then again at most this often.
const RestlessAfter = 400 * time.Millisecond

// SilentAfter is the first gap of the silence safety net, for a device that does not report a
// change during a wait (an app whose changes the platform does not report; iOS's embedded
// Safari view, whose page is drawn by another process): after this long without news, the
// screen is read again (see Watch). 1.5 s; TB_SILENT_MS overrides, read once; 0 disables.
// It is the one place this setting lives: callers pass it to Subscribe.
func SilentAfter() time.Duration { return silentAfter() }

var silentAfter = sync.OnceValue(func() time.Duration {
	if v, err := strconv.Atoi(os.Getenv("TB_SILENT_MS")); err == nil {
		return time.Duration(v) * time.Millisecond
	}
	return 1500 * time.Millisecond
})

// ErrLost: the device's events ended (its agent connection dropped).
var ErrLost = errors.New("lost the connection to the device agent")

// Source is what a Watch listens to: a driver's events and its change counter.
type Source interface {
	Seq() int64
	Events() (<-chan driver.Event, func())
}

// Watch is how a wait that re-reads the screen until it shows something waits between reads:
// it wakes the wait when the screen may differ from its last read, and it holds the wait's
// silence safety net. Every such wait goes through it, so they keep one rule: only news (see
// News) proves the device is reporting the screen. An event without news changes nothing
// here: it neither wakes the wait (a re-read would only find the screen already read) nor
// touches the net.
type Watch struct {
	src     Source
	events  <-chan driver.Event
	stop    func()
	first   time.Duration // the net's first gap; 0: no net
	gap     time.Duration // without news for this long, the wait reads the screen again
	backoff bool          // after each silence re-read, the gap doubles up to twice first
}

// Subscribe starts watching src's events for one wait; Stop ends it. Subscribe before the
// first read, so no change after it is missed. silentAfter is the net's first gap (0: no
// net). With backoff (a lookup), the gap doubles once and stays at twice silentAfter while the
// device reports nothing; without, the screen is re-read every silentAfter throughout.
//
// The cap (issue #96): uncapped, the gaps went 1.5, 3, 6, 12, 24 s, so on a screen that stays
// silent (iOS's embedded Safari view while its page loads: another process) a page that
// loaded 30 s into a 45 s wait was only read at the deadline. Capped, content is read at most
// 3 s after it appears, for one read every 3 s while the screen stays silent: a read takes 20
// to 50 ms on an iOS simulator and 13 to 60 ms on an Android emulator (bench/results), and the
// next gap starts only once a read has ended, so even a slow read on a busy runner (4 s, issue
// #92) does not pile up. A screen that reports its changes never waits on this.
func Subscribe(src Source, silentAfter time.Duration, backoff bool) *Watch {
	events, stop := src.Events()
	return &Watch{src: src, events: events, stop: stop, first: silentAfter, gap: silentAfter, backoff: backoff}
}

// Stop ends the subscription.
func (w *Watch) Stop() { w.stop() }

// Wait blocks until the screen may differ from the read at seq, or the deadline. With early
// (a positive lookup's first wait), it returns on the first change; otherwise it waits for
// the change to settle, or RestlessAfter, so as not to take a snapshot for every frame.
// silent > 0 says that no news came for that long: the screen may have changed unreported,
// and the caller reads it afresh. It returns ErrLost when the device's events end, and the
// context's error when it ends.
//
// The net is armed for the whole wait and is disarmed only by news, which also guarantees a
// wake (a "settled" at once, anything else within RestlessAfter): a wait can never be left
// with neither. Nor can events without news put the net off: it counts from the start of the
// wait, not from the last event, so a device that keeps repeating them (a settle, a change in
// a process in the background) cannot hold it off. A sliding window would: a device that
// repeats such events faster than the gap would hold the net off forever.
func (w *Watch) Wait(ctx context.Context, seq int64, deadline time.Time, early bool) (silent time.Duration, err error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	gap := w.gap
	w.gap = w.first // any return but silence: the next silence starts afresh
	dl := time.NewTimer(time.Until(deadline))
	defer dl.Stop()
	var restless <-chan time.Time
	if w.src.Seq() > seq {
		if early {
			return 0, nil
		}
		restless = time.After(RestlessAfter)
	}
	var hush <-chan time.Time
	if gap > 0 {
		hush = time.After(gap)
	}
	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-dl.C:
			return 0, nil
		case <-restless:
			return 0, nil
		case <-hush:
			if w.backoff {
				w.gap = min(2*gap, 2*w.first)
			} else {
				w.gap = gap
			}
			return gap, nil
		case e, ok := <-w.events:
			if !ok || e.Kind == "disconnected" {
				return 0, ErrLost
			}
			if !News(e, seq) {
				continue // nothing new about the screen: the net keeps counting (issue #285)
			}
			hush = nil // the device reported the change: it wakes this wait below
			if e.Kind == "settled" || early {
				return 0, nil
			}
			if restless == nil {
				restless = time.After(RestlessAfter)
			}
		}
	}
}
