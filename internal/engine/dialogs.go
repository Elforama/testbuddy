package engine

import (
	"context"
	"strings"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// System dialogs that take over the screen whatever the flow does, answered when a lookup
// reads one (as a person running the test would):
//
//   - Android's "isn't responding" and "keeps stopping" dialogs. A freshly booted emulator's
//     launcher, or a slow app start, often stalls long enough to raise one; it is modal and
//     takes the app's taps and keys until answered. The first time a dialog shows, Wait (a
//     slow start is no failure); if the same one comes back, Close app. Their button ids are
//     the same in every language.
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
