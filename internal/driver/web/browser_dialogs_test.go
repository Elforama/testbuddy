package web

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// JavaScript dialogs (issue #76): alert(), confirm(), prompt() and a beforeunload "Leave
// site?" block the page's script until they are answered, and nothing answered them, so the
// tap that opened one, and every command after it, hung until its timeout. tb answers each
// one as it opens (OK; a prompt with its default text; leave the page) and says so in the
// run's trace.
const dialogsPage = `<!doctype html><html><body style="margin:0">
<button id="alert" style="width:200px;height:50px" onclick="alert('Saved!'); this.textContent='Alert answered'">Alert</button>
<button id="confirm" style="width:200px;height:50px" onclick="this.textContent = confirm('Delete it?') ? 'Confirmed' : 'Cancelled'">Confirm</button>
<button id="prompt" style="width:200px;height:50px" onclick="this.textContent = 'Name: ' + prompt('Your name?', 'Ada')">Prompt</button>
<script>
window.addEventListener('beforeunload', e => { e.preventDefault(); e.returnValue = ''; });
</script>
</body></html>`

func TestPageJSDialogs(t *testing.T) {
	d, ctx := openPage(t, dialogsPage)
	notes := func() string {
		n, ok := any(d).(interface{ Notes() []string })
		if !ok {
			return ""
		}
		return strings.Join(n.Notes(), "\n")
	}
	var said []string // every note so far (Notes hands each one out once)
	saw := func(s string) bool {
		said = append(said, notes())
		return strings.Contains(strings.Join(said, "\n"), s)
	}
	// within runs a command with its own deadline: a dialog nobody answers hangs it.
	within := func(what string, do func(ctx context.Context) error) {
		t.Helper()
		cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		start := time.Now()
		if err := do(cctx); err != nil {
			t.Fatalf("%s: %v after %v (a JavaScript dialog left open blocks the page)", what, err, time.Since(start).Round(time.Millisecond))
		}
	}
	for _, c := range []struct{ id, want, note string }{
		{"alert", "Alert answered", `alert "Saved!"`},
		{"confirm", "Confirmed", `confirm "Delete it?"`},
		{"prompt", "Name: Ada", `prompt "Your name?"`},
	} {
		s := screenWith(t, d, ctx, c.id)
		btn := find(s, c.id)
		if btn == nil {
			t.Fatalf("no #%s", c.id)
		}
		within("tap on #"+c.id, func(ctx context.Context) error {
			_, err := d.Tap(ctx, btn.Bounds.CX(), btn.Bounds.CY(), driver.ActionOpts{})
			return err
		})
		within("screen read after #"+c.id, func(ctx context.Context) error {
			s, err := d.Screen(ctx)
			if err == nil {
				if n := find(s, c.id); n == nil || n.Text != c.want {
					t.Errorf("#%s after its dialog: %+v, want %q", c.id, n, c.want)
				}
			}
			return err
		})
		within("screenshot after #"+c.id, func(ctx context.Context) error {
			_, err := d.Screenshot(ctx, 1, "png")
			return err
		})
		if !saw(c.note) {
			t.Errorf("the trace does not say the page opened the %s: %q", c.note, said)
		}
	}

	// Leaving a page whose beforeunload asks first (the taps above gave it the user activation
	// Chrome requires for that dialog).
	var href string
	within("reading the page's address", func(ctx context.Context) (err error) {
		href, err = d.Foreground(ctx)
		return err
	})
	within("opening another page past beforeunload", func(ctx context.Context) error {
		return d.LaunchApp(ctx, strings.TrimSuffix(href, "/")+"/next", driver.LaunchOpts{})
	})
	within("reading the new page's address", func(ctx context.Context) error {
		href, err := d.Foreground(ctx)
		if err == nil && !strings.HasSuffix(href, "/next") {
			t.Errorf("still on %s: the page was not left", href)
		}
		return err
	})
	if !saw("beforeunload") {
		t.Errorf("the trace does not say the page asked before it was left: %q", said)
	}
}
