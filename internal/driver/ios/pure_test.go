package ios

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

func TestWindowType(t *testing.T) {
	for in, want := range map[int]string{1: "app", 2: "keyboard", 3: "system", 4: "overlay", 0: "other", 7: "other", -3: "other"} {
		if got := windowType(in); got != want {
			t.Errorf("windowType(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestConvert(t *testing.T) {
	names := []string{"Clickable", "Enabled", "Focused", "Scrollable", "Checked", "Selected", "Checkable", "Password",
		"Editable", "LongClickable", "", "ShowingHint", "Visible"}
	get := func(n *ui.Node) map[string]bool {
		return map[string]bool{"Clickable": n.Clickable, "Enabled": n.Enabled, "Focused": n.Focused, "Scrollable": n.Scrollable,
			"Checked": n.Checked, "Selected": n.Selected, "Checkable": n.Checkable, "Password": n.Password, "Editable": n.Editable,
			"LongClickable": n.LongClickable, "ShowingHint": n.ShowingHint, "Visible": n.Visible}
	}
	for i, want := range names {
		for name, v := range get(convert(rawNode{F: 1 << i})) {
			if v != (name == want) {
				t.Errorf("flag %d: %s = %v", 1<<i, name, v)
			}
		}
	}
	n := convert(rawNode{D: 2, B: [4]int{1, 2, 3, 4}, C: "TextField", T: "hi", CD: "Email", ID: "email", H: "you@example.com", V: "hi"})
	want := ui.Node{Depth: 2, Bounds: ui.Rect{L: 1, T: 2, R: 3, B: 4}, Class: "TextField", Text: "hi", Desc: "Email", ID: "email",
		Hint: "you@example.com", Value: "hi"}
	if !reflect.DeepEqual(*n, want) {
		t.Errorf("convert = %+v\nwant %+v", *n, want)
	}
}

func TestActionParams(t *testing.T) {
	cases := []struct {
		name string
		o    driver.ActionOpts
		want map[string]any
	}{
		{"defaults", driver.ActionOpts{}, map[string]any{"noChange": 1000}},
		{"no settle", driver.ActionOpts{NoSettle: true}, map[string]any{"settle": false, "noChange": 1000}},
		{"react", driver.ActionOpts{React: true}, map[string]any{"react": true, "noChange": 1000}},
		{"noChange", driver.ActionOpts{NoChangeMs: 300}, map[string]any{"noChange": 300}},
		{"timeout", driver.ActionOpts{Timeout: 1500 * time.Millisecond}, map[string]any{"noChange": 1000, "timeout": int64(1500)}},
		{"hold is a tap param, not a settle param", driver.ActionOpts{Hold: 20 * time.Millisecond}, map[string]any{"noChange": 1000}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := actionParams(c.o, map[string]any{}); !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestKeyCode(t *testing.T) {
	cases := []struct {
		in   string
		code int
		ok   bool
	}{
		{"enter", 66, true}, {"ENTER", 66, true}, {" backspace ", 67, true}, {"delete", 67, true}, {"home", 3, true},
		{"lock", 26, true}, {"power", 26, true}, {"volume_up", 24, true}, {"Volume Down", 25, true}, {"tab", 61, true},
		{"escape", 111, true}, {"back", 4, true},
		{"remote dpad up", 0, false}, {"", 0, false}, {"menu", 0, false},
	}
	for _, c := range cases {
		if code, ok := KeyCode(c.in); code != c.code || ok != c.ok {
			t.Errorf("KeyCode(%q) = %d, %v; want %d, %v", c.in, code, ok, c.code, c.ok)
		}
	}
}

func TestValidID(t *testing.T) {
	for _, ok := range []string{"com.example.app", "com.example.my-app", "a_b.C9", "x"} {
		if err := validID(ok); err != nil {
			t.Errorf("validID(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "com.app;rm", "com app", "com/app", "com.app'", "$(x)", "ü.app", "a\tb"} {
		if err := validID(bad); err == nil {
			t.Errorf("validID(%q) = nil", bad)
		}
	}
	if err := validID(""); err == nil || err.Error() != "no app id given" {
		t.Errorf("empty: %v", err)
	}
	if err := validID("a b"); err == nil || !strings.Contains(err.Error(), `invalid bundle id "a b"`) {
		t.Errorf("bad: %v", err)
	}
}

func TestArgString(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"text", "text"}, {"", ""}, {true, "true"}, {false, "false"}, {2.0, "2"}, {0.25, "0.25"}, {1e21, "1000000000000000000000"},
		{-3.5, "-3.5"}, {7, "7"}, {int64(9), "9"}, {nil, "<nil>"}, {[]string{"a", "b"}, "[a b]"},
	}
	for _, c := range cases {
		if got := argString(c.in); got != c.want {
			t.Errorf("argString(%#v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSQLQuote(t *testing.T) {
	for in, want := range map[string]string{"abc": "'abc'", "": "''", "it's": "'it''s'", "''": "''''''", `a"b`: `'a"b'`} {
		if got := sqlQuote(in); got != want {
			t.Errorf("sqlQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestPreferredPort(t *testing.T) {
	seen := map[int]bool{}
	for i := 0; i < 200; i++ {
		udid := fmt.Sprintf("SIM-%04d", i)
		p := preferredPort(udid)
		if p < basePort || p >= basePort+portSpan {
			t.Fatalf("preferredPort(%q) = %d, out of range", udid, p)
		}
		if p != preferredPort(udid) {
			t.Fatalf("preferredPort(%q) is not stable", udid)
		}
		seen[p] = true
	}
	if len(seen) < 150 {
		t.Errorf("200 udids share only %d ports", len(seen))
	}
}

func TestBuildID(t *testing.T) {
	if len(agentArchive) < 2 || agentArchive[0] != 0x1f || agentArchive[1] != 0x8b {
		t.Fatal("the embedded agent is not gzip")
	}
	sum := sha256.Sum256(agentArchive)
	if BuildID() != hex.EncodeToString(sum[:])[:12] || !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(BuildID()) {
		t.Errorf("BuildID = %q", BuildID())
	}
}

func TestAnimationSpeedup(t *testing.T) {
	for v, want := range map[string]bool{"": true, "1": true, "0": false, "false": true} {
		t.Setenv("TB_IOS_FAST_ANIMATIONS", v)
		if got := animationSpeedup(); got != want {
			t.Errorf("TB_IOS_FAST_ANIMATIONS=%q: %v, want %v", v, got, want)
		}
	}
}

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		m[k] = v
	}
	return m
}

func TestLaunchEnv(t *testing.T) {
	const probe = "/cache/libTBProbe.dylib"
	const dyld, flags = "SIMCTL_CHILD_DYLD_INSERT_LIBRARIES", "SIMCTL_CHILD_FEATUREFLAGS_ENABLED"
	cases := []struct {
		name     string
		probe    string
		fast     string
		existing map[string]string
		want     map[string]string
	}{
		{"both", probe, "", nil, map[string]string{dyld: probe, flags: "UIKit/UIPreferences"}},
		{"no probe", "", "", nil, map[string]string{dyld: "", flags: "UIKit/UIPreferences"}},
		{"no fast animations", probe, "0", nil, map[string]string{dyld: probe, flags: ""}},
		{"neither", "", "0", nil, map[string]string{dyld: "", flags: ""}},
		{"appends to a library list", probe, "0", map[string]string{dyld: "/other.dylib"}, map[string]string{dyld: "/other.dylib:" + probe}},
		{"fills an empty value", probe, "0", map[string]string{dyld: ""}, map[string]string{dyld: probe}},
		{"already there", probe, "0", map[string]string{dyld: "/x.dylib:" + probe}, map[string]string{dyld: "/x.dylib:" + probe}},
		{"flag merged", "", "", map[string]string{flags: "Other/Flag"}, map[string]string{flags: "Other/Flag:UIKit/UIPreferences"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(dyld, "")
			t.Setenv(flags, "")
			os.Unsetenv(dyld)
			os.Unsetenv(flags)
			for k, v := range c.existing {
				t.Setenv(k, v)
			}
			t.Setenv("TB_IOS_FAST_ANIMATIONS", c.fast)
			t.Setenv("TB_SOME_OTHER", "kept")
			env := (&Driver{probe: c.probe}).launchEnv()
			m := envMap(env)
			for k, want := range c.want {
				got, ok := m[k]
				if want == "" && ok {
					t.Errorf("%s = %q, want it absent", k, got)
				} else if want != "" && got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}
			if m["TB_SOME_OTHER"] != "kept" {
				t.Error("the caller's environment was not kept")
			}
			n := 0
			for _, e := range env {
				if strings.HasPrefix(e, dyld+"=") {
					n++
				}
			}
			if n > 1 {
				t.Errorf("%s set %d times", dyld, n)
			}
		})
	}
}

func TestLaunchEnvOrder(t *testing.T) {
	for _, k := range []string{"SIMCTL_CHILD_DYLD_INSERT_LIBRARIES", "SIMCTL_CHILD_FEATUREFLAGS_ENABLED"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	t.Setenv("TB_IOS_FAST_ANIMATIONS", "1")
	t.Setenv("TB_IOS_GESTURE_TRACE", "0")
	env := (&Driver{probe: "/p.dylib"}).launchEnv()
	if len(env) < 2 {
		t.Fatal(env)
	}
	tail := env[len(env)-2:]
	if !reflect.DeepEqual(tail, []string{"SIMCTL_CHILD_DYLD_INSERT_LIBRARIES=/p.dylib", "SIMCTL_CHILD_FEATUREFLAGS_ENABLED=UIKit/UIPreferences"}) {
		t.Errorf("appended %v", tail)
	}
}

func TestGestureTraceEnv(t *testing.T) {
	const key = "SIMCTL_CHILD_TB_GESTURE_TRACE"
	t.Setenv(key, "")
	os.Unsetenv(key)
	want := filepath.Join("/cache", "gesture-trace-"+testUDID+".log")
	cases := []struct {
		name, ci, flag string
		probe          bool
		on             bool
	}{
		{"off by default", "0", "", true, false},
		{"on in CI mode", "1", "", true, true},
		{"on when asked", "0", "1", true, true},
		{"off when asked, in CI mode", "1", "0", true, false},
		{"never without the probe", "1", "1", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TB_CI", c.ci)
			t.Setenv("TB_IOS_GESTURE_TRACE", c.flag)
			d := &Driver{probe: "/cache/libTBProbe.dylib", dir: "/cache", info: device.Info{Serial: testUDID}}
			if !c.probe {
				d.probe = ""
			}
			got, ok := envMap(d.launchEnv())[key]
			if ok != c.on || (c.on && got != want) {
				t.Errorf("%s = %q (set: %v), want set: %v (%q)", key, got, ok, c.on, want)
			}
		})
	}
}

func TestEmptyDir(t *testing.T) {
	for _, bad := range []string{"", "relative/CoreSimulator/Devices/x", "/Users/me/Documents", "/tmp"} {
		if err := emptyDir(bad); err == nil || !strings.Contains(err.Error(), "refusing to empty") {
			t.Errorf("emptyDir(%q) = %v", bad, err)
		}
	}
	dir := filepath.Join(t.TempDir(), "CoreSimulator", "Devices", "UDID", "data", "Containers", "Data", "Application", "APP")
	for _, p := range []string{"Documents/a.txt", "Library/Caches/b", "tmp/c"} {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755)
		os.WriteFile(filepath.Join(dir, p), []byte("x"), 0o644)
	}
	if err := emptyDir(dir); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("%d entries left", len(entries))
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("the folder itself was removed: %v", err)
	}
	if err := emptyDir(filepath.Join(dir, "missing", "CoreSimulator", "Devices")); err == nil {
		t.Error("a missing folder was not reported")
	}
}

func TestTCCDB(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d := &Driver{info: testInfo()}
	want := filepath.Join(home, "Library/Developer/CoreSimulator/Devices", testUDID, "data/Library/TCC/TCC.db")
	if got := d.tccDB(); got != want {
		t.Errorf("tccDB = %q, want %q", got, want)
	}
}

func TestTCCServices(t *testing.T) {
	for name, s := range tccServices {
		if name != strings.ToLower(name) || !strings.HasPrefix(s, "kTCCService") {
			t.Errorf("%q → %q", name, s)
		}
	}
	for _, name := range []string{"camera", "photos", "microphone", "contacts", "calendar"} {
		if tccServices[name] == "" {
			t.Errorf("no %q service", name)
		}
	}
	if _, ok := tccServices["location"]; ok {
		t.Error("location goes through simctl privacy, not TCC")
	}
	if _, ok := tccServices["notifications"]; ok {
		t.Error("notifications have no TCC entry")
	}
}

func TestLogTail(t *testing.T) {
	d := &Driver{info: testInfo(), dir: t.TempDir()}
	if got := d.logTail(); got != "" {
		t.Errorf("no log: %q", got)
	}
	var lines []string
	for i := 1; i <= 40; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	os.WriteFile(d.logPath(), []byte(strings.Join(lines, "\n")+"\n\n"), 0o644)
	got := strings.Split(d.logTail(), "\n")
	if len(got) != 25 || got[0] != "line 16" || got[24] != "line 40" {
		t.Errorf("tail = %v", got)
	}
	os.WriteFile(d.logPath(), []byte("only\n"), 0o644)
	if d.logTail() != "only" {
		t.Errorf("short tail = %q", d.logTail())
	}
	if filepath.Base(d.logPath()) != "runner-"+testUDID+".log" {
		t.Errorf("logPath = %q", d.logPath())
	}
}

func TestIdentityAndAirplane(t *testing.T) {
	d := &Driver{info: testInfo(), w: 393, h: 852, port: 22123}
	if d.Platform() != "ios" || d.Serial() != testUDID || d.Name() != "iPhone Test" || d.Port() != 22123 {
		t.Errorf("identity %q %q %q %d", d.Platform(), d.Serial(), d.Name(), d.Port())
	}
	if w, h := d.Size(); w != 393 || h != 852 {
		t.Errorf("Size = %dx%d", w, h)
	}
	if d.SetAirplaneMode(ctxT(t), true) != errAirplane || d.ToggleAirplaneMode(ctxT(t)) != errAirplane {
		t.Error("airplane mode should be unsupported")
	}
	if err := d.AddMedia(ctxT(t), nil); err != nil {
		t.Errorf("AddMedia(nil) = %v", err)
	}
	if err := d.StopRecording(ctxT(t)); err != nil {
		t.Errorf("StopRecording without a recording = %v", err)
	}
}

func TestUnpack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	dir, err := unpack()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dir, home) || filepath.Base(dir) != "ios-agent-"+BuildID() {
		t.Errorf("unpacked to %q", dir)
	}
	for _, p := range []string{"tb-agent.xctestrun", "Release-iphonesimulator/libTBProbe.dylib",
		"Release-iphonesimulator/TBAgentUITests-Runner.app/Info.plist"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(dir))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".ios-agent-") {
			t.Errorf("temporary folder %s left behind", e.Name())
		}
	}
	marker := filepath.Join(dir, "marker")
	os.WriteFile(marker, nil, 0o644)
	again, err := unpack()
	if err != nil || again != dir {
		t.Fatalf("second unpack = %q, %v", again, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("the second unpack extracted again instead of reusing the folder")
	}
}

func TestUnpackUnwritableCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Skip(err)
	}
	os.MkdirAll(filepath.Dir(cache), 0o755)
	os.WriteFile(cache, []byte("a file, not a folder"), 0o644)
	if _, err := unpack(); err == nil {
		t.Error("unpack succeeded under a file")
	}
}

func withArchive(t *testing.T, b []byte) {
	old := agentArchive
	agentArchive = b
	t.Cleanup(func() { agentArchive = old })
}

type tarEntry struct {
	name, body, link string
	typ              byte
}

func makeTarGz(t *testing.T, entries []tarEntry) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: 0o755, Size: int64(len(e.body)), Linkname: e.link}
		if e.typ != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			tw.Write([]byte(e.body))
		}
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

func TestUnpackEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	withArchive(t, makeTarGz(t, []tarEntry{
		{name: "./", typ: tar.TypeDir},
		{name: "./Release/", typ: tar.TypeDir},
		{name: "./Release/lib.dylib", body: "LIB", typ: tar.TypeReg},
		{name: "deep/nested/file.txt", body: "deep", typ: tar.TypeReg},
		{name: "./Release/current", link: "lib.dylib", typ: tar.TypeSymlink},
		{name: "../escape.txt", body: "no", typ: tar.TypeReg},
		{name: "/abs.txt", body: "no", typ: tar.TypeReg},
		{name: "fifo", typ: tar.TypeFifo},
		{name: "tb-agent.xctestrun", body: "<plist/>", typ: tar.TypeReg},
	}))
	dir, err := unpack()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != "ios-agent-"+BuildID() {
		t.Errorf("folder %q is not named after the build", dir)
	}
	for p, want := range map[string]string{"Release/lib.dylib": "LIB", "deep/nested/file.txt": "deep", "Release/current": "LIB", "tb-agent.xctestrun": "<plist/>"} {
		b, err := os.ReadFile(filepath.Join(dir, p))
		if err != nil || string(b) != want {
			t.Errorf("%s = %q, %v; want %q", p, b, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.txt")); err == nil {
		t.Error("an entry escaped the agent folder")
	}
	if _, err := os.Stat(filepath.Join(dir, "abs.txt")); err == nil {
		t.Error("an absolute entry was extracted")
	}
	if _, err := os.Stat(filepath.Join(dir, "fifo")); err == nil {
		t.Error("a special file was extracted")
	}
	if l, err := os.Readlink(filepath.Join(dir, "Release/current")); err != nil || l != "lib.dylib" {
		t.Errorf("symlink = %q, %v", l, err)
	}
}

func TestUnpackDamagedArchive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	withArchive(t, []byte("not gzip"))
	if _, err := unpack(); err == nil || !strings.Contains(err.Error(), "rebuild it with agents/ios/build.sh") {
		t.Errorf("err = %v", err)
	}
	if _, err := Open(ctxT(t), testInfo()); err == nil {
		t.Error("Open succeeded without an agent")
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte("this is not a tar archive, just some bytes that are long enough to fill a header block................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................"))
	zw.Close()
	withArchive(t, buf.Bytes())
	if _, err := unpack(); err == nil {
		t.Error("a broken tar was accepted")
	}
}

func TestUnpackWithoutXctestrun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	withArchive(t, makeTarGz(t, []tarEntry{{name: "x.txt", body: "x", typ: tar.TypeReg}}))
	if _, err := unpack(); err != nil {
		t.Fatal(err)
	}
	if _, err := unpack(); err == nil {
		t.Error("re-extracting over an existing folder succeeded")
	}
}
