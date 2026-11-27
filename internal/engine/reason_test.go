package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

func TestReason(t *testing.T) {
	away := &NotFound{FlowError: FlowError{Msg: "Element not found: \"Buy\""}, away: "the app left the foreground"}
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"not found", &NotFound{FlowError: FlowError{Msg: "Element not found: \"Buy\""}}, ReasonNotFound},
		{"wrapped not found", fmt.Errorf("step 2: %w", &NotFound{FlowError: FlowError{Msg: "Element not found: id: buy"}}), ReasonNotFound},
		{"replaced since the last look", &NotFound{FlowError: FlowError{Msg: "\"Buy\" (in your last look) is gone"}, moved: true}, ReasonScreenChanged},
		{"screen left while scrolling back", &NotFound{FlowError: FlowError{Msg: "Element not found: \"Buy\""}, left: true}, ReasonScreenChanged},
		{"app went away", nameAway(away), ReasonScreenChanged},
		{"flow error", failf("invalid timeout %q", "x"), ReasonOther},
		{"a message that only reads like an assertion", failf("Assertion is false: said a script"), ReasonOther},
		{"device error", errors.New("device offline"), ReasonOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Reason(c.err); got != c.want {
				t.Errorf("Reason(%v) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}

func runReasonSteps(t *testing.T, f *fakeDriver, stalled bool, src string) error {
	t.Helper()
	cmds, err := flow.ParseCommands(".", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	o := Options{LookupTimeout: 300 * time.Millisecond, StallFloor: 100 * time.Millisecond}
	if stalled {
		return New(&stallDriver{fakeDriver: f, all: true, hold: 400 * time.Millisecond}, o).RunCommands(ctx, cmds, "app", nil)
	}
	return New(f, o).RunCommands(ctx, cmds, "app", nil)
}

func otherScreen() *fakeDriver {
	f := &fakeDriver{plat: "ios"}
	f.screen = func() []*ui.Node {
		return []*ui.Node{{Text: "Other", Visible: true, Bounds: ui.Rect{L: 0, T: 100, R: 100, B: 140}}}
	}
	return f
}

func TestReasonOfAnAssertionSurvivesTheStallNaming(t *testing.T) {
	for _, src := range []string{
		"- assertVisible: Save",
		"- assertNotVisible: Other",
		"- extendedWaitUntil: {visible: Save, timeout: 300}",
		"- extendedWaitUntil: {notVisible: Other, timeout: 300}",
	} {
		t.Run(src, func(t *testing.T) {
			err := runReasonSteps(t, otherScreen(), true, src)
			if err == nil || !contains(err.Error(), "the step's own failure: Assertion is false") {
				t.Fatalf("the stall was not named: %v", err)
			}
			if got := Reason(err); got != ReasonAssertionFailed {
				t.Errorf("Reason = %q, want %q for %v", got, ReasonAssertionFailed, err)
			}
		})
	}
}

func TestReasonOfAnAbsenceThatCouldNotBeChecked(t *testing.T) {
	f := &fakeDriver{screen: static()}
	f.noApp.Store(true)
	f.unread.Store(1)
	err := runReasonSteps(t, f, false, "- assertNotVisible: Home")
	if err == nil || !contains(err.Error(), "Could not check") {
		t.Fatalf("err = %v", err)
	}
	if got := Reason(err); got != ReasonOther {
		t.Errorf("Reason = %q, want %q", got, ReasonOther)
	}
}

func TestFailedVisibleConditionNamesWhatItLookedFor(t *testing.T) {
	cases := []struct {
		name, src, query, id string
		notFound             bool
	}{
		{"visible text", "- extendedWaitUntil: {visible: Save, timeout: 100}", "Save", "", true},
		{"visible id", "- extendedWaitUntil: {visible: {id: save}, timeout: 100}", "", "save", true},
		{"in a runFlow", "- runFlow:\n    commands:\n      - extendedWaitUntil: {visible: Save, timeout: 100}", "Save", "", true},
		{"the notVisible half failed", "- extendedWaitUntil: {visible: Other, notVisible: Other, timeout: 100}", "", "", false},
		{"notVisible", "- extendedWaitUntil: {notVisible: Other, timeout: 100}", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := runReasonSteps(t, otherScreen(), false, c.src)
			if err == nil || !contains(err.Error(), "Assertion is false: ") {
				t.Fatalf("err = %v", err)
			}
			if got := Reason(err); got != ReasonAssertionFailed {
				t.Errorf("Reason = %q", got)
			}
			var nf *NotFound
			if errors.As(err, &nf) != c.notFound {
				t.Fatalf("NotFound = %v, want %v: %v", !c.notFound, c.notFound, err)
			}
			if c.notFound && (nf.Query != c.query || nf.ID != c.id || nf.Screen == nil) {
				t.Errorf("query %q id %q screen %v, want %q %q and the screen", nf.Query, nf.ID, nf.Screen != nil, c.query, c.id)
			}
		})
	}
}

func TestFailedVisibleConditionStaysOptionalAndRetried(t *testing.T) {
	res := runWith(t, otherScreen(), quick, "- extendedWaitUntil: {visible: Save, timeout: 100, optional: true}\n- assertVisible: Other")
	if res.Status != Passed || len(res.Steps) != 2 || res.Steps[0].Status != Warned {
		t.Errorf("optional wait: %s %+v", res.Status, res.Steps)
	}
	f := otherScreen()
	res = runWith(t, f, quick, "- retry:\n    maxRetries: 1\n    commands:\n      - inputText: a\n      - extendedWaitUntil: {visible: Save, timeout: 100}")
	mustFail(t, res, "Assertion is false: \"Save\" is visible")
	if len(f.typed) != 2 {
		t.Errorf("the retry block ran %d times, want 2", len(f.typed))
	}
}
