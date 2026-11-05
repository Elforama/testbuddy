package android

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// Window type numbers from the protocol map to the ui package's names; unknown ones are "other".
func TestWindowType(t *testing.T) {
	for in, want := range map[int]string{1: "app", 2: "keyboard", 3: "system", 4: "overlay", 0: "other", 5: "other", -1: "other", 99: "other"} {
		if got := windowType(in); got != want {
			t.Errorf("windowType(%d) = %q, want %q", in, got, want)
		}
	}
}

// Each protocol flag bit sets exactly its own Node field (focusable, 1024, has none).
func TestConvertFlags(t *testing.T) {
	field := func(n *ui.Node) map[string]bool {
		return map[string]bool{"Clickable": n.Clickable, "Enabled": n.Enabled, "Focused": n.Focused, "Scrollable": n.Scrollable,
			"Checked": n.Checked, "Selected": n.Selected, "Checkable": n.Checkable, "Password": n.Password, "Editable": n.Editable,
			"LongClickable": n.LongClickable, "ShowingHint": n.ShowingHint, "Visible": n.Visible}
	}
	cases := []struct {
		bit  int
		want string
	}{
		{1, "Clickable"}, {2, "Enabled"}, {4, "Focused"}, {8, "Scrollable"}, {16, "Checked"}, {32, "Selected"},
		{64, "Checkable"}, {128, "Password"}, {256, "Editable"}, {512, "LongClickable"}, {1024, ""},
		{2048, "ShowingHint"}, {4096, "Visible"},
	}
	for _, c := range cases {
		got := field(convert(rawNode{F: c.bit}))
		for name, v := range got {
			if v != (name == c.want) {
				t.Errorf("flag %d: %s = %v", c.bit, name, v)
			}
		}
	}
	all := field(convert(rawNode{F: 8191}))
	for name, v := range all {
		if !v {
			t.Errorf("all flags: %s not set", name)
		}
	}
	for name, v := range field(convert(rawNode{})) {
		if v {
			t.Errorf("no flags: %s set", name)
		}
	}
}

// convert copies every string field and the bounds of a protocol node.
func TestConvertFields(t *testing.T) {
	n := convert(rawNode{D: 3, B: [4]int{10, 20, 110, 220}, C: "android.widget.EditText", T: "hello", CD: "desc",
		ID: "com.app:id/name", H: "Your name", E: "Required", SD: "on"})
	want := ui.Node{Depth: 3, Bounds: ui.Rect{L: 10, T: 20, R: 110, B: 220}, Class: "android.widget.EditText", Text: "hello",
		Desc: "desc", ID: "com.app:id/name", Hint: "Your name", Error: "Required", State: "on"}
	if !reflect.DeepEqual(*n, want) {
		t.Errorf("convert = %+v\nwant %+v", *n, want)
	}
	if n.ShortClass() != "EditText" {
		t.Errorf("ShortClass = %q", n.ShortClass())
	}
}

// actionParams adds the settle params: noChange defaults to 1000 ms, react/settle/timeout only when asked.
func TestActionParams(t *testing.T) {
	cases := []struct {
		name string
		o    driver.ActionOpts
		want map[string]any
	}{
		{"defaults", driver.ActionOpts{}, map[string]any{"x": 1, "noChange": 1000}},
		{"no settle", driver.ActionOpts{NoSettle: true}, map[string]any{"x": 1, "settle": false, "noChange": 1000}},
		{"react", driver.ActionOpts{React: true}, map[string]any{"x": 1, "react": true, "noChange": 1000}},
		{"noChange", driver.ActionOpts{NoChangeMs: 250}, map[string]any{"x": 1, "noChange": 250}},
		{"timeout", driver.ActionOpts{Timeout: 2500 * time.Millisecond}, map[string]any{"x": 1, "noChange": 1000, "timeout": int64(2500)}},
		{"hold and drag are not settle params", driver.ActionOpts{Hold: time.Second, Drag: true}, map[string]any{"x": 1, "noChange": 1000}},
		{"everything", driver.ActionOpts{NoSettle: true, React: true, NoChangeMs: 40, Timeout: time.Second},
			map[string]any{"x": 1, "settle": false, "react": true, "noChange": 40, "timeout": int64(1000)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := map[string]any{"x": 1}
			got := actionParams(c.o, p)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
			if reflect.ValueOf(got).Pointer() != reflect.ValueOf(p).Pointer() {
				t.Error("actionParams should fill the map it was given")
			}
		})
	}
}

