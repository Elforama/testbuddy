package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
)

type fakeDaemon struct {
	mu    sync.Mutex
	reqs  []daemon.Request
	resp  map[string]daemon.Response
	parts map[string][]string
	hook  func(ctx context.Context, cmd string)
}

func (f *fakeDaemon) last(t *testing.T) (string, map[string]any, string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) == 0 {
		t.Fatal("daemon got no request")
	}
	r := f.reqs[len(f.reqs)-1]
	var args map[string]any
	if len(r.Args) > 0 {
		if err := json.Unmarshal(r.Args, &args); err != nil {
			t.Fatalf("args %s: %v", r.Args, err)
		}
	}
	return r.Cmd, args, r.Cwd
}

func startFakeDaemon(t *testing.T) *fakeDaemon {
	t.Helper()
	dir, err := os.MkdirTemp("", "tbfd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("TB_HOME", dir)
	f := &fakeDaemon{resp: map[string]daemon.Response{}, parts: map[string][]string{}}
	srv := daemon.NewServer(time.Hour)
	srv.Handle("ping", func(context.Context, daemon.Request, func(daemon.Response)) daemon.Response {
		return daemon.Response{OK: true, Text: daemon.BuildID()}
	})
	for _, cmd := range []string{"open", "close", "look", "locate", "watch", "shot", "log", "applog", "clear", "do", "save", "devices", "stop", "yield"} {
		cmd := cmd
		srv.Handle(cmd, func(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
			f.mu.Lock()
			f.reqs = append(f.reqs, req)
			parts := f.parts[cmd]
			resp, canned := f.resp[cmd]
			hook := f.hook
			f.mu.Unlock()
			if hook != nil {
				hook(ctx, cmd)
			}
			for _, p := range parts {
				send(daemon.Response{Text: p})
			}
			if !canned {
				resp = daemon.Response{OK: true, Text: cmd + " done\n"}
			}
			if cmd == "stop" {
				daemon.StopAfterReply(ctx)
			}
			return resp
		})
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve() }()
	for i := 0; !daemon.Running(); i++ {
		if i > 1000 {
			t.Fatal("fake daemon did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Cleanup(func() { srv.Stop(); <-done })
	return f
}

func (f *fakeDaemon) answer(cmd string, r daemon.Response, parts ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resp[cmd] = r
	f.parts[cmd] = parts
}

func TestAgentCallArgs(t *testing.T) {
	f := startFakeDaemon(t)
	wd, _ := os.Getwd()
	cases := []struct {
		name  string
		cmd   string
		argv  []string
		extra []flagSpec
		want  map[string]any
	}{
		{"look with flags", "look", []string{"-d", "alice", "--shot", "--scale", "0.5", "-p", "ios"}, lookFlags,
			map[string]any{"device": "alice", "platform": "ios", "shot": true, "scale": 0.5}},
		{"unset flags are left out", "look", nil, lookFlags, map[string]any{"device": "", "platform": ""}},
		{"false bool is left out", "look", []string{"--all=false"}, lookFlags, map[string]any{"device": "", "platform": ""}},
		{"watch numbers", "watch", []string{"--until", "Done", "--timeout", "3000"},
			watchFlags,
			map[string]any{"device": "", "platform": "", "until": "Done", "timeout": float64(3000)}},
		{"shot path", "shot", []string{"out.png", "--scale", "0.25"}, shotFlags,
			map[string]any{"device": "", "platform": "", "path": "out.png", "scale": 0.25}},
		{"applog flags", "applog", []string{"-d", "alice", "--since", "30", "--grep", "Net|Db", "--app", "com.app"}, appLogFlags,
			map[string]any{"device": "alice", "platform": "", "since": float64(30), "grep": "Net|Db", "app": "com.app"}},
		{"close all", "close", []string{"all"}, nil, map[string]any{"device": "all", "platform": ""}},
		{"close one", "close", []string{"-d", "bob"}, nil, map[string]any{"device": "bob", "platform": ""}},
		{"open positional device", "open", []string{"Pixel 9", "--name", "alice", "--app", "com.app"},
			[]flagSpec{{[]string{"name", "n"}, textFlag}, {[]string{"app", "a"}, textFlag}},
			map[string]any{"device": "Pixel 9", "platform": "", "name": "alice", "app": "com.app"}},
		{"open -d wins over positional", "open", []string{"other", "-d", "Pixel"}, []flagSpec{{[]string{"name", "n"}, textFlag}},
			map[string]any{"device": "Pixel", "platform": ""}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var code int
			out, _ := capture(t, func() { code = agentCall(c.cmd, c.argv, c.extra) })
			if code != 0 || out != c.cmd+" done\n" {
				t.Errorf("code %d, out %q", code, out)
			}
			cmd, args, cwd := f.last(t)
			if cmd != c.cmd || cwd != wd {
				t.Errorf("cmd/cwd = %q/%q", cmd, cwd)
			}
			if len(args) != len(c.want) {
				t.Errorf("args = %v, want %v", args, c.want)
			}
			for k, v := range c.want {
				if args[k] != v {
					t.Errorf("args[%s] = %#v, want %#v", k, args[k], v)
				}
			}
		})
	}
}

func TestCallDaemonStreaming(t *testing.T) {
	f := startFakeDaemon(t)
	f.answer("watch", daemon.Response{OK: true, Exit: 0, Text: "stopped\n"}, "+ Button \"A\"\n", "- Text \"B\"\n")
	var code int
	out, _ := capture(t, func() { code = agentCall("watch", nil, nil) })
	if code != 0 || out != "+ Button \"A\"\n- Text \"B\"\nstopped\n" {
		t.Errorf("code %d, out %q", code, out)
	}
}

func TestCallDaemonFailure(t *testing.T) {
	f := startFakeDaemon(t)
	f.answer("look", daemon.Response{OK: false, Exit: 2, Text: "no device named alice"})
	var code int
	out, errOut := capture(t, func() { code = agentCall("look", []string{"-d", "alice"}, nil) })
	if code != 2 || out != "" || errOut != "no device named alice\n" {
		t.Errorf("code %d, out %q, err %q", code, out, errOut)
	}
	f.answer("look", daemon.Response{OK: false, Exit: 1, Text: "FAILED\n"})
	_, errOut = capture(t, func() { code = agentCall("look", nil, nil) })
	if code != 1 || errOut != "FAILED\n" {
		t.Errorf("code %d, err %q", code, errOut)
	}
	f.answer("do", daemon.Response{OK: true, Exit: 1, Text: "FAILED tapOn\n"})
	out, errOut = capture(t, func() { code = cmdDo([]string{"tapOn: x"}) })
	if code != 1 || out != "FAILED tapOn\n" || errOut != "" {
		t.Errorf("code %d, out %q, err %q", code, out, errOut)
	}
}

func TestCallDaemonJSON(t *testing.T) {
	f := startFakeDaemon(t)
	f.answer("look", daemon.Response{OK: true, Exit: 0, Text: "human text", JSON: json.RawMessage(`{"elements":[]}`)})
	var code int
	out, _ := capture(t, func() { code = agentCall("look", []string{"--json"}, lookFlags) })
	if code != 0 || out != "{\"elements\":[]}\n" {
		t.Errorf("code %d, out %q", code, out)
	}
	f.answer("look", daemon.Response{OK: true, Text: "only text\n"})
	out, _ = capture(t, func() { code = agentCall("look", []string{"--json"}, lookFlags) })
	if out != "only text\n" {
		t.Errorf("out %q", out)
	}
}

func TestCmdDoArgs(t *testing.T) {
	f := startFakeDaemon(t)
	var code int
	capture(t, func() {
		code = cmdDo([]string{"-d", "alice", "tapOn: Login", "-e", "USER=ann", "--app", "com.app", "inputText: hi", "-e", "Q=a=b", "-p", "android"})
	})
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	cmd, args, _ := f.last(t)
	if cmd != "do" || args["device"] != "alice" || args["platform"] != "android" || args["app"] != "com.app" {
		t.Errorf("cmd %q args %v", cmd, args)
	}
	if args["commands"] != "- tapOn: Login\n- inputText: hi\n" {
		t.Errorf("commands = %q", args["commands"])
	}
	env, _ := args["env"].(map[string]any)
	if len(env) != 2 || env["USER"] != "ann" || env["Q"] != "a=b" {
		t.Errorf("env = %v", args["env"])
	}
}

func TestCmdDoStdin(t *testing.T) {
	f := startFakeDaemon(t)
	withStdin(t, "- launchApp\n- tapOn: Go\n", func() {
		capture(t, func() { cmdDo([]string{"-", "back"}) })
	})
	_, args, _ := f.last(t)
	if args["commands"] != "- launchApp\n- tapOn: Go\n- back\n" {
		t.Errorf("commands = %q", args["commands"])
	}
	if env, _ := args["env"].(map[string]any); len(env) != 0 {
		t.Errorf("env = %v", args["env"])
	}
}

func TestCmdWaitCommands(t *testing.T) {
	f := startFakeDaemon(t)
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"Welcome", "back"}, "- extendedWaitUntil:\n    visible: \"Welcome back\"\n    timeout: 17000\n"},
		{[]string{"Spinner", "--gone", "-t", "500"}, "- extendedWaitUntil:\n    notVisible: \"Spinner\"\n    timeout: 500\n"},
		{[]string{"@5"}, "- extendedWaitUntil:\n    visible: @5\n    timeout: 17000\n"},
		{[]string{"{id: done}", "--timeout", "100"}, "- extendedWaitUntil:\n    visible: {id: done}\n    timeout: 100\n"},
		{[]string{`Say "hi"`}, "- extendedWaitUntil:\n    visible: \"Say \\\"hi\\\"\"\n    timeout: 17000\n"},
	}
	for _, c := range cases {
		capture(t, func() { cmdWait(append([]string{"-d", "alice"}, c.argv...)) })
		cmd, args, _ := f.last(t)
		if cmd != "do" || args["device"] != "alice" || args["commands"] != c.want {
			t.Errorf("wait %q: cmd %q commands %q, want %q", c.argv, cmd, args["commands"], c.want)
		}
	}
}

