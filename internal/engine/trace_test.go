package engine

import (
	"bytes"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// syncBuf is a bytes.Buffer safe for the trace's concurrent writers.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// traced runs yaml on f with a trace and returns the flow's result and its trace.
func traced(t *testing.T, f *fakeDriver, yaml string) (FlowResult, string) {
	t.Helper()
	var buf syncBuf
	o := quick
	o.Trace = NewTrace(&buf)
	res := runWith(t, f, o, yaml)
	return res, buf.String()
}

// wantLines checks that each pattern matches a line of the trace, in order.
func wantLines(t *testing.T, trace string, patterns ...string) {
	t.Helper()
	lines := strings.Split(trace, "\n")
	i := 0
	for _, p := range patterns {
		re := regexp.MustCompile(p)
		for i < len(lines) && !re.MatchString(lines[i]) {
			i++
		}
		if i == len(lines) {
			t.Errorf("no line matching %q (in order) in the trace:\n%s", p, trace)
			return
		}
		i++
	}
}

// Each line: seconds since the start, the role, indentation by depth; several lines keep it.
func TestTracePrintf(t *testing.T) {
	var buf bytes.Buffer
	tr := NewTrace(&buf)
	tr.Printf("", 0, "top")
	tr.Printf("alice", 2, "nested %d", 7)
	tr.Printf("", 1, "two\nlines\n")
	got := buf.String()
	for _, re := range []string{
		`^ +\d+\.\d{3}s top\n`,
		`\n +\d+\.\d{3}s \[alice\]     nested 7\n`,
		`\n +\d+\.\d{3}s   two\n {12}lines\n$`,
	} {
		if !regexp.MustCompile(re).MatchString(got) {
			t.Errorf("trace %q does not match %q", got, re)
		}
	}
	var none *Trace
	none.Printf("", 0, "a nil trace writes nothing") // no panic
}

// A passing tap: the command, the element found, where the tap went and what it did.
func TestTraceTap(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static(btn("Sign in", "login", 300))}
	res, tr := traced(t, f, "- tapOn: Sign in\n")
	mustPass(t, res)
	wantLines(t, tr,
		`^ +\d+\.\d+s ▶ Tap on "Sign in"  \(flow\.yaml:3\)$`,
		`^ +\d+\.\d+s   found "Sign in": .*"Sign in" #login \[`,
		`^ +\d+\.\d+s   tap at \(\d+, \d+\): the screen changed \(sent in \d+ ms, waited \d+ ms\)$`,
		`^ +\d+\.\d+s ✓ Tap on "Sign in"  \d+ ms$`)
}

// notingDriver is a driver with things to say (web: a JavaScript dialog it answered).
type notingDriver struct {
	*fakeDriver
	notes []string
}

func (n *notingDriver) Notes() []string {
	out := n.notes
	n.notes = nil
	return out
}

// What the driver did on its own during a command is in the trace, under the command
// (issue #76: the web driver answering a JavaScript alert must not be silent).
func TestTraceDriverNotes(t *testing.T) {
	t.Parallel()
	f := &notingDriver{fakeDriver: &fakeDriver{screen: static(btn("Sign in", "login", 300))},
		notes: []string{`web: the page opened a JavaScript alert "Saved!": tb answered OK`}}
	var buf syncBuf
	o := quick
	o.Trace = NewTrace(&buf)
	mustPass(t, runWith(t, f, o, "- tapOn: Sign in\n"))
	wantLines(t, buf.String(),
		`^ +\d+\.\d+s ▶ Tap on "Sign in"`,
		`^ +\d+\.\d+s   web: the page opened a JavaScript alert "Saved!": tb answered OK$`,
		`^ +\d+\.\d+s ✓ Tap on "Sign in"`)
}

// A failed lookup says what was looked for, for how long, and what on screen looked like it.
func TestTraceNotFound(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static(btn("Sign in", "", 300), btn("Settings", "", 400))}
	res, tr := traced(t, f, "- tapOn: Sign up\n")
	mustFail(t, res, "Element not found")
	wantLines(t, tr,
		`▶ Tap on "Sign up"`,
		`   not found: "Sign up", after \d+ ms and \d+ reads \(screen: .*elements, allowed`,
		`   similar on screen: .*"Sign in"`,
		`✗ Tap on "Sign up"  \d+ ms: Element not found: "Sign up"`)
}

// Skipped conditions, retries and nested flows show why and at what depth.
func TestTraceConditionsAndRetries(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static(btn("Home", "", 100))}
	res, tr := traced(t, f, `- runFlow:
    when:
      visible: Promo
    commands:
      - tapOn: Promo
- retry:
    maxRetries: 1
    commands:
      - assertVisible: Missing
`)
	mustFail(t, res, "Missing")
	wantLines(t, tr,
		`▶ Run flow when "Promo" is visible`,
		`^ +\d+\.\d+s   condition not met \("Promo" is visible\): skipped$`,
		`○ Run flow when "Promo" is visible`,
		`▶ Retry`,
		`^ +\d+\.\d+s   ▶ Assert that "Missing" is visible`,
		`retry: attempt 1 of 2 failed`,
		`✗ Retry`)
}

// A disabled button that turns on, and an action the device reports did nothing.
func TestTraceDisabledAndNoChange(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{tapSettle: driver.NoChange}
	var on bool
	var mu sync.Mutex
	f.screen = func() []*ui.Node {
		mu.Lock()
		defer mu.Unlock()
		b := btn("Pay", "", 300)
		b.Enabled = on
		return []*ui.Node{b}
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		mu.Lock()
		on = true
		mu.Unlock()
		f.change(true)
	}()
	res, tr := traced(t, f, "- tapOn: Pay\n")
	mustPass(t, res)
	wantLines(t, tr,
		`it is disabled: waiting up to 3s for it to turn on`,
		`it turned on after \d+ ms`,
		`tap at \(\d+, \d+\): nothing changed`)
}

// The trace's scroll lines: each drag and how it ended.
func TestTraceScroll(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	f.scroller(40, false, 0)
	res, tr := traced(t, f, "- scrollUntilVisible: {element: Item 30, timeout: 5000}\n")
	mustPass(t, res)
	wantLines(t, tr,
		`drag 1: \(\d+, \d+\) -> \(\d+, \d+\); target not on screen yet, \d+ texts seen`,
		`in view after \d+ swipes, \d+ ms: .*"Item 30"`)
}
