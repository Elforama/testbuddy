package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// capture runs fn with stdout and stderr redirected, returning what it printed.
func capture(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	outC, errC := make(chan string), make(chan string)
	go func() { b, _ := io.ReadAll(rOut); outC <- string(b) }()
	go func() { b, _ := io.ReadAll(rErr); errC <- string(b) }()
	os.Stdout, os.Stderr = wOut, wErr
	func() {
		defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
		fn()
	}()
	wOut.Close()
	wErr.Close()
	return <-outC, <-errC
}

// withStdin runs fn reading the given text as stdin.
func withStdin(t *testing.T, text string, fn func()) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "stdin")
	os.WriteFile(p, []byte(text), 0o600)
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	old := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = old }()
	fn()
}

// `tb agent-guide` prints the guide for AGENTS.md and exits 0.
func TestCmdGuide(t *testing.T) {
	var code int
	out, _ := capture(t, func() { code = cmdGuide(nil) })
	if code != 0 || out != agentGuide {
		t.Errorf("code %d, out %q", code, out)
	}
	for _, want := range []string{"tb look", "tb do", "tb wait", "tb save", "tb test", "Exit codes: 0 ok, 1 failed, 2 bad input"} {
		if !strings.Contains(agentGuide, want) {
			t.Errorf("guide lacks %q", want)
		}
	}
}

// An unknown WebView mode is noted. The flow still runs with accessibility.
func TestCmdCheckNotesIgnoredKeys(t *testing.T) {
	f := filepath.Join(t.TempDir(), "web.yaml")
	os.WriteFile(f, []byte("appId: com.app\nandroidWebViewHierarchy: unknown\n---\n- launchApp\n"), 0o644)
	var code int
	out, _ := capture(t, func() { code = cmdCheck([]string{f}) })
	if code != 0 || !strings.HasPrefix(out, "ok "+f+"\n") || !strings.Contains(out, "androidWebViewHierarchy") || !strings.Contains(out, "ignored") {
		t.Errorf("code %d, out %q: want ok and a note that androidWebViewHierarchy is ignored", code, out)
	}
}

// `tb check` prints ok per valid flow and the error per bad one, exiting 1 if any is bad.
func TestCmdCheck(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.yaml")
	bad := filepath.Join(dir, "bad.yaml")
	os.WriteFile(good, []byte("appId: com.app\n---\n- launchApp\n- tapOn: Login\n"), 0o644)
	os.WriteFile(bad, []byte("appId: com.app\n---\n- notARealCommand: 1\n"), 0o644)
	missing := filepath.Join(dir, "missing.yaml")

	var code int
	out, _ := capture(t, func() { code = cmdCheck([]string{good}) })
	if code != 0 || out != "ok "+good+"\n" {
		t.Errorf("good: code %d, out %q", code, out)
	}
	out, _ = capture(t, func() { code = cmdCheck([]string{good, bad, missing}) })
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if code != 1 || len(lines) != 3 || lines[0] != "ok "+good {
		t.Fatalf("mixed: code %d, out %q", code, out)
	}
	if !strings.Contains(lines[1], "bad.yaml") || strings.HasPrefix(lines[1], "ok ") {
		t.Errorf("bad flow line = %q", lines[1])
	}
	if !strings.Contains(lines[2], "missing.yaml") {
		t.Errorf("missing flow line = %q", lines[2])
	}
	out, _ = capture(t, func() { code = cmdCheck(nil) })
	if code != 0 || out != "" {
		t.Errorf("no files: code %d, out %q", code, out)
	}
}