func TestCmdSaveArgs(t *testing.T) {
	f := startFakeDaemon(t)
	capture(t, func() { cmdSave([]string{"flows/new.yaml", "-d", "alice", "-a", "com.app"}) })
	cmd, args, _ := f.last(t)
	if cmd != "save" || args["path"] != "flows/new.yaml" || args["device"] != "alice" || args["app"] != "com.app" {
		t.Errorf("cmd %q args %v", cmd, args)
	}
}

func TestCommandsWithRunningDaemon(t *testing.T) {
	f := startFakeDaemon(t)
	var code int
	out, _ := capture(t, func() { code = cmdDevices([]string{"--json"}) })
	if code != 0 || out != "devices done\n" {
		t.Errorf("devices: code %d out %q", code, out)
	}
	if cmd, args, _ := f.last(t); cmd != "devices" || args["json"] != true {
		t.Errorf("devices request %q %v", cmd, args)
	}
	out, _ = capture(t, func() { code = cmdDaemon([]string{"status"}) })
	if code != 0 || out != "tb daemon is running: "+daemon.SocketPath()+"\n" {
		t.Errorf("status: code %d out %q", code, out)
	}
	out, _ = capture(t, func() { code = cmdDaemon([]string{"stop"}) })
	if cmd, args, _ := f.last(t); code != 0 || cmd != "stop" || args != nil {
		t.Errorf("stop: code %d cmd %q args %v out %q", code, cmd, args, out)
	}
}

