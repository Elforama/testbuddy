package flow

import (
	"strings"
	"testing"
)

func TestTapTimeoutParses(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`- tapOn: {text: "Not now", optional: true, timeout: 1000}`, "1000"},
		{`- tapOn: {id: skip, optional: true, timeout: 0}`, "0"},
		{`- doubleTapOn: {id: skip, timeout: 2_500}`, "2_500"},
		{`- longPressOn: {id: skip, optional: true, timeout: "${output.ms}"}`, "${output.ms}"},
		{`- tapOn: {id: skip, optional: true}`, ""},
	} {
		c := step(t, tc.body)
		if c.Timeout != tc.want {
			t.Errorf("%s: timeout %q, want %q", tc.body, c.Timeout, tc.want)
		}
	}
}

func TestTapTimeoutInvalid(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`- tapOn: {id: skip, optional: true, timeout: -5}`, `timeout "-5" is not a number of milliseconds`},
		{`- tapOn: {id: skip, optional: true, timeout: soon}`, `timeout "soon" is not a number of milliseconds`},
		{`- tapOn: {id: skip, optional: true, timeout: [1]}`, "timeout is a number of milliseconds"},
		{`- tapOn: {point: "50%,50%", timeout: 100}`, "a point tap looks for none"},
		{`- tapOn: {id: skip, optional: true, timeout: NaN}`, `timeout "NaN" is not a number of milliseconds`},
		{`- tapOn: {id: skip, optional: true, timeout: Inf}`, `timeout "Inf" is not a number of milliseconds`},
		{`- tapOn: {id: skip, optional: true, timeout: "+Infinity"}`, `timeout "+Infinity" is not a number of milliseconds`},
	} {
		_, err := parse(t, hdr+tc.body)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %v, want it to contain %q", tc.body, err, tc.want)
		}
	}
}
