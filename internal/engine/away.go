package engine

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/ci"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

// Issue #142: on a CI simulator, the read after a tap showed SpringBoard's app switcher where
// the flow's app should have been, and the assertVisible that followed waited out its whole
// timeout (28.6 s) on it, then failed with only "Assertion is false". A lookup now notices
// SpringBoard alone in front of the flow's app: it says so in its trace at once, fails once that
// has lasted the grace (awayGrace) over two reads at least, none of them slow (awayRead), and
// names what is in front in the step's message either way.
//
// Only SpringBoard alone counts, and only after the flow's app was seen in front: an iOS
// permission alert, an app the flow opened (openLink, launchApp of another app: that app is
// then the one in front), the keyboard and the system sheets apps present are not SpringBoard
// alone; a SpringBoard alert with no app behind it is answered or matched as before; and a flow
// that went home itself (pressKey home, stopApp, ...) can wait on the home screen as long as
// it likes. An element the lookup is looking for that is on SpringBoard's screen is still found.
//
// Only screens tb knows count toward failing early: the home screen and the app switcher
// (away). Other SpringBoard trees, such as system UI it shows over the app, are not judged.
//
// Waits for absence (assertNotVisible, a notVisible condition) never pass on SpringBoard in
// front: it shows none of the app's elements. assertNotVisible, assertTrue and
// extendedWaitUntil fail the same way; a when/while condition is then not met (its block is
// skipped), never failed.
//
// The app is not brought back (activated): in #142 the switcher was the agent misreading a
// starved simulator (an empty active-apps list read as SpringBoard alone; the agent reads the
// last app in front now, Front.read), and launching the app over whatever really is in front
// would hide it.

const springboard = "com.apple.springboard"

// awayRead: a read slower than this is the device struggling, not answering as usual (reads
// take 0.05-0.5 s, up to ~5 s on a busy CI runner). In #142 the reads that showed the switcher
// took 5.4 and 9.9 s, and the agent had just failed to list the active apps
// (kAXErrorIPCTimeout): a starved simulator can be misread as SpringBoard alone. Such a read
// does not count toward failing early; the step waits its full time, and its message still
// says what the read showed.
var awayRead = 2 * time.Second

func (r *Runner) awayGrace() time.Duration {
	if r.opts.AwayGrace > 0 {
		return r.opts.AwayGrace
	}
	if ci.On() {
		// A CI allowance (package ci): a busy runner's SpringBoard can be slow to hand the
		// screen back after a transition.
		return 10 * time.Second
	}
	return 5 * time.Second
}

// noteFront keeps the app a read shows in front: an app other than SpringBoard in front is the
// flow's, and the flow is back in it.
func (r *Runner) noteFront(s *ui.Screen) {
	if s == nil {
		return
	}
	if fg := s.Foreground(); fg != "" && fg != springboard {
		r.frontApp = fg
		r.leftApp = false
	}
}

// leavesApp: commands after which SpringBoard alone in front is what the flow asked for.
func leavesApp(c *flow.Command) bool {
	switch c.Name {
	case "stopApp", "killApp", "clearState", "openLink":
		return true
	case "pressKey":
		switch strings.ToLower(strings.TrimSpace(c.Text)) {
		case "home", "lock", "power":
			return true
		}
	}
	return false
}

// isAway: s shows SpringBoard alone in front of the flow's app, known screen or not.
func (r *Runner) isAway(s *ui.Screen) bool {
	what, _ := r.away(s)
	return what != ""
}

// resetFront forgets the app in front: each flow, and each command batch of a session (a
// person may have pressed home in between), sees it for itself.
func (r *Runner) resetFront() {
	r.frontApp = ""
	r.leftApp = false
}

// away says what is in front when it is SpringBoard alone, not the flow's app, with no alert or
// sheet of its own ("" otherwise), and whether tb knows that screen for one the app is not on:
//
//   - the home screen (its icons, SBHomeScreen...), also what a crashed app leaves;
//   - the app switcher: SpringBoard's switcher whose cards show nothing of their own (the app
//     as a picture).
//
// Any other SpringBoard tree is not judged (known false): SpringBoard also hosts system UI
// over the app in its switcher's card, such as the contacts access picker (its buttons inside
// the app's card), and Notification Center or Control Center. The step does not fail early on
// it, but a wait for an element to go does not pass on it either.
func (r *Runner) away(s *ui.Screen) (what string, known bool) {
	if s == nil || r.leftApp || r.frontApp == "" || s.Foreground() != springboard {
		return "", false
	}
	home, switcher, cardContent := false, false, false
	for _, n := range s.Nodes {
		switch {
		case n.Class == "Alert" || n.Class == "Sheet":
			return "", false
		case n.ID == "Home screen icons" || strings.HasPrefix(n.ID, "SBHomeScreen"):
			home = true
		case n.ID == "AppSwitcherContentView":
			switcher = true
		case inCard(n) && (n.Text != "" || n.Clickable || n.Class == "Button" || n.Class == "StaticText"):
			cardContent = true
		}
	}
	switch {
	case home && !switcher:
		return fmt.Sprintf("the home screen (%s) is in front, not %s", springboard, r.frontApp), true
	case switcher && !cardContent && !home:
		return fmt.Sprintf("the app switcher (%s) is in front, not %s", springboard, r.frontApp), true
	}
	return fmt.Sprintf("SpringBoard (%s) is in front, not %s", springboard, r.frontApp), false
}