func TestMCPCallTools(t *testing.T) {
	f := startFakeDaemon(t)
	cases := []struct {
		tool    string
		args    map[string]any
		wantCmd string
		want    map[string]any
	}{
		{"devices", nil, "devices", nil},
		{"open", map[string]any{"device": "Pixel", "name": "alice", "app": "com.app", "platform": "android"}, "open",
			map[string]any{"device": "Pixel", "name": "alice", "app": "com.app", "platform": "android"}},
		{"open", map[string]any{"name": "browser", "app": "http://localhost:7360/", "platform": "web", "headless": true}, "open",
			map[string]any{"device": "", "name": "browser", "app": "http://localhost:7360/", "platform": "web", "headless": true}},
		{"look", map[string]any{"device": "alice", "all": true}, "look", map[string]any{"device": "alice", "all": true, "shot": false, "json": false}},
		{"run", map[string]any{"device": "alice", "commands": "tapOn: Login"}, "do",
			map[string]any{"device": "alice", "commands": "- tapOn: Login\n"}},
		{"run", map[string]any{"commands": "- back\n- tapOn: x"}, "do", map[string]any{"device": "", "commands": "- back\n- tapOn: x\n"}},
		{"wait", map[string]any{"text": "Done", "gone": true, "timeout_ms": float64(250)}, "do",
			map[string]any{"device": "", "commands": "- extendedWaitUntil:\n    notVisible: \"Done\"\n    timeout: 250\n"}},
		{"wait", map[string]any{"text": "Hi"}, "do",
			map[string]any{"device": "", "commands": "- extendedWaitUntil:\n    visible: \"Hi\"\n    timeout: 17000\n"}},
		{"save_flow", map[string]any{"path": "f.yaml", "device": "bob"}, "save", map[string]any{"device": "bob", "path": "f.yaml"}},
		{"app_log", map[string]any{"device": "bob", "since": float64(30), "grep": "Net"}, "applog",
			map[string]any{"device": "bob", "since": float64(30), "grep": "Net"}},
		{"close", map[string]any{"device": "bob"}, "close", map[string]any{"device": "bob"}},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			r := mcpCall(context.Background(), c.tool, c.args)
			if r["isError"] != false {
				t.Errorf("isError = %v", r["isError"])
			}
			content := r["content"].([]map[string]any)
			if content[0]["text"] != c.wantCmd+" done\n" {
				t.Errorf("text = %q", content[0]["text"])
			}
			cmd, args, _ := f.last(t)
			if cmd != c.wantCmd {
				t.Errorf("cmd = %q, want %q", cmd, c.wantCmd)
			}
			if len(args) != len(c.want) {
				t.Errorf("args = %v, want %v", args, c.want)
			}
			for k, v := range c.want {
				if args[k] != v {
					t.Errorf("args[%s] = %#v, want %#v", k, args[k], v)
				}
			}
		})
	}
}

