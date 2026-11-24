package ios

import (
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/driver"
)

func TestTapTracesTheWaitForTheAppToTakeInput(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer map[string]any
		want   string
	}{
		{"waited", map[string]any{"result": "changed", "seq": 5, "awaitedInputMs": 240}, "tap: the app was coming to the foreground: waited 240 ms"},
		{"still entering", map[string]any{"result": "changed", "seq": 5, "awaitedInputMs": 3000, "appActive": false}, "tap: the app was still coming to the foreground after 3000 ms; going on anyway"},
		{"a short wait", map[string]any{"result": "changed", "seq": 5, "awaitedInputMs": 12}, ""},
		{"none", map[string]any{"result": "changed", "seq": 5}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newFakeRunner(t)
			r.on("tap", func(map[string]any) map[string]any { return c.answer })
			d := newIOSDriver(t, r)
			if _, err := d.Tap(ctxT(t), 100, 200, driver.ActionOpts{}); err != nil {
				t.Fatal(err)
			}
			notes := strings.Join(d.Notes(), "\n")
			if (c.want == "") != (notes == "") || !strings.Contains(notes, c.want) {
				t.Errorf("notes %q, want %q", notes, c.want)
			}
		})
	}
}
