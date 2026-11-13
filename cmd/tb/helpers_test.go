package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/orchestrate"
	"github.com/elforama/testbuddy/internal/ui"
)

// TestMain keeps the CLI away from the real state dir, and makes a stray "<exe> daemon"
// spawn of this test binary exit at once.
func TestMain(m *testing.M) {
	// Device locks off: these tests make many pools and sessions for the same fake devices
	// (locks have their own tests, in devlock and in TestPoolLocks / TestSessionLocks).
	os.Setenv("TB_NO_DEVICE_LOCK", "1")
	if len(os.Args) > 1 && os.Args[1] == "daemon" {
		os.Exit(0)
	}
	dir, err := os.MkdirTemp("", "tbcli")
	if err != nil {
		panic(err)
	}
	os.Setenv("TB_HOME", dir)
	// No real device is reachable from these tests: no adb, xcrun or Chrome to find (PATH
	// holds git only, which the a11y --changed tests use; the SDK and home folders are empty).
	// A command that lists or opens devices without the tests' fakes then finds none.
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	if git, err := exec.LookPath("git"); err == nil {
		os.Symlink(git, filepath.Join(bin, "git"))
	}
	for k, v := range map[string]string{"PATH": bin, "HOME": dir, "ANDROID_HOME": dir, "ANDROID_SDK_ROOT": dir,
		"TB_CHROME": filepath.Join(dir, "no-chrome")} {
		os.Setenv(k, v)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// dur prints milliseconds under a second, tenths under a minute, else a rounded duration.
func TestDur(t *testing.T) {
	cases := map[time.Duration]string{
		0:                        "0ms",
		999 * time.Microsecond:   "0ms",
		12 * time.Millisecond:    "12ms",
		999 * time.Millisecond:   "999ms",
		time.Second:              "1.0s",
		1260 * time.Millisecond:  "1.3s",
		59900 * time.Millisecond: "59.9s",
		time.Minute:              "1m0s",
		90*time.Second + 4e8:     "1m30s",
		90*time.Second + 6e8:     "1m31s",
		time.Hour + time.Second:  "1h0m1s",
	}
	for d, want := range cases {
		if got := dur(d); got != want {
			t.Errorf("dur(%v) = %q, want %q", d, got, want)
		}
	}
}

// orDefault falls back only for the empty string.
func TestOrDefault(t *testing.T) {
	if orDefault("", "report.xml") != "report.xml" || orDefault("x.xml", "report.xml") != "x.xml" || orDefault(" ", "d") != " " {
		t.Error("orDefault")
	}
}

// toCommandList turns `tb do` arguments into one YAML command list.
func TestToCommandList(t *testing.T) {
	cases := []struct {
		name  string
		parts []string
		want  string
	}{
		{"none", nil, ""},
		{"one command", []string{"tapOn: Login"}, "- tapOn: Login\n"},
		{"bare command", []string{"back"}, "- back\n"},
		{"several commands", []string{"launchApp", "tapOn: Login", `inputText: "hi"`}, "- launchApp\n- tapOn: Login\n- inputText: \"hi\"\n"},
		{"multi-line command is indented", []string{"tapOn:\n  id: go\n  index: 1"}, "- tapOn:\n    id: go\n    index: 1\n"},
		{"trailing newlines trimmed", []string{"back\n\n"}, "- back\n"},
		{"already a list is kept", []string{"- launchApp\n- back"}, "- launchApp\n- back\n"},
		{"indented list is kept", []string{"  - back"}, "  - back\n"},
		{"stdin list with trailing newline", []string{"- tapOn: x\n- back\n"}, "- tapOn: x\n- back\n"},
		{"a dash inside a value is not a list", []string{`inputText: "a - b"`}, "- inputText: \"a - b\"\n"},
		{"mix of list and commands", []string{"- launchApp", "back"}, "- launchApp\n- back\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := toCommandList(c.parts); got != c.want {
				t.Errorf("toCommandList(%q) = %q, want %q", c.parts, got, c.want)
			}
		})
	}
}