func TestMCPRunStructuredFailure(t *testing.T) {
	f := startFakeDaemon(t)
	report := json.RawMessage(`{"exit":1,"steps":[{"status":"failed"}]}`)
	f.answer("do", daemon.Response{Exit: 1, Text: "failed\n", JSON: report})
	result := mcpCall(context.Background(), "run", map[string]any{"commands": "tapOn: Missing", "json": true})
	if structured, _ := result["structuredContent"].(json.RawMessage); string(structured) != string(report) ||
		result["isError"] != true || result["content"].([]map[string]any)[0]["text"] != string(report) {
		t.Fatalf("tool result: %v", result)
	}
	if _, args, _ := f.last(t); args["json"] != true {
		t.Errorf("missing json option: %v", args)
	}
}

func TestMCPLocateStructuredResult(t *testing.T) {
	f := startFakeDaemon(t)
	report := json.RawMessage(`{"count":0,"candidates":[]}`)
	f.answer("locate", daemon.Response{OK: true, JSON: report})
	result := mcpCall(context.Background(), "locate", map[string]any{"selector": "Missing"})
	if structured, _ := result["structuredContent"].(json.RawMessage); string(structured) != string(report) ||
		result["isError"] != false || result["content"].([]map[string]any)[0]["text"] != string(report) {
		t.Fatalf("tool result: %v", result)
	}
}

