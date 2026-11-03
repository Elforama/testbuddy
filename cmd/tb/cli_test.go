package main

import (
	"bufio"
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
		{"look unknown flag", func() int { return agentCall("look", []string{"--bogus"}, []flagSpec{{[]string{"shot"}, false}}) }, "unknown option --bogus"},
		{"open missing device", func() int { return agentCall("open", []string{"-d"}, nil) }, "-d needs a value"},
		{"watch bad flag", func() int { return agentCall("watch", []string{"--until"}, []flagSpec{{[]string{"until"}, true}}) }, "--until needs a value"},
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
	r := mcpCall("frobnicate", map[string]any{"x": 1})
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
	var replies []map[string]json.RawMessage
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("bad reply %q: %v", sc.Text(), err)
		}
		replies = append(replies, m)
	}
	if len(replies) != 5 {
		t.Fatalf("replies = %d:\n%s", len(replies), out)
	}
	for i, id := range []string{"1", `"two"`, "3", "4", "5"} {
		if string(replies[i]["id"]) != id || string(replies[i]["jsonrpc"]) != `"2.0"` {
			t.Errorf("reply %d id/jsonrpc = %s/%s", i, replies[i]["id"], replies[i]["jsonrpc"])
		}
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
