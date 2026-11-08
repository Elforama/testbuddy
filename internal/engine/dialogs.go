package engine

import (
	"context"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// systemAlertOf is the system alert holding n (an iOS permission prompt, which SpringBoard
// shows in a system window), or nil.
func systemAlertOf(s *ui.Screen, n *ui.Node) *ui.Node {
	if s == nil || n == nil || !systemWindow(s, n) {
		return nil
	}
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Class == "Alert" {
			return p
		}
	}
	return nil
}

// System alerts ignore touches while they animate in, so a tap made just as one appears can be
// lost, and the alert then stays up and blocks every later step (and flow). A system alert's
// buttons always close it, so one still showing the same button after the tap means the tap
// was lost: tap again, up to alertRetries times. "Still showing" takes two reads at least
// alertRecheck apart, so an alert that is closing (and still listed for a moment) is not
// tapped again: that tap would land on the app behind it.
const (
	alertRetries = 3
	alertRecheck = 300 * time.Millisecond
)

func (r *Runner) confirmAlertTap(ctx context.Context, alert, button *ui.Node, x, y int, o driver.ActionOpts) error {
	for try := 1; ; try++ {
		r.Drv.WaitSettled(ctx, 2*time.Second)
		if !r.alertStillUp(ctx, alert, button) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(alertRecheck * time.Duration(try)):
		}
		if !r.alertStillUp(ctx, alert, button) {
			return nil
		}
		if try > alertRetries {
			r.tracef("the system alert %q is still up after %d taps on %q: leaving it", alert.Label(), try, button.Label())
			return nil
		}
		r.tracef("the system alert %q is still up: the tap on %q was lost (alerts ignore touches while they appear); tapping again", alert.Label(), button.Label())
		r.invalidate()
		res, err := r.Drv.Tap(ctx, x, y, o)
		if err != nil {
			return err
		}
		r.tracef("tap at (%d, %d): %s", x, y, actionDesc(res))
	}
}

// closeStaleAlerts closes system alerts still up before a launch that restarts the app (iOS).
// One left from before (a permission prompt the last flow did not answer: it ended, or failed,
// first) outlives the app that raised it, even a reinstall, and would take every touch of the
// flow being started, so this flow, and each after it, would fail. Nothing the new launch does
// can have raised it yet. Each is declined (its "Don't Allow" or "Cancel", else its first
// button): launchApp sets the permissions the flow wants itself.
func (r *Runner) closeStaleAlerts(ctx context.Context) error {
	if r.Drv.Platform() == "android" {
		// The system dialogs a lookup answers (an autofill save sheet left up, an "isn't
		// responding" dialog) would keep the app from coming to the front.
		for i := 0; i < maxDialogAnswers; i++ {
			r.invalidate()
			s, err := r.read(ctx)
			if err != nil {
				return nil
			}
			if tapped, err := r.answerDialog(ctx, s); err != nil || !tapped {
				return err
			}
			r.Drv.WaitSettled(ctx, 2*time.Second)
		}
		return nil
	}
	if r.Drv.Platform() != "ios" {
		return nil
	}
	for i := 0; i < maxDialogAnswers; i++ {
		r.invalidate()
		s, err := r.read(ctx)
		if err != nil {
			return nil // nothing to go on: launch as before
		}
		var alert, button *ui.Node
		for _, n := range s.Nodes {
			if n.Class == "Alert" && systemWindow(s, n) {
				alert = n
				break
			}
		}
		if alert == nil {
			return nil
		}
		alert.Walk(func(n *ui.Node) bool {
			if n.Class != "Button" {
				return true
			}
			l := strings.ToLower(strings.ReplaceAll(n.Label(), "’", "'"))
			if strings.Contains(l, "don't allow") || l == "cancel" || l == "not now" || l == "deny" {
				button = n
				return false
			}
			if button == nil {
				button = n
			}
			return true
		})
		if button == nil {
			r.tracef("a system alert %q from before is still up, with no button to close it", alert.Label())
			return nil
		}
		r.tracef("a system alert from before is still up: %q: tapping %q before launching", alert.Label(), button.Label())
		if _, err := r.Drv.Tap(ctx, button.Bounds.CX(), button.Bounds.CY(), driver.ActionOpts{Hold: iosFrameHold}); err != nil {
			return err
		}
		r.Drv.WaitSettled(ctx, 2*time.Second)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(alertRecheck):
		}
	}
	return nil
}