func TestMCPLookJSON(t *testing.T) {
	f := startFakeDaemon(t)
	f.answer("look", daemon.Response{OK: true, Text: "android 1080x2400, 0 elements\n", JSON: []byte(`{"blocks":[]}`)})
	r := mcpCall(context.Background(), "look", map[string]any{"json": true})
	if text := r["content"].([]map[string]any)[0]["text"]; text != `{"blocks":[]}` {
		t.Errorf("text = %q", text)
	}
	if _, args, _ := f.last(t); args["json"] != true {
		t.Errorf("json not passed: %v", args)
	}
}

func TestMCPCallStreamAndError(t *testing.T) {
	f := startFakeDaemon(t)
	f.answer("do", daemon.Response{OK: true, Exit: 1, Text: "FAILED\n"}, "ok launchApp\n")
	r := mcpCall(context.Background(), "run", map[string]any{"commands": "launchApp"})
	content := r["content"].([]map[string]any)
	if content[0]["text"] != "ok launchApp\nFAILED\n" || r["isError"] != true {
		t.Errorf("result = %v", r)
	}
}

func TestMCPCallLookScreenshot(t *testing.T) {
	f := startFakeDaemon(t)
	png := filepath.Join(t.TempDir(), "shot.png")
	os.WriteFile(png, []byte("\x89PNG fake"), 0o600)
	f.answer("look", daemon.Response{OK: true, Text: "android 1080x2400\n@1 Button \"Go\"\nscreenshot: " + png + "\n"})
	r := mcpCall(context.Background(), "look", map[string]any{"screenshot": true})
	_, args, _ := f.last(t)
	if args["shot"] != true {
		t.Errorf("shot arg = %v", args["shot"])
	}
	content := r["content"].([]map[string]any)
	if len(content) != 2 || content[1]["type"] != "image" || content[1]["mimeType"] != "image/png" ||
		content[1]["data"] != base64.StdEncoding.EncodeToString([]byte("\x89PNG fake")) {
		t.Errorf("content = %v", content)
	}
	if !strings.Contains(content[0]["text"].(string), `@1 Button "Go"`) {
		t.Errorf("text = %v", content[0]["text"])
	}
	f.answer("look", daemon.Response{OK: true, Text: "android 1x1\n"})
	if c := mcpCall(context.Background(), "look", map[string]any{"screenshot": true})["content"].([]map[string]any); len(c) != 1 {
		t.Errorf("content without a screenshot = %v", c)
	}
}

func TestMCPCallLookJSONScreenshot(t *testing.T) {
	f := startFakeDaemon(t)
	png := filepath.Join(t.TempDir(), "shot.png")
	os.WriteFile(png, []byte("\x89PNG fake"), 0o600)
	js := `{"elements":[],"shot":"` + png + `"}`
	f.answer("look", daemon.Response{OK: true, Text: "screenshot: " + png + "\n", JSON: []byte(js)})
	r := mcpCall(context.Background(), "look", map[string]any{"screenshot": true, "json": true})
	content := r["content"].([]map[string]any)
	if len(content) != 2 || content[0]["text"] != js || content[1]["type"] != "image" ||
		content[1]["data"] != base64.StdEncoding.EncodeToString([]byte("\x89PNG fake")) {
		t.Errorf("content = %v", content)
	}
}

func TestMCPCancelledCallStopsInDaemon(t *testing.T) {
	f := startFakeDaemon(t)
	started := make(chan struct{})
	cancelled := make(chan struct{})
	release := make(chan struct{})
	f.mu.Lock()
	f.hook = func(ctx context.Context, cmd string) {
		if cmd != "do" {
			return
		}
		close(started)
		select {
		case <-ctx.Done():
			close(cancelled)
		case <-release:
		}
	}
	f.mu.Unlock()

	m := startMCP(t)
	m.send(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"wait","arguments":{"text":"Never"}}}`)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the tool call did not reach the daemon")
	}
	m.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":7,"reason":"user cancelled"}}`)
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Error("the daemon command kept running after notifications/cancelled")
		close(release)
	}
	m.send(`{"jsonrpc":"2.0","id":8,"method":"ping"}`)
	if ids, out := m.finish(); len(ids) != 1 || ids[0] != "8" {
		t.Errorf("replies to ids %v, want only the ping (8): a cancelled request gets no response\n%s", ids, out)
	}
}