// inCard: n is inside one of the app switcher's cards (below it, not the card itself).
func inCard(n *ui.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if strings.HasPrefix(p.ID, "card:") {
			return true
		}
	}
	return false
}

// awayWatch follows one lookup's reads for SpringBoard in front of the flow's app.
type awayWatch struct {
	r     *Runner
	grace time.Duration
	since time.Time     // when the first read showed it, zero while the app is in front
	quick int           // reads since then that showed it and answered as usual
	slow  time.Duration // the slowest read that showed it (0: none was slow)
	last  *ui.Screen    // the screen counted last (a cached read is not counted again)
	what  string
	// unjudged: a read showed SpringBoard with something of its own that tb does not know
	// (away's known false): no early failure until the app is back.
	unjudged bool
}

func (r *Runner) awayWatch() *awayWatch { return &awayWatch{r: r, grace: r.awayGrace()} }

// seen takes the screen in hand, and reports whether the lookup should fail now: SpringBoard
// has been in front for the grace, over two reads at least, none of them slow.
func (a *awayWatch) seen(s *ui.Screen) bool {
	what, known := a.r.away(s)
	if what == "" {
		if !a.since.IsZero() {
			a.r.tracef("the app is in front again after %s", since(a.since))
		}
		*a = awayWatch{r: a.r, grace: a.grace}
		return false
	}
	if a.since.IsZero() {
		a.since = time.Now()
		if known {
			a.r.tracef("%s: waiting up to %s for it to come back", what, a.grace)
		}
	}
	if !known && !a.unjudged {
		a.unjudged = true
		a.r.tracef("%s, showing something of its own (system UI over the app?): not judged, the step waits its full time", what)
	}
	a.what = what
	if s != a.last {
		a.last = s
		a.r.mu.Lock()
		took := a.r.screenTook
		if a.r.screen != s {
			took = 0
		}
		a.r.mu.Unlock()
		if took < awayRead {
			a.quick++
		} else {
			if a.slow == 0 {
				a.r.tracef("that read took %.1f s: the device is slow to answer, so the step waits its full time", took.Seconds())
			}
			a.slow = max(a.slow, took)
		}
	}
	return a.due() && a.quick >= 2
}

// active: the last read showed SpringBoard in front of the flow's app.
func (a *awayWatch) active() bool { return !a.since.IsZero() }

// due: SpringBoard has been in front for the grace, read as usual: a fresh read decides.
func (a *awayWatch) due() bool {
	return !a.since.IsZero() && a.slow == 0 && !a.unjudged && time.Since(a.since) >= a.grace
}

// wake is when the lookup should read again to decide, if before deadline.
func (a *awayWatch) wake(deadline time.Time) time.Time {
	if a.since.IsZero() || a.slow > 0 || a.unjudged {
		return deadline
	}
	if w := a.since.Add(a.grace); w.After(time.Now()) && w.Before(deadline) {
		return w
	}
	return deadline
}

// message is the away text for the step's failure: what is in front, and how the reads that
// showed it went when the device was slow.
func (a *awayWatch) message() string {
	if a.since.IsZero() {
		return ""
	}
	m := a.what
	if a.slow > 0 {
		m += fmt.Sprintf(" (as read while the device was slow to answer: up to %.1f s for a read)", a.slow.Seconds())
	}
	return m
}

// nameAway puts first in a failed lookup's message that SpringBoard, not the flow's app, was in
// front (issue #142), once.
func nameAway(err error) error {
	var nf *NotFound
	if err == nil || !errors.As(err, &nf) || nf.away == "" {
		return err
	}
	nf.Msg = nf.away + ": " + nf.Msg
	nf.away = ""
	return err
}

// assertionFalse words a condition assertion's lookup failure as the assertion's (the away
// text nameAway puts first is kept).
func assertionFalse(err error, c *flow.Condition) error {
	var nf *NotFound
	if errors.As(err, &nf) {
		nf.Msg = "Assertion is false: " + describeCondition(c)
	}
	return err
}
