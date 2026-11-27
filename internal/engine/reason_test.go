package engine

import (
	"errors"
	"fmt"
	"testing"
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
		{"assertVisible", &NotFound{FlowError: FlowError{Msg: "Assertion is false: \"Buy\" is visible"}}, ReasonAssertionFailed},
		{"assertion on an app that went away", &NotFound{FlowError: FlowError{Msg: "Assertion is false: \"Buy\" is visible"}, away: "the app left the foreground"}, ReasonScreenChanged},
		{"condition", failf("Assertion is false: %s", "\"Buy\" is not visible"), ReasonAssertionFailed},
		{"flow error", failf("invalid timeout %q", "x"), ReasonOther},
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