// `tb check --lint` lists fragile steps under each flow. Warnings fail the check only with
// --fail-on warning; a flow that does not parse still fails it.
func TestCmdCheckLint(t *testing.T) {
	dir := t.TempDir()
	fragile := filepath.Join(dir, "fragile.yaml")
	clean := filepath.Join(dir, "clean.yaml")
	os.WriteFile(fragile, []byte("appId: com.app\n---\n- launchApp\n- tapOn: {point: \"50%,50%\"}\n- tapOn: 'Sign in?'\n"), 0o644)
	os.WriteFile(clean, []byte("appId: com.app\n---\n- launchApp\n- tapOn: Login\n- assertVisible: Welcome\n"), 0o644)

	var code int
	out, _ := capture(t, func() { code = cmdCheck([]string{"--lint", fragile, clean}) })
	want := "ok " + fragile + "\n" +
		"  ! :4 coordinate_tap: Tap depends on coordinates. Prefer an element selector.\n" +
		"  ! :5 regex_text: "
	if code != 0 || !strings.HasPrefix(out, want) {
		t.Fatalf("lint: code %d, out:\n%s", code, out)
	}
	for _, w := range []string{"  ! missing_assertion: ", "ok " + clean + "\n", "! 3 warnings (2 flows checked)\n"} {
		if !strings.Contains(out, w) {
			t.Errorf("lint output lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out[strings.Index(out, "ok "+clean):], ":4") {
		t.Errorf("the clean flow got warnings:\n%s", out)
	}

	out, _ = capture(t, func() { code = cmdCheck([]string{"--lint", "--fail-on", "warning", fragile}) })
	if code != 1 {
		t.Errorf("--fail-on warning: code %d, out %q", code, out)
	}
	out, _ = capture(t, func() { code = cmdCheck([]string{"--lint", "--fail-on", "warning", clean}) })
	if code != 0 || out != "ok "+clean+"\nok: 1 flow checked, no warnings\n" {
		t.Errorf("clean --fail-on warning: code %d, out %q", code, out)
	}
	// Without --lint, check is unchanged: no warnings.
	out, _ = capture(t, func() { code = cmdCheck([]string{fragile}) })
	if code != 0 || out != "ok "+fragile+"\n" {
		t.Errorf("no --lint: code %d, out %q", code, out)
	}
	if code := cmdCheck([]string{"--fail-on", "sometimes", clean}); code != 2 {
		t.Errorf("bad --fail-on: code %d, want 2", code)
	}
	if code := cmdCheck([]string{"--nope"}); code != 2 {
		t.Errorf("unknown option: code %d, want 2", code)
	}
}

// `tb check --lint --json` prints one object: each flow with its error or warnings.
func TestCmdCheckLintJSON(t *testing.T) {
	dir := t.TempDir()
	fragile := filepath.Join(dir, "fragile.yaml")
	bad := filepath.Join(dir, "bad.yaml")
	os.WriteFile(fragile, []byte("appId: com.app\n---\n- tapOn: {text: OK, index: 1}\n- assertVisible: Done\n"), 0o644)
	os.WriteFile(bad, []byte("appId: com.app\n---\n- notARealCommand: 1\n"), 0o644)
	var code int
	out, _ := capture(t, func() { code = cmdCheck([]string{"--lint", "--json", fragile, bad}) })
	var got struct {
		Errors   int `json:"errors"`
		Warnings int `json:"warnings"`
		Files    []struct {
			File     string `json:"file"`
			OK       bool   `json:"ok"`
			Error    string `json:"error"`
			Warnings []struct {
				Code string `json:"code"`
				Line int    `json:"line"`
				Step int    `json:"step"`
			} `json:"warnings"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	if code != 1 || got.Errors != 1 || got.Warnings != 1 || len(got.Files) != 2 {
		t.Fatalf("code %d, %+v", code, got)
	}
	f, b := got.Files[0], got.Files[1]
	if f.File != fragile || !f.OK || len(f.Warnings) != 1 || f.Warnings[0].Code != "indexed_selector" ||
		f.Warnings[0].Line != 3 || f.Warnings[0].Step != 1 {
		t.Errorf("fragile: %+v", f)
	}
	if b.OK || !strings.Contains(b.Error, "notARealCommand") {
		t.Errorf("bad: %+v", b)
	}
}

// `tb test` usage paths: -h prints usage (0); no flows or a bad option is bad input (2).
func TestCmdTestUsage(t *testing.T) {
	var code int
	out, _ := capture(t, func() { code = cmdTest([]string{"-h"}) })
	if code != 0 || out != testUsage {
		t.Errorf("-h: code %d, out %q", code, out)
	}
	out, _ = capture(t, func() { code = cmdTest([]string{"--help", "flows/"}) })
	if code != 0 || out != testUsage {
		t.Errorf("--help with flows: code %d", code)
	}
	out, _ = capture(t, func() { code = cmdTest(nil) })
	if code != 2 || out != testUsage {
		t.Errorf("no flows: code %d, out %q", code, out)
	}
	_, errOut := capture(t, func() { code = cmdTest([]string{"flows/", "--bogus"}) })
	if code != 2 || !strings.HasPrefix(errOut, "unknown option --bogus\n") || !strings.HasSuffix(errOut, testUsage) {
		t.Errorf("bad option: code %d, stderr %q", code, errOut)
	}
	_, errOut = capture(t, func() { code = cmdTest([]string{"flows/", "--format"}) })
	if code != 2 || !strings.HasPrefix(errOut, "--format needs a value\n") {
		t.Errorf("missing value: code %d, stderr %q", code, errOut)
	}
}

// `tb test` rejects flows it cannot load before looking for devices.
func TestCmdTestBadFlows(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	os.WriteFile(bad, []byte("appId: x\n---\n- notARealCommand: 1\n"), 0o644)
	good := filepath.Join(dir, "good.yaml")
	os.WriteFile(good, []byte("appId: x\n---\n- launchApp\n"), 0o644)
	cases := map[string][]string{
		"missing path":   {filepath.Join(dir, "nope")},
		"bad flow":       {bad},
		"missing config": {good, "--config", filepath.Join(dir, "no-config.yaml")},
	}
	for name, argv := range cases {
		t.Run(name, func(t *testing.T) {
			var code int
			_, errOut := capture(t, func() { code = cmdTest(argv) })
			if code != 2 || errOut == "" {
				t.Errorf("code %d, stderr %q", code, errOut)
			}
		})
	}
}

// Device commands reject bad input with exit 2 before contacting the daemon.
func TestCommandsRejectBadInput(t *testing.T) {
	cases := []struct {
		name    string
		run     func() int
		wantErr string
	}{
		{"look unknown flag", func() int { return agentCall("look", []string{"--bogus"}, []flagSpec{{[]string{"shot"}, boolFlag}}) }, "unknown option --bogus"},
		{"open missing device", func() int { return agentCall("open", []string{"-d"}, nil) }, "-d needs a value"},
		{"watch bad flag", func() int { return agentCall("watch", []string{"--until"}, []flagSpec{{[]string{"until"}, textFlag}}) }, "--until needs a value"},
		{"do without commands", func() int { return cmdDo(nil) }, "usage: tb do"},
		{"do bad flag", func() int { return cmdDo([]string{"--x"}) }, "unknown option --x"},
		{"wait without target", func() int { return cmdWait(nil) }, "usage: tb wait"},
		{"wait bad flag", func() int { return cmdWait([]string{"Hi", "--nope"}) }, "usage: tb wait"},
		{"save without path", func() int { return cmdSave(nil) }, "usage: tb save"},
		{"save bad flag", func() int { return cmdSave([]string{"f.yaml", "--z"}) }, "usage: tb save"},
		{"hierarchy bad flag", func() int { return cmdHierarchy([]string{"--z"}) }, "unknown option --z"},
		{"devices bad flag", func() int { return cmdDevices([]string{"--z"}) }, "unknown option --z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var code int
			out, errOut := capture(t, func() { code = c.run() })
			if code != 2 {
				t.Errorf("code = %d", code)
			}
			if !strings.Contains(errOut, c.wantErr) {
				t.Errorf("stderr = %q, want %q", errOut, c.wantErr)
			}
			if out != "" {
				t.Errorf("stdout = %q", out)
			}
		})
	}
}

// `tb daemon status` and `tb daemon stop` report a daemon that is not running (exit 0).
func TestCmdDaemonNotRunning(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	for _, sub := range []string{"status", "stop"} {
		var code int
		out, _ := capture(t, func() { code = cmdDaemon([]string{sub}) })
		if code != 0 || out != "tb daemon is not running\n" {
			t.Errorf("%s: code %d, out %q", sub, code, out)
		}
	}
}

// An unknown MCP tool is an error result, without contacting the daemon.
func TestMCPCallUnknownTool(t *testing.T) {
	r := mcpCall(context.Background(), "frobnicate", map[string]any{"x": 1})
	content := r["content"].([]map[string]any)
	if len(content) != 1 || content[0]["type"] != "text" || content[0]["text"] != "unknown tool frobnicate" || r["isError"] != true {
		t.Errorf("result = %v", r)
	}
}

// The MCP server answers initialize, ping, tools/list and tools/call, ignores notifications and
// garbage, returns method-not-found otherwise, and exits at end of input.
func TestCmdMCP(t *testing.T) {
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`this is not json`,
		`{"jsonrpc":"2.0","id":"two","method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"resources/list"}`, // last line without a newline
	}, "\n")
	var code int
	var out string
	withStdin(t, in, func() { out, _ = capture(t, func() { code = cmdMCP(nil) }) })
	if code != 0 {
		t.Errorf("code = %d", code)
	}
	// Replies may come out of order (ping, tools/list and unknown methods are answered at once,
	// initialize and tools/call in turn), so they are matched by ID.
	byID := map[string]map[string]json.RawMessage{}
	n := 0
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("bad reply %q: %v", sc.Text(), err)
		}
		byID[string(m["id"])] = m
		n++
	}
	if n != 5 {
		t.Fatalf("replies = %d:\n%s", n, out)
	}
	var replies []map[string]json.RawMessage
	for _, id := range []string{"1", `"two"`, "3", "4", "5"} {
		r := byID[id]
		if r == nil || string(r["jsonrpc"]) != `"2.0"` {
			t.Fatalf("reply to %s = %v\n%s", id, r, out)
		}
		replies = append(replies, r)
	}
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools map[string]any `json:"tools"`
		} `json:"capabilities"`
		ServerInfo   struct{ Name, Version string } `json:"serverInfo"`
		Instructions string                         `json:"instructions"`
	}
	json.Unmarshal(replies[0]["result"], &init)
	if init.ProtocolVersion != "2025-06-18" || init.Capabilities.Tools == nil || init.ServerInfo.Name != "testbuddy" ||
		init.ServerInfo.Version != version || init.Instructions == "" {
		t.Errorf("initialize = %s", replies[0]["result"])
	}
	if string(replies[1]["result"]) != "{}" {
		t.Errorf("ping = %s", replies[1]["result"])
	}
	var list struct{ Tools []mcpTool }
	json.Unmarshal(replies[2]["result"], &list)
	if len(list.Tools) != len(mcpTools) || list.Tools[0].Name != mcpTools[0].Name {
		t.Errorf("tools/list = %s", replies[2]["result"])
	}
	var call struct {
		Content []struct{ Type, Text string }
		IsError bool `json:"isError"`
	}
	json.Unmarshal(replies[3]["result"], &call)
	if !call.IsError || len(call.Content) != 1 || call.Content[0].Text != "unknown tool nope" {
		t.Errorf("tools/call = %s", replies[3]["result"])
	}
	var e struct {
		Code    int
		Message string
	}
	json.Unmarshal(replies[4]["error"], &e)
	if e.Code != -32601 || e.Message != "method not found" || replies[4]["result"] != nil {
		t.Errorf("unknown method = %s", replies[4]["error"])
	}
}