// The command lists toCommandList builds parse as flows.
func TestToCommandListParses(t *testing.T) {
	yaml := toCommandList([]string{"launchApp", "tapOn:\n  id: go", "- back\n- inputText: hi"})
	dir := t.TempDir()
	p := filepath.Join(dir, "f.yaml")
	os.WriteFile(p, []byte("appId: com.app\n---\n"+yaml), 0o644)
	f, err := flow.ParseFile(p)
	if err != nil {
		t.Fatalf("%v\n%s", err, yaml)
	}
	if len(f.Commands) != 4 {
		t.Errorf("commands = %d\n%s", len(f.Commands), yaml)
	}
}

func job(name, path string, shard, attempt int) *orchestrate.Job {
	return &orchestrate.Job{Flow: &flow.Flow{Path: path, Config: flow.Config{Name: name}}, ShardID: shard, Attempt: attempt}
}

// Debug dirs are named after the flow (unsafe characters replaced), plus shard and retry.
func TestFlowDebugDir(t *testing.T) {
	root := filepath.Join("/tmp", "tb-debug")
	cases := []struct {
		name string
		j    *orchestrate.Job
		want string
	}{
		{"file name without extension", job("", "/w/flows/login.yaml", 0, 0), "login"},
		{"config name", job("Checkout", "/w/x.yaml", 0, 0), "Checkout"},
		{"unsafe characters collapse to one underscore", job("Checkout: Pay/Ship ✓", "", 0, 0), "Checkout_Pay_Ship_"},
		{"spaces", job("", "/w/login flow.yaml", 0, 0), "login_flow"},
		{"safe punctuation kept", job("a.b_c-d", "", 0, 0), "a.b_c-d"},
		{"path separators cannot escape", job("../../etc", "", 0, 0), ".._.._etc"},
		{"shard", job("f", "", 2, 0), "f-shard-2"},
		{"retry", job("f", "", 0, 1), "f-retry-1"},
		{"shard and retry", job("f", "", 3, 2), "f-shard-3-retry-2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := flowDebugDir(root, c.j); got != filepath.Join(root, c.want) {
				t.Errorf("flowDebugDir = %q, want %q", got, filepath.Join(root, c.want))
			}
		})
	}
}