// systemWindow: n is in a window of the system's, not an app's: a system window, or
// SpringBoard's (iOS lists SpringBoard as the app when no app is in front, with its alerts).
func systemWindow(s *ui.Screen, n *ui.Node) bool {
	if n.Window < 0 || n.Window >= len(s.Windows) {
		return false
	}
	w := s.Windows[n.Window]
	return w.Type == "system" || w.Package == "com.apple.springboard"
}

// alertStillUp: a fresh read still shows the alert with the button, where it was.
func (r *Runner) alertStillUp(ctx context.Context, alert, button *ui.Node) bool {
	r.invalidate()
	s, err := r.read(ctx)
	if err != nil {
		return false
	}
	for _, n := range s.Nodes {
		if n.Class != button.Class || n.Label() != button.Label() || n.Bounds != button.Bounds {
			continue
		}
		if a := systemAlertOf(s, n); a != nil && a.Label() == alert.Label() {
			return true
		}
	}
	return false
}

// System dialogs that take over the screen whatever the flow does, answered when a lookup
// reads one (as a person running the test would):
//
//   - Android's "isn't responding" and "keeps stopping" dialogs. A freshly booted emulator's
//     launcher, or a slow app start, often stalls long enough to raise one; it is modal and
//     takes the app's taps and keys until answered. The first time a dialog shows, Wait (a
//     slow start is no failure); if the same one comes back, Close app. Their button ids are
//     the same in every language.
//   - Android's autofill "Save password?" sheet (Google Password Manager, or another autofill
//     service), raised when a login form is left after typing a password. It covers the app
//     and takes its taps, and outlives the app: declined ("Not now", android:id/autofill_save_no).
//   - iOS's "Would Like to Send You Notifications" alert. Simulators have no way to grant
//     notifications ahead (no privacy database entry), so it shows on the app's first request;
//     it is answered as launchApp's permissions say (`notifications`, else `all`; the
//     default grants all). `unset` leaves it to the flow.

// maxDialogAnswers bounds the dialogs answered during one lookup: one that will not go away
// is left on screen for the flow to fail on.
const maxDialogAnswers = 3

// answerDialog answers a system dialog on s, if there is one. It returns true when it tapped.
func (r *Runner) answerDialog(ctx context.Context, s *ui.Screen) (bool, error) {
	var button *ui.Node
	var what string
	switch s.Platform {
	case "android":
		var wait, closeApp *ui.Node
		title := ""
		for _, n := range s.Nodes {
			if n.ID == "android:id/autofill_save_no" {
				button, what = n, "autofill save sheet"
				break
			}
		}
		if button != nil {
			break
		}
		for _, n := range s.Nodes {
			switch n.ID {
			case "android:id/aerr_wait":
				wait = n
			case "android:id/aerr_close":
				closeApp = n
			case "android:id/alertTitle":
				title = n.Text
			}
		}
		if wait == nil && closeApp == nil {
			return false, nil
		}
		button = wait
		if button == nil || (r.dialogsSeen[title] && closeApp != nil) {
			button = closeApp
		}
		if r.dialogsSeen == nil {
			r.dialogsSeen = map[string]bool{}
		}
		r.dialogsSeen[title] = true
		what = title
	case "ios":
		answer := r.notifications
		if answer != "allow" && answer != "deny" {
			return false, nil
		}
		for _, w := range s.Windows {
			alert := false
			var buttons []*ui.Node
			for _, n := range w.Nodes {
				if strings.Contains(strings.ToLower(n.Desc+" "+n.Text), "would like to send you notifications") {
					alert = true
				}
				if n.Class == "Button" {
					buttons = append(buttons, n)
				}
			}
			if !alert || len(buttons) == 0 {
				continue
			}
			for _, b := range buttons {
				label := strings.ToLower(strings.ReplaceAll(b.Label(), "’", "'"))
				if answer == "allow" && label == "allow" || answer == "deny" && strings.Contains(label, "don't allow") {
					button = b
					break
				}
			}
			if button == nil { // iOS lists "Don't Allow" first, "Allow" second
				button = buttons[0]
				if answer == "allow" && len(buttons) > 1 {
					button = buttons[1]
				}
			}
			what = "notifications alert"
			break
		}
	}
	if button == nil {
		return false, nil
	}
	r.tracef("system dialog: %s: tapping %q", what, button.Label())
	r.invalidate()
	if _, err := r.Drv.Tap(ctx, button.Bounds.CX(), button.Bounds.CY(), driver.ActionOpts{}); err != nil {
		return false, err
	}
	return true, nil
}