// With no input at all the MCP server exits cleanly.
func TestCmdMCPEmptyInput(t *testing.T) {
	var code int
	var out string
	withStdin(t, "", func() { out, _ = capture(t, func() { code = cmdMCP(nil) }) })
	if code != 0 || out != "" {
		t.Errorf("code %d, out %q", code, out)
	}
}

// The usage text lists every top-level command main dispatches.
func TestUsageMentionsCommands(t *testing.T) {
	for _, cmd := range []string{"tb test", "tb check", "tb devices", "tb open", "tb look", "tb do", "tb wait", "tb watch",
		"tb shot", "tb save", "tb log", "tb close", "tb hierarchy", "tb mcp", "tb agent-guide", "tb daemon stop"} {
		if !strings.Contains(usage, cmd) {
			t.Errorf("usage lacks %q", cmd)
		}
	}
	for _, opt := range []string{"--env", "--platform", "--device", "--include-tags", "--exclude-tags", "--shard-split",
		"--shard-all", "--format", "--output", "--debug-output", "--retries", "--json", "--quiet"} {
		if !strings.Contains(testUsage, opt) {
			t.Errorf("test usage lacks %q", opt)
		}
	}
}

// `tb check <folder>` checks every flow in it and its sub-folders, but not config.yaml.
func TestCmdCheckFolder(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	a := filepath.Join(dir, "a.yaml")
	b := filepath.Join(dir, "sub", "b.yml")
	os.WriteFile(a, []byte("appId: com.app\n---\n- launchApp\n"), 0o644)
	os.WriteFile(b, []byte("appId: com.app\n---\n- tapOn: Login\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("flows:\n  - \"*\"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a flow"), 0o644)
	var code int
	out, _ := capture(t, func() { code = cmdCheck([]string{dir}) })
	if code != 0 || out != "ok "+a+"\nok "+b+"\n" {
		t.Errorf("folder: code %d, out %q", code, out)
	}
	os.WriteFile(filepath.Join(dir, "sub", "bad.yaml"), []byte("appId: com.app\n---\n- notARealCommand: 1\n"), 0o644)
	out, _ = capture(t, func() { code = cmdCheck([]string{dir}) })
	if code != 1 || !strings.Contains(out, "bad.yaml") {
		t.Errorf("folder with a bad flow: code %d, out %q", code, out)
	}
	// A folder that does not exist stays as given, so its error shows.
	out, _ = capture(t, func() { code = cmdCheck([]string{filepath.Join(dir, "nope")}) })
	if code != 1 || !strings.Contains(out, "nope") {
		t.Errorf("missing folder: code %d, out %q", code, out)
	}
}

