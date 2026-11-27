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

const springboard = "com.apple.springboard"

var awayRead = 2 * time.Second

func (r *Runner) awayGrace() time.Duration {
	if r.opts.AwayGrace > 0 {
		return r.opts.AwayGrace
	}
	if ci.On() {
		return 10 * time.Second
	}
	return 5 * time.Second
}

func (r *Runner) noteFront(s *ui.Screen) {
	if s == nil {
		return
	}
	if fg := s.Foreground(); fg != "" && fg != springboard {
		r.frontApp = fg
		r.leftApp = false
	}
}

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

func (r *Runner) isAway(s *ui.Screen) bool {
	what, _ := r.away(s)
	return what != ""
}

func (r *Runner) resetFront() {
	r.frontApp = ""
	r.leftApp = false
}

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

func inCard(n *ui.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if strings.HasPrefix(p.ID, "card:") {
			return true
		}
	}
	return false
}

type awayWatch struct {
	r        *Runner
	grace    time.Duration
	since    time.Time
	quick    int
	slow     time.Duration
	last     *ui.Screen
	what     string
	unjudged bool
}

func (r *Runner) awayWatch() *awayWatch { return &awayWatch{r: r, grace: r.awayGrace()} }

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

func (a *awayWatch) active() bool { return !a.since.IsZero() }

func (a *awayWatch) due() bool {
	return !a.since.IsZero() && a.slow == 0 && !a.unjudged && time.Since(a.since) >= a.grace
}

func (a *awayWatch) wake(deadline time.Time) time.Time {
	if a.since.IsZero() || a.slow > 0 || a.unjudged {
		return deadline
	}
	if w := a.since.Add(a.grace); w.After(time.Now()) && w.Before(deadline) {
		return w
	}
	return deadline
}

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

func nameAway(err error) error {
	var nf *NotFound
	if err == nil || !errors.As(err, &nf) || nf.away == "" {
		return err
	}
	nf.Msg = nf.away + ": " + nf.Msg
	nf.away = ""
	nf.moved = true
	return err
}

func assertionFalse(err error, c *flow.Condition) error {
	var nf *NotFound
	if errors.As(err, &nf) {
		nf.Msg = "Assertion is false: " + describeCondition(c)
	}
	return err
}