// Every job of a run gets its own debug folder, named before the run (issue #56). A flow
// whose name is unique in the run keeps its name; flows that share one (the same file name
// in two directories, the same name: in two files, names that differ only in case or in
// characters a folder cannot hold) are told apart by their directory, then their path
// relative to each other, then their file name, then a number.
func TestNameDebugDirs(t *testing.T) {
	cases := []struct {
		name string
		jobs []*orchestrate.Job
		want []string
	}{
		{"unique names are kept",
			[]*orchestrate.Job{job("", "/w/flows/login.yaml", 0, 0), job("Checkout", "/w/x.yaml", 0, 0)},
			[]string{"login", "Checkout"}},
		{"same file name in two directories",
			[]*orchestrate.Job{job("", "/w/login/smoke.yaml", 0, 0), job("", "/w/a.yaml", 0, 0), job("", "/w/signup/smoke.yaml", 0, 0)},
			[]string{"smoke-login", "a", "smoke-signup"}},
		{"same name: in two files of one directory",
			[]*orchestrate.Job{job("Checkout", "/w/flows/pay.yaml", 0, 0), job("Checkout", "/w/flows/ship.yaml", 0, 0)},
			[]string{"Checkout-pay", "Checkout-ship"}},
		{"same parent directory name",
			[]*orchestrate.Job{job("", "/w/a/x/smoke.yaml", 0, 0), job("", "/w/b/x/smoke.yaml", 0, 0)},
			[]string{"smoke-a_x", "smoke-b_x"}},
		{"names that differ only in case",
			[]*orchestrate.Job{job("", "/w/a/Login.yaml", 0, 0), job("", "/w/b/login.yaml", 0, 0)},
			[]string{"Login-a", "login-b"}},
		{"names that differ only in unsafe characters",
			[]*orchestrate.Job{job("Pay now", "/w/a/f.yaml", 0, 0), job("Pay/now", "/w/b/f.yaml", 0, 0)},
			[]string{"Pay_now-a", "Pay_now-b"}},
		{"the same file twice: a number",
			[]*orchestrate.Job{job("", "/w/s.yaml", 0, 0), job("", "/w/s.yaml", 0, 0)},
			[]string{"s-1", "s-2"}},
		{"inline flows: a number",
			[]*orchestrate.Job{job("f", "", 0, 0), job("f", "", 0, 0)},
			[]string{"f-1", "f-2"}},
		{"a made-up name never takes a flow's own",
			[]*orchestrate.Job{job("", "/w/login/smoke.yaml", 0, 0), job("", "/w/signup/smoke.yaml", 0, 0), job("smoke-login", "/w/z.yaml", 0, 0)},
			[]string{"smoke-login_smoke", "smoke-signup", "smoke-login"}},
		{"shards of one flow are told apart by their shard",
			[]*orchestrate.Job{job("", "/w/s.yaml", 1, 0), job("", "/w/s.yaml", 2, 0)},
			[]string{"s", "s"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nameDebugDirs(c.jobs)
			var got []string
			for _, j := range c.jobs {
				got = append(got, j.DebugName)
			}
			if strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Errorf("debug names = %q, want %q", got, c.want)
			}
		})
	}
}

// Whatever the flows are called, no two jobs of a run share a debug folder, even on a file
// system that ignores case; a retry keeps its job's name (issue #56).
func TestNameDebugDirsUnique(t *testing.T) {
	root := filepath.Join("/tmp", "tb-debug")
	jobs := []*orchestrate.Job{
		job("", "/w/login/smoke.yaml", 0, 0), job("", "/w/signup/smoke.yaml", 0, 0),
		job("Smoke", "/w/login/other.yaml", 0, 0), job("smoke-login", "/w/z.yaml", 0, 0),
		job("", "/w/s.yaml", 1, 0), job("", "/w/s.yaml", 2, 0), job("", "/w/t/s.yaml", 1, 0),
		job("", "", 0, 0), job("..", "", 0, 0), job(".", "", 0, 0),
	}
	nameDebugDirs(jobs)
	seen := map[string]int{}
	for i, j := range jobs {
		dir := strings.ToLower(flowDebugDir(root, j))
		if k, ok := seen[dir]; ok {
			t.Errorf("jobs %d (%s) and %d (%s) share the debug folder %s", k, jobs[k].Flow.Path, i, j.Flow.Path, dir)
		}
		seen[dir] = i
		if !strings.HasPrefix(dir, strings.ToLower(root)+string(filepath.Separator)) || strings.ContainsRune(j.DebugName, filepath.Separator) {
			t.Errorf("job %d's debug folder %q is not a folder of its own in the root", i, dir)
		}
	}
	retry := *jobs[0]
	retry.Attempt++
	if got, want := flowDebugDir(root, &retry), flowDebugDir(root, jobs[0])+"-retry-1"; got != want {
		t.Errorf("retry's debug folder = %q, want %q", got, want)
	}
}

// A flow's debug dir always lies inside the debug root, whatever the flow is called.
func TestFlowDebugDirStaysInRoot(t *testing.T) {
	root := filepath.Join("/tmp", "tb-debug")
	for _, name := range []string{"..", "."} {
		got := flowDebugDir(root, job(name, "", 0, 0))
		if !strings.HasPrefix(got, root+string(filepath.Separator)) {
			t.Errorf("flowDebugDir for a flow named %q is %q, outside (or equal to) the debug root %q", name, got, root)
		}
	}
}