// KeyCode accepts flow key names in any case, with spaces or underscores, trimmed.
func TestKeyCode(t *testing.T) {
	cases := []struct {
		in   string
		code int
		ok   bool
	}{
		{"enter", 66, true}, {"Enter", 66, true}, {" ENTER ", 66, true},
		{"backspace", 67, true}, {"back", 4, true}, {"home", 3, true}, {"lock", 26, true}, {"power", 26, true},
		{"volume up", 24, true}, {"volume_up", 24, true}, {"VOLUME_DOWN", 25, true},
		{"tab", 61, true}, {"escape", 111, true},
		{"remote_dpad_up", 19, true}, {"Remote Dpad Center", 23, true}, {"remote media play pause", 85, true},
		{"remote_tv_input_hdmi_3", 245, true}, {"remote system navigation down", 281, true},
		{"delete", 0, false}, {"", 0, false}, {"volume  up", 0, false}, {"f13", 0, false},
	}
	for _, c := range cases {
		code, ok := KeyCode(c.in)
		if code != c.code || ok != c.ok {
			t.Errorf("KeyCode(%q) = %d, %v; want %d, %v", c.in, code, ok, c.code, c.ok)
		}
	}
}

// Every key in the table resolves from its upper-case, underscored spelling too.
func TestKeyCodeTableRoundTrip(t *testing.T) {
	for name, want := range keyCodes {
		for _, spelling := range []string{name, strings.ToUpper(name), strings.ReplaceAll(name, " ", "_")} {
			if got, ok := KeyCode(spelling); !ok || got != want {
				t.Errorf("KeyCode(%q) = %d, %v; want %d", spelling, got, ok, want)
			}
		}
	}
}

// Package names: letters, digits, dots and underscores only (anything else could reach a shell).
func TestValidPkg(t *testing.T) {
	for _, ok := range []string{"com.example.app", "a", "A_b.C9", "com.example.app_debug"} {
		if err := validPkg(ok); err != nil {
			t.Errorf("validPkg(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "com.app;rm -rf /", "com app", "com-app", "com/app", "$(id)", "com.app'", "ünï.app", "com.app\n"} {
		if err := validPkg(bad); err == nil {
			t.Errorf("validPkg(%q) = nil, want an error", bad)
		}
	}
	if err := validPkg(""); err == nil || !strings.Contains(err.Error(), "no app id") {
		t.Errorf("empty id: %v", err)
	}
}

// shellQuote produces one shell word that expands to exactly the input.
func TestShellQuote(t *testing.T) {
	cases := map[string]string{"abc": "'abc'", "": "''", "it's": `'it'\''s'`, "a b": "'a b'"}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	for _, in := range []string{"plain", "it's", `"double"`, "$HOME", "`id`", "a\nb", `back\slash`, "*", "; rm -rf /", "''", "é ✓"} {
		out, err := exec.Command(sh, "-c", "printf %s "+shellQuote(in)).Output()
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != in {
			t.Errorf("shell expanded %s to %q, want %q", shellQuote(in), out, in)
		}
	}
}

// The embedded agent is an APK; its hash names the remote file (12 hex digits of its sha256).
func TestAgentHashAndVersion(t *testing.T) {
	if len(agentAPK) == 0 || !bytes.HasPrefix(agentAPK, []byte("PK")) {
		t.Fatalf("embedded agent is not an APK (%d bytes)", len(agentAPK))
	}
	sum := sha256.Sum256(agentAPK)
	if got, want := agentHash(), hex.EncodeToString(sum[:])[:12]; got != want {
		t.Errorf("agentHash = %q, want %q", got, want)
	}
	if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(agentHash()) || agentHash() != agentHash() {
		t.Errorf("agentHash %q is not 12 stable hex digits", agentHash())
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(agentVersion()) {
		t.Errorf("agentVersion %q is not x.y.z", agentVersion())
	}
}

// The NoopService component belongs to the agent's own package.
func TestNoopServiceComponent(t *testing.T) {
	if !strings.HasPrefix(noopService, agentPackage+"/") {
		t.Errorf("noopService %q is not in %q", noopService, agentPackage)
	}
	if socketName != "localabstract:testbuddy-agent" {
		t.Errorf("socketName = %q (the agent listens on @testbuddy-agent)", socketName)
	}
}

// seq maps an agent count to base+count and records the largest seen (lastSeq never goes backwards).
func TestSeqMapping(t *testing.T) {
	d := &Driver{}
	steps := []struct {
		base, agent, want, last int64
	}{
		{0, 5, 5, 5}, {0, 3, 3, 5}, {0, 9, 9, 9}, {10, 0, 10, 10}, {10, 1, 11, 11}, {10, 0, 10, 11}, {20, 0, 20, 20},
	}
	for _, s := range steps {
		d.seqBase = s.base
		if got := d.seq(s.agent); got != s.want {
			t.Errorf("base %d, seq(%d) = %d, want %d", s.base, s.agent, got, s.want)
		}
		if got := d.lastSeq.Load(); got != s.last {
			t.Errorf("base %d, seq(%d): lastSeq = %d, want %d", s.base, s.agent, got, s.last)
		}
	}
}

// Concurrent seq updates settle on the largest value.
func TestSeqConcurrent(t *testing.T) {
	d := &Driver{}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := int64(0); i < 1000; i++ {
				d.seq(i*8 + int64(g))
			}
		}(g)
	}
	wg.Wait()
	if got := d.lastSeq.Load(); got != 7999 {
		t.Errorf("lastSeq = %d, want 7999", got)
	}
}

// Every permission group maps lower-case flow names to fully qualified Android permissions.
func TestPermissionGroups(t *testing.T) {
	for name, perms := range permissionGroups {
		if name != strings.ToLower(name) {
			t.Errorf("group %q is not lower case (lookups lower-case the flow's name)", name)
		}
		if len(perms) == 0 {
			t.Errorf("group %q is empty", name)
		}
		for _, p := range perms {
			if !regexp.MustCompile(`^android\.permission\.[A-Z_]+$`).MatchString(p) {
				t.Errorf("group %q: %q is not an android.permission name", name, p)
			}
		}
	}
	for _, want := range []string{"camera", "location", "microphone", "contacts", "notifications", "storage", "photos"} {
		if _, ok := permissionGroups[want]; !ok {
			t.Errorf("no %q group", want)
		}
	}
}

// Identity accessors report the device info and the size the agent gave.
func TestIdentity(t *testing.T) {
	d := bareDriver(nil)
	d.w, d.h = 1080, 2400
	if d.Platform() != "android" || d.Serial() != testSerial || d.Name() != "Pixel Test" {
		t.Errorf("identity = %q %q %q", d.Platform(), d.Serial(), d.Name())
	}
	if w, h := d.Size(); w != 1080 || h != 2400 {
		t.Errorf("Size = %d, %d", w, h)
	}
	if err := d.ClearKeychain(ctxT(t)); err != nil {
		t.Errorf("ClearKeychain = %v (Android has no keychain: a no-op)", err)
	}
}

// fanout never blocks on a full subscriber: the 64-slot buffer fills and the rest are dropped.
func TestFanoutDropsWhenSubscriberIsFull(t *testing.T) {
	d := bareDriver(nil)
	ch, cancel := d.Events()
	defer cancel()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			d.fanout(driver.Event{Kind: "changed", Seq: int64(i)})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fanout blocked on a full subscriber")
	}
	if len(ch) != 64 {
		t.Errorf("buffered %d events, want 64", len(ch))
	}
	if e := <-ch; e.Seq != 0 {
		t.Errorf("first event seq %d, want 0 (the oldest are kept)", e.Seq)
	}
}

