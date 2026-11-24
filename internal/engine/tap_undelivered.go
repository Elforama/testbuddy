package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/ci"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

const maxTapReplays = 2

const deliveryWait = 1500 * time.Millisecond

func (r *Runner) undelivered(ctx context.Context, c *flow.Command, res driver.ActionResult, tapped *ui.Node, on *ui.Screen) bool {
	dc, ok := r.Drv.(driver.DeliveryChecker)
	if !ok || !ci.On() || res.DeliveryToken == 0 || c.LongPress || (c.Repeat > 1 && c.Name != "doubleTapOn") || c.Point != "" ||
		res.Targeted || res.Reached || res.Touch == "ended" || res.Touch == "cancelled" || (res.Clicked != nil && *res.Clicked) ||
		res.NotInjected != "" || !inAppWindow(on, tapped) {
		return false
	}
	t := time.Now()
	v, err := dc.Delivery(ctx, res.DeliveryToken, deliveryWait)
	if err != nil {
		r.tracef("asking whether the app got the touch failed: %v", err)
		return false
	}
	if time.Since(t) >= 50*time.Millisecond {
		r.traceWait(t, "for the app's word on the touch")
	}
	if v.Verdict != driver.Delivered {
		verdict := v.Verdict
		if v.Pending {
			verdict += " (no verdict in time)"
		}
		r.tracef("delivery verdict %s: %s", verdict, deliveryEvidence(v))
	}
	return v.Verdict == driver.NotDelivered
}

func (r *Runner) swipeUndelivered(ctx context.Context, token int64, n int) bool {
	dc, ok := r.Drv.(driver.DeliveryChecker)
	if !ok || !ci.On() || token == 0 {
		return false
	}
	t := time.Now()
	v, err := dc.Delivery(ctx, token, deliveryWait)
	if err != nil {
		r.tracef("asking whether the app got swipe %d's touches failed: %v", n, err)
		return false
	}
	if time.Since(t) >= 50*time.Millisecond {
		r.traceWait(t, "for the app's word on the swipe")
	}
	if v.Implicit {
		ms := 0
		if v.ReportMs != nil {
			ms = *v.ReportMs
		}
		r.tracef("swipe %d moved nothing, and whether it reached the app is unknown: neither the app nor the system reported its touches (testmanagerd confirmed them anyway %d ms after the lift): counted as delivered", n, ms)
		return false
	}
	switch v.Verdict {
	case driver.NotDelivered:
		r.tracef("swipe %d did not reach the app (%s)", n, deliveryEvidence(v))
		return true
	case driver.Delivered:
		return false
	}
	why := deliveryEvidence(v)
	if v.Pending {
		why = "no verdict in time; " + why
	}
	r.tracef("swipe %d moved nothing, and whether it reached the app is unknown (%s): counted as delivered", n, why)
	return false
}

func deliveryEvidence(v driver.DeliveryVerdict) string {
	report := "SpringBoard's report none"
	if v.ReportMs != nil {
		report = fmt.Sprintf("SpringBoard's report %d ms after the lift", *v.ReportMs)
		if v.Implicit {
			report = fmt.Sprintf("no process reported the touches, testmanagerd confirmed them anyway %d ms after the lift", *v.ReportMs)
		}
	}
	answers := "none"
	if len(v.AnswerMs) > 0 {
		parts := make([]string, len(v.AnswerMs))
		for i, ms := range v.AnswerMs {
			parts[i] = strconv.Itoa(ms)
		}
		answers = "in " + strings.Join(parts, ", ") + " ms"
	}
	return report + ", the app's idle answers " + answers
}

func inAppWindow(s *ui.Screen, n *ui.Node) bool {
	if s == nil || n == nil {
		return false
	}
	if n.Window >= 0 && n.Window < len(s.Windows) {
		w := s.Windows[n.Window]
		return w.Type != "keyboard" && w.Type != "system" && w.Package != "com.apple.springboard"
	}
	return true
}

func sameTapTarget(was *ui.Node, before *ui.Screen, next *ui.Node, now *ui.Screen, target *driver.ActionTarget) bool {
	if was == nil || next == nil || next.Class != was.Class || next.Label() != was.Label() {
		return false
	}
	if target != nil {
		t := identifiedAppTarget(next, now)
		return t != nil && t.ID == target.ID && t.Window == target.Window
	}
	if !inAppWindow(now, next) || !sameElement(was, next) {
		return false
	}
	if before != nil && was.Window >= 0 && was.Window < len(before.Windows) && next.Window >= 0 && next.Window < len(now.Windows) {
		a, b := before.Windows[was.Window], now.Windows[next.Window]
		return a.ID == b.ID && a.Type == b.Type && a.Package == b.Package
	}
	return true
}