// hierarchyJSON nests windows and nodes with the usual attribute names; empty attributes are
// left out and flags are present on element nodes only.
func TestHierarchyJSON(t *testing.T) {
	root := &ui.Node{Class: "android.widget.FrameLayout", Bounds: ui.Rect{R: 400, B: 800}, Enabled: true, Depth: 0}
	btn := &ui.Node{Text: "Sign in", Desc: "sign in button", ID: "com.app:id/go", Hint: "h", Class: "android.widget.Button",
		Error: "bad", Value: "v", Bounds: ui.Rect{L: 10, T: 20, R: 110, B: 70}, Clickable: true, Enabled: true,
		Focused: true, Checked: true, Selected: true, Depth: 1}
	field := &ui.Node{Class: "android.widget.EditText", Bounds: ui.Rect{L: 1, T: 2, R: 3, B: 4}, Depth: 1}
	s := &ui.Screen{Platform: "android", Width: 400, Height: 800, Windows: []*ui.Window{
		{Type: "app", Package: "com.app", Bounds: ui.Rect{R: 400, B: 800}, Nodes: []*ui.Node{root, btn, field}},
		{Type: "keyboard", Package: "com.ime", Bounds: ui.Rect{T: 500, R: 400, B: 800}},
	}}
	s.Link()
	h := hierarchyJSON(s)
	if h.Attributes["bounds"] != "[0,0][400,800]" || len(h.Attributes) != 1 || h.Clickable != nil {
		t.Errorf("root = %+v", h)
	}
	if len(h.Children) != 2 {
		t.Fatalf("windows = %d", len(h.Children))
	}
	win := h.Children[0]
	if win.Attributes["window"] != "app" || win.Attributes["package"] != "com.app" || win.Attributes["bounds"] != "[0,0][400,800]" {
		t.Errorf("window = %+v", win.Attributes)
	}
	if len(h.Children[1].Children) != 0 || h.Children[1].Attributes["window"] != "keyboard" {
		t.Errorf("keyboard window = %+v", h.Children[1])
	}
	if len(win.Children) != 1 || len(win.Children[0].Children) != 2 {
		t.Fatalf("tree shape: %+v", win.Children)
	}
	r := win.Children[0]
	if want := map[string]string{"bounds": "[0,0][400,800]", "class": "android.widget.FrameLayout"}; !mapsEqual(r.Attributes, want) {
		t.Errorf("root node attrs = %v", r.Attributes)
	}
	if r.Clickable == nil || *r.Clickable || r.Enabled == nil || !*r.Enabled {
		t.Errorf("root flags = %v %v", r.Clickable, r.Enabled)
	}
	b := r.Children[0]
	want := map[string]string{"bounds": "[10,20][110,70]", "text": "Sign in", "accessibilityText": "sign in button",
		"resource-id": "com.app:id/go", "hintText": "h", "class": "android.widget.Button", "error": "bad", "value": "v"}
	if !mapsEqual(b.Attributes, want) {
		t.Errorf("button attrs = %v", b.Attributes)
	}
	for name, p := range map[string]*bool{"clickable": b.Clickable, "enabled": b.Enabled, "focused": b.Focused, "checked": b.Checked, "selected": b.Selected} {
		if p == nil || !*p {
			t.Errorf("button %s = %v", name, p)
		}
	}
	// JSON keeps false flags (they are pointers) and drops empty children.
	js, _ := json.Marshal(r.Children[1])
	if string(js) != `{"attributes":{"bounds":"[1,2][3,4]","class":"android.widget.EditText"},"clickable":false,"enabled":false,"focused":false,"checked":false,"selected":false}` {
		t.Errorf("field JSON = %s", js)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// An empty screen is just the root with its size.
func TestHierarchyJSONEmpty(t *testing.T) {
	h := hierarchyJSON(&ui.Screen{Width: 10, Height: 20})
	js, _ := json.Marshal(h)
	if string(js) != `{"attributes":{"bounds":"[0,0][10,20]"}}` {
		t.Errorf("JSON = %s", js)
	}
}

// bp returns a pointer to its own copy.
func TestBp(t *testing.T) {
	a, b := bp(true), bp(true)
	if !*a || a == b {
		t.Error("bp")
	}
}

// schema builds an object schema, listing required properties only when given.
func TestSchema(t *testing.T) {
	s := schema(map[string]any{"x": 1})
	if s["type"] != "object" || s["properties"].(map[string]any)["x"] != 1 {
		t.Errorf("schema = %v", s)
	}
	if _, ok := s["required"]; ok {
		t.Error("required present without required properties")
	}
	s = schema(map[string]any{}, "a", "b")
	if r, _ := s["required"].([]string); len(r) != 2 || r[0] != "a" {
		t.Errorf("required = %v", s["required"])
	}
}

// The MCP tool list has unique names and object schemas whose required fields exist.
func TestMCPTools(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range mcpTools {
		if seen[tool.Name] {
			t.Errorf("duplicate tool %q", tool.Name)
		}
		seen[tool.Name] = true
		if tool.Description == "" || tool.InputSchema["type"] != "object" {
			t.Errorf("tool %q: %+v", tool.Name, tool)
		}
		props := tool.InputSchema["properties"].(map[string]any)
		req, _ := tool.InputSchema["required"].([]string)
		for _, r := range req {
			if _, ok := props[r]; !ok {
				t.Errorf("tool %q requires undeclared %q", tool.Name, r)
			}
		}
	}
	for _, name := range []string{"devices", "open", "look", "run", "wait", "save_flow", "close"} {
		if !seen[name] {
			t.Errorf("tool %q missing", name)
		}
	}
	if _, err := json.Marshal(mcpTools); err != nil {
		t.Errorf("tools do not marshal: %v", err)
	}
}

// saveFailure writes the error and every step's result into the flow's debug dir (screens are
// added per device; with none attached only the run's own record is written).
func TestSaveFailureRecord(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "debug", "Login")
	r := orchestrate.Result{Err: errors.New("Element not found: \"Go\""), Steps: []engine.StepResult{
		{Desc: "launchApp", Status: engine.Passed, Duration: 1500 * time.Millisecond},
		{Desc: "tapOn: Go", Status: engine.Failed, Err: "not found", Duration: 17 * time.Second},
	}}
	saveFailure(dir, r, nil)
	b, err := os.ReadFile(filepath.Join(dir, "error.txt"))
	if err != nil || string(b) != "Element not found: \"Go\"\n" {
		t.Errorf("error.txt = %q, %v", b, err)
	}
	b, err = os.ReadFile(filepath.Join(dir, "commands.json"))
	if err != nil {
		t.Fatal(err)
	}
	var steps []map[string]any
	if err := json.Unmarshal(b, &steps); err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 || steps[0]["command"] != "launchApp" || steps[0]["status"] != "passed" || steps[0]["durationMs"] != float64(1500) ||
		steps[1]["status"] != "failed" || steps[1]["error"] != "not found" || steps[1]["durationMs"] != float64(17000) {
		t.Errorf("commands.json = %s", b)
	}
	// Without an error there is no error.txt; without steps commands.json is null.
	dir2 := filepath.Join(t.TempDir(), "d2")
	saveFailure(dir2, orchestrate.Result{}, nil)
	if _, err := os.Stat(filepath.Join(dir2, "error.txt")); !os.IsNotExist(err) {
		t.Error("error.txt written without an error")
	}
	if b, _ := os.ReadFile(filepath.Join(dir2, "commands.json")); string(b) != "null" {
		t.Errorf("commands.json without steps = %q", b)
	}
}

// Every flow run gets a log.txt: a header saying what ran where, the engine's lines, and the
// result. A flow that never started (no device) still gets one, with the reason.
func TestFlowLogs(t *testing.T) {
	t.Setenv("TB_DEBUG", "")
	root := t.TempDir()
	fl := &flow.Flow{Path: "flows/login.yaml", Config: flow.Config{Name: "Login"}}
	j := &orchestrate.Job{Flow: fl}
	dir := filepath.Join(root, "Login")
	logs := newFlowLogs()
	tr := logs.open(dir, j, map[string]device.Info{"": {Platform: "android", Name: "Pixel", Serial: "emulator-5554"}})
	if tr == nil {
		t.Fatal("no trace")
	}
	tr.Printf("", 1, "found something")
	os.WriteFile(filepath.Join(dir, "screen.txt"), []byte("Home\n"), 0o644) // saveFailure's, from the device
	logs.close(dir, orchestrate.Result{Job: j, Status: engine.Failed, Err: errors.New("Element not found: \"Go\""), Duration: 1500 * time.Millisecond})
	b, err := os.ReadFile(filepath.Join(dir, "log.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`tb ` + version + `: "Login" (flows/login.yaml)`, "device: android Pixel (emulator-5554)",
		"  found something", `FAILED in 1.5s: Element not found: "Go"`, "screen*.txt"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("log.txt lacks %q:\n%s", want, b)
		}
	}
	// issue #101: a failure whose device could not be read (lost, not opened again) points at
	// no screen files, since there are none
	dirLost := filepath.Join(root, "Lost")
	logs.open(dirLost, j, map[string]device.Info{"": {Platform: "android", Name: "Pixel", Serial: "emulator-5554"}})
	logs.close(dirLost, orchestrate.Result{Job: j, Status: engine.Failed, Err: errors.New("the connection was lost"), Duration: time.Second})
	if b, _ := os.ReadFile(filepath.Join(dirLost, "log.txt")); !strings.Contains(string(b), "FAILED") || strings.Contains(string(b), "screen*.txt") {
		t.Errorf("log.txt points at screen files that were not saved:\n%s", b)
	}
	// A passing run, with roles.
	dir2 := filepath.Join(root, "Chat")
	j2 := &orchestrate.Job{Flow: &flow.Flow{Path: "chat.yaml", Config: flow.Config{Name: "Chat"}}, Attempt: 1}
	logs.open(dir2, j2, map[string]device.Info{"bob": {Platform: "ios", Name: "iPhone", Serial: "UDID"}, "alice": {Platform: "web", Name: "Chrome", Serial: "web"}})
	logs.close(dir2, orchestrate.Result{Job: j2, Status: engine.Passed, Duration: time.Second})
	b, _ = os.ReadFile(filepath.Join(dir2, "log.txt"))
	if s := string(b); !strings.Contains(s, "role alice: web Chrome") || strings.Index(s, "role alice") > strings.Index(s, "role bob") ||
		!strings.Contains(s, "retry 1") || !strings.Contains(s, "PASSED in 1s") {
		t.Errorf("log.txt:\n%s", s)
	}
	// Never started: the reason is logged.
	dir3 := filepath.Join(root, "NoDevice")
	logs.close(dir3, orchestrate.Result{Job: j, Status: engine.Failed, Err: errors.New("no free android device")})
	if b, _ := os.ReadFile(filepath.Join(dir3, "log.txt")); !strings.Contains(string(b), "FAILED in 0s: no free android device") {
		t.Errorf("never started: %q", b)
	}
}

// Runs started in the same second get their own debug folders.
func TestClaimDir(t *testing.T) {
	parent := t.TempDir()
	a, b, c := claimDir(parent, "2026-11-03_171702"), claimDir(parent, "2026-11-03_171702"), claimDir(parent, "2026-11-03_171702")
	if a == b || b == c || filepath.Base(b) != "2026-11-03_171702-2" || filepath.Base(c) != "2026-11-03_171702-3" {
		t.Errorf("dirs %s %s %s", a, b, c)
	}
	for _, d := range []string{a, b, c} {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			t.Errorf("%s not made: %v", d, err)
		}
	}
}