// A number flag with a value that is not a number is bad input (exit 2, naming the flag and
// the value), not read as 0: issue #62, where `tb test flows/ --retries two --shard-split 3x`
// ran with no retries and no sharding without a word. tb test checks before it runs a flow;
// the device commands before they reach the daemon.
func TestNumberFlagsRejectBadValues(t *testing.T) {
	withFakePhone(t)
	dir := t.TempDir()
	flow := writeFlow(t, dir, "pass.yaml", passingFlow)
	f := startFakeDaemon(t)
	test := func(argv ...string) func() int {
		return func() int {
			return cmdTest(append([]string{flow, "--json", "--debug-output", filepath.Join(dir, "debug")}, argv...))
		}
	}
	cases := []struct {
		name    string
		run     func() int
		wantErr string
	}{
		{"test --retries", test("--retries", "two"), `--retries: want a number, not "two"`},
		{"test --shard-split", test("--shard-split", "3x"), `--shard-split: want a number, not "3x"`},
		{"test --shard-split alias", test("-s", "1.5"), `-s: want a number, not "1.5"`},
		{"test --shard-all", test("--shard-all=many"), `--shard-all: want a number, not "many"`},
		{"test --retries empty", test("--retries="), `--retries: want a number, not ""`},
		{"test --retries negative", test("--retries", "-1"), `--retries: want 0 or more, not "-1"`},
		{"wait --timeout", func() int { return cmdWait([]string{"Hi", "--timeout", "soon"}) }, `--timeout: want a number, not "soon"`},
		{"wait -t", func() int { return cmdWait([]string{"Hi", "-t", "5s"}) }, `-t: want a number, not "5s"`},
		{"watch --timeout", func() int { return agentCall("watch", []string{"--timeout", "5s"}, watchFlags) }, `--timeout: want a number, not "5s"`},
		{"shot --scale", func() int { return agentCall("shot", []string{"--scale", "half"}, shotFlags) }, `--scale: want a number, not "half"`},
		{"look --scale", func() int { return agentCall("look", []string{"--scale=-0.5"}, lookFlags) }, `--scale: want 0 to 1, not "-0.5"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var code int
			out, errOut := capture(t, func() { code = c.run() })
			if code != 2 || !strings.HasPrefix(errOut, c.wantErr+"\n") {
				t.Errorf("code %d, stderr %q; want 2 and %q\nstdout: %s", code, errOut, c.wantErr, out)
			}
		})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) != 0 {
		t.Errorf("the daemon got %d requests; bad input must stop before it", len(f.reqs))
	}
}

// --screen-size, a boolean flag's value and --scale's upper bound are checked as strictly as
// numbers: tb test --screen-size big ran at 1280x800, --json=yes meant false, and
// tb shot --scale 2 was sent on (issue #151). Each is a usage error, exit 2, before any work.
func TestFlagValuesRejectBadValues(t *testing.T) {
	withFakePhone(t)
	t.Setenv("TB_WEB_SIZE", "")
	dir := t.TempDir()
	flow := writeFlow(t, dir, "pass.yaml", passingFlow)
	f := startFakeDaemon(t)
	test := func(argv ...string) func() int {
		return func() int {
			return cmdTest(append([]string{flow, "--debug-output", filepath.Join(dir, "debug")}, argv...))
		}
	}
	const sizeWant = "want WIDTHxHEIGHT, each a whole number from 200 to 10000, such as 1280x800"
	cases := []struct {
		name    string
		run     func() int
		wantErr string
	}{
		{"test --screen-size word", test("--screen-size", "big"), `--screen-size: ` + sizeWant + `, not "big"`},
		{"test --screen-size one number", test("--screen-size", "1280"), `--screen-size: ` + sizeWant + `, not "1280"`},
		{"test --screen-size zero", test("--screen-size=0x800"), `--screen-size: ` + sizeWant + `, not "0x800"`},
		{"test --screen-size negative", test("--screen-size", "-1x800"), `--screen-size: ` + sizeWant + `, not "-1x800"`},
		{"test --screen-size three numbers", test("--screen-size", "1280x800x2"), `--screen-size: ` + sizeWant + `, not "1280x800x2"`},
		{"test --screen-size fraction", test("--screen-size", "1280.5x800"), `--screen-size: ` + sizeWant + `, not "1280.5x800"`},
		{"test --screen-size too big", test("--screen-size", "100000x800"), `--screen-size: ` + sizeWant + `, not "100000x800"`},
		{"test --screen-size too small", test("--screen-size", "1280x80"), `--screen-size: ` + sizeWant + `, not "1280x80"`},
		{"test --screen-size empty", test("--screen-size="), `--screen-size: ` + sizeWant + `, not ""`},
		{"test --json=yes", test("--json=yes"), `--json: want true or false, not "yes"`},
		{"test -q=no", test("-q=no"), `-q: want true or false, not "no"`},
		{"look --json=on", func() int { return agentCall("look", []string{"--json=on"}, lookFlags) }, `--json: want true or false, not "on"`},
		{"shot --scale 2", func() int { return agentCall("shot", []string{"--scale", "2"}, shotFlags) }, `--scale: want 0 to 1, not "2"`},
		{"look --scale 1.5", func() int { return agentCall("look", []string{"--scale=1.5"}, lookFlags) }, `--scale: want 0 to 1, not "1.5"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var code int
			out, errOut := capture(t, func() { code = c.run() })
			if code != 2 || !strings.HasPrefix(errOut, c.wantErr+"\n") {
				t.Errorf("code %d, stderr %q; want 2 and %q\nstdout: %s", code, errOut, c.wantErr, out)
			}
		})
	}
	if v := os.Getenv("TB_WEB_SIZE"); v != "" {
		t.Errorf("TB_WEB_SIZE = %q: a bad --screen-size was passed on", v)
	}
	f.mu.Lock()
	n := len(f.reqs)
	f.mu.Unlock()
	if n != 0 {
		t.Errorf("the daemon got %d requests; bad input must stop before it", n)
	}

	// Good values still go through: a scale from 0 to 1 is sent as a number, --json=false
	// is false, and a good --screen-size reaches the web driver.
	for v, want := range map[string]float64{"0": 0, "0.5": 0.5, "1": 1} {
		if code := agentCall("shot", []string{"--scale", v}, shotFlags); code != 0 {
			t.Fatalf("shot --scale %s: code %d", v, code)
		}
		if _, args, _ := f.last(t); args["scale"] != want {
			t.Errorf("shot --scale %s sent %v", v, args)
		}
	}
	if code := agentCall("look", []string{"--json=false"}, lookFlags); code != 0 {
		t.Fatalf("look --json=false: code %d", code)
	}
	if _, args, _ := f.last(t); args["json"] == true {
		t.Errorf("look --json=false sent %v", args)
	}
	var code int
	capture(t, func() { code = test("--screen-size", "390x844")() })
	if code != 0 || os.Getenv("TB_WEB_SIZE") != "390x844" {
		t.Errorf("--screen-size 390x844: code %d, TB_WEB_SIZE %q", code, os.Getenv("TB_WEB_SIZE"))
	}
}