func TestMCPCancelledQueuedCallNeverRuns(t *testing.T) {
	f := startFakeDaemon(t)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	f.mu.Lock()
	f.hook = func(ctx context.Context, cmd string) {
		started <- struct{}{}
		<-release
	}
	f.mu.Unlock()
	cancelRead := make(chan struct{})
	mcpRead = func(method string) {
		if method == "notifications/cancelled" {
			close(cancelRead)
		}
	}
	t.Cleanup(func() { mcpRead = nil })

	m := startMCP(t)
	m.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"devices","arguments":{}}}`)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the first tool call did not reach the daemon")
	}
	m.send(`{"jsonrpc":"2.0","id":"two","method":"tools/call","params":{"name":"close","arguments":{}}}`)
	m.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"two"}}`)
	select {
	case <-cancelRead:
	case <-time.After(5 * time.Second):
		t.Fatal("tb mcp did not read the cancel")
	}
	close(release)
	if ids, out := m.finish(); len(ids) != 1 || ids[0] != "1" {
		t.Errorf("replies to ids %v, want only 1\n%s", ids, out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) != 1 || f.reqs[0].Cmd != "devices" {
		t.Errorf("the daemon got %v, want only devices", f.reqs)
	}
}

func TestMCPPingDuringCall(t *testing.T) {
	f := startFakeDaemon(t)
	started := make(chan struct{})
	release := make(chan struct{})
	f.mu.Lock()
	f.hook = func(ctx context.Context, cmd string) {
		close(started)
		<-release
	}
	f.mu.Unlock()

	m := startMCP(t)
	m.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"devices","arguments":{}}}`)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the tool call did not reach the daemon")
	}
	m.send(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	select {
	case id := <-m.replies:
		if id != "2" {
			t.Errorf("first reply to id %s, want the ping (2)", id)
		}
	case <-time.After(5 * time.Second):
		t.Error("the ping was not answered while the tool call ran")
	}
	close(release)
	if ids, out := m.finish(); len(ids) != 2 || ids[0] != "2" || ids[1] != "1" {
		t.Errorf("replies to ids %v, want 2 then 1\n%s", ids, out)
	}
}

type mcpPipe struct {
	t       *testing.T
	in      *os.File
	replies chan string
	done    chan struct{}
	code    int
	mu      sync.Mutex
	out     strings.Builder
	ids     []string
}

func startMCP(t *testing.T) *mcpPipe {
	t.Helper()
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW
	t.Cleanup(func() { inR.Close(); inW.Close(); outR.Close() })
	m := &mcpPipe{t: t, in: inW, replies: make(chan string, 100), done: make(chan struct{})}
	read := make(chan struct{})
	go func() {
		defer close(read)
		sc := bufio.NewScanner(outR)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			var r map[string]json.RawMessage
			id := "?"
			if json.Unmarshal(sc.Bytes(), &r) == nil {
				id = string(r["id"])
			}
			m.mu.Lock()
			m.out.WriteString(sc.Text() + "\n")
			m.ids = append(m.ids, id)
			m.mu.Unlock()
			m.replies <- id
		}
	}()
	go func() {
		defer close(m.done)
		m.code = cmdMCP(nil)
		os.Stdin, os.Stdout = oldIn, oldOut
		outW.Close()
		<-read
	}()
	return m
}

func (m *mcpPipe) send(line string) {
	m.t.Helper()
	if _, err := m.in.Write([]byte(line + "\n")); err != nil {
		m.t.Fatal(err)
	}
}

func (m *mcpPipe) finish() ([]string, string) {
	m.t.Helper()
	m.in.Close()
	select {
	case <-m.done:
	case <-time.After(10 * time.Second):
		m.t.Fatal("tb mcp did not exit at the end of its input")
	}
	if m.code != 0 {
		m.t.Errorf("code = %d", m.code)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ids, m.out.String()
}