// Cancelling a subscription removes it (and is safe to call twice); other subscribers still get events.
func TestEventsCancel(t *testing.T) {
	d := bareDriver(nil)
	a, cancelA := d.Events()
	b, cancelB := d.Events()
	defer cancelB()
	cancelA()
	cancelA()
	d.fanout(driver.Event{Kind: "settled", Seq: 3})
	if len(a) != 0 {
		t.Error("a cancelled subscriber still got an event")
	}
	if e := <-b; e.Kind != "settled" || e.Seq != 3 {
		t.Errorf("got %+v", e)
	}
	if len(d.subs) != 1 {
		t.Errorf("%d subscribers left, want 1", len(d.subs))
	}
}

// A node's drawing order ("o", its place among its siblings as drawn) is read when the agent
// sends it, 0 included; without it (an older agent) it is unknown. Issue #11.
func TestConvertDrawingOrder(t *testing.T) {
	var raw []rawNode
	if err := json.Unmarshal([]byte(`[{"d":0,"b":[0,0,1,1],"o":2},{"d":0,"b":[0,0,1,1],"o":0},{"d":0,"b":[0,0,1,1]}]`), &raw); err != nil {
		t.Fatal(err)
	}
	for i, want := range []struct {
		order int
		known bool
	}{{2, true}, {0, true}, {0, false}} {
		n := convert(raw[i])
		if n.DrawOrder != want.order || n.DrawOrderKnown != want.known {
			t.Errorf("node %d: order %d known %v, want %d %v", i, n.DrawOrder, n.DrawOrderKnown, want.order, want.known)
		}
	}
}
