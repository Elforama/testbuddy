package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/flow"
)

// log prints the recorded commands; clear forgets them.
func TestLogAndClear(t *testing.T) {
	s := New()
	f := newFake("e", "E", device.Android)
	addDev(s, f, "")
	if resp, _ := call(t, s.log, nil, ""); resp.Text != "no commands recorded yet\n" {
		t.Errorf("empty log %+v", resp)
	}
	call(t, s.do, map[string]any{"commands": "- inputText: a\n- inputText: b"}, "")
	if resp, _ := call(t, s.log, nil, ""); resp.Text != "- inputText: a\n- inputText: b\n" {
		t.Errorf("log %q", resp.Text)
	}
	if resp, _ := call(t, s.clear, nil, ""); !resp.OK || resp.Text != "recording cleared\n" {
		t.Errorf("clear %+v", resp)
	}
	if resp, _ := call(t, s.log, nil, ""); resp.Text != "no commands recorded yet\n" {
		t.Errorf("log after clear %+v", resp)
	}
	for name, h := range map[string]handler{"log": s.log, "clear": s.clear} {
		if resp, _ := call(t, h, map[string]any{"device": "nobody"}, ""); resp.Exit != 2 {
			t.Errorf("%s without device: %+v", name, resp)
		}
	}
}

// Recordings are per device.
func TestRecordingPerDevice(t *testing.T) {
	s := New()
	addDev(s, newFake("a", "A", device.Android), "alice")
	addDev(s, newFake("b", "B", device.Android), "bob")
	call(t, s.do, map[string]any{"device": "alice", "commands": "inputText: from-alice"}, "")
	call(t, s.do, map[string]any{"device": "bob", "commands": "inputText: from-bob"}, "")
	if resp, _ := call(t, s.log, map[string]any{"device": "alice"}, ""); resp.Text != "- inputText: from-alice\n" {
		t.Errorf("alice log %q", resp.Text)
	}
	if resp, _ := call(t, s.log, map[string]any{"device": "bob"}, ""); resp.Text != "- inputText: from-bob\n" {
		t.Errorf("bob log %q", resp.Text)
	}
}

// save writes the passing commands as a flow that parses back to the same commands.
func TestSaveWritesFlow(t *testing.T) {
	s := New()
	f := newFake("e", "E", device.Android)
	f.show(loginScreen()...)
	addDev(s, f, "")
	call(t, s.look, nil, "")
	call(t, s.do, map[string]any{"commands": "- tapOn: @3\n- inputText: hi\n- tapOn:\n    id: email\n- assertVisible: Missing\n", "app": "com.chat"}, "")
	cwd := t.TempDir()
	resp, _ := call(t, s.save, map[string]any{"path": "flows/sign-in.yaml"}, cwd)
	path := filepath.Join(cwd, "flows", "sign-in.yaml")
	if !resp.OK || resp.Text != "saved 3 commands to "+path+"\n" {
		t.Fatalf("resp %+v", resp)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "appId: com.chat\n---\n- tapOn: \"Sign in\"\n- inputText: hi\n- tapOn:\n    id: email\n"
	if string(b) != want {
		t.Errorf("file =\n%s\nwant\n%s", b, want)
	}
	fl, err := flow.ParseFile(path)
	if err != nil {
		t.Fatalf("saved flow does not parse: %v", err)
	}
	if fl.Config.AppID != "com.chat" || len(fl.Commands) != 3 || fl.Commands[2].Selector.ID != "email" {
		t.Errorf("parsed %+v", fl.Config)
	}
}

func TestSavePreviewDoesNotWriteAndReportsWarnings(t *testing.T) {
	s := New()
	d := addDev(s, newFake("e", "E", device.Android), "")
	d.AppID = "com.example"
	d.steps = []string{"- tapOn: {point: '20%,20%'}", "- tapOn: {text: OK, index: 1}"}
	dir := t.TempDir()
	args := map[string]any{"path": "new/flow.yaml", "preview": true}
	resp, _ := call(t, s.save, args, dir)
	if !resp.OK {
		t.Fatalf("preview: %+v", resp)
	}
	if _, err := os.Stat(filepath.Join(dir, "new")); !os.IsNotExist(err) {
		t.Fatalf("preview created a directory: %v", err)
	}
	var preview struct {
		YAML     string
		Steps    int
		Warnings []recordingWarning
	}
	if err := json.Unmarshal(resp.JSON, &preview); err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, w := range preview.Warnings {
		codes = append(codes, w.Code)
	}
	want := []string{"missing_fresh_launch", "coordinate_tap", "indexed_selector", "missing_assertion"}
	if preview.Steps != 2 || !reflect.DeepEqual(codes, want) ||
		preview.Warnings[1].Step != 1 || preview.Warnings[2].Step != 2 {
		t.Fatalf("preview %+v", preview)
	}
	delete(args, "preview")
	resp, _ = call(t, s.save, args, dir)
	body, err := os.ReadFile(filepath.Join(dir, "new", "flow.yaml"))
	if err != nil || !resp.OK || string(body) != preview.YAML {
		t.Fatalf("save: %v, %s, want %q", err, resp.Text, preview.YAML)
	}
}

// The preview gets tb check --lint's other rules too: text read as an unintended regex,
// and an assertion made optional.
func TestSavePreviewLintRules(t *testing.T) {
	s := New()
	d := addDev(s, newFake("e", "E", device.Android), "")
	d.AppID = "com.example"
	d.steps = []string{"- launchApp", "- tapOn: 'Sign in?'", "- assertVisible: {text: Done, optional: true}"}
	resp, _ := call(t, s.save, map[string]any{"preview": true}, t.TempDir())
	var preview struct{ Warnings []recordingWarning }
	if err := json.Unmarshal(resp.JSON, &preview); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, w := range preview.Warnings {
		got = append(got, fmt.Sprintf("%s@%d", w.Code, w.Step))
	}
	if want := []string{"regex_text@2", "optional_assertion@3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("warnings %v, want %v", got, want)
	}
}

// save's app id: --app, else the device's app, else the foreground app.
func TestSaveAppID(t *testing.T) {
	s := New()
	f := newFake("e", "E", device.Android)
	f.fg = "com.fg"
	d := addDev(s, f, "")
	dir := t.TempDir()
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		first, _, _ := strings.Cut(string(b), "\n")
		return first
	}
	call(t, s.save, map[string]any{"path": filepath.Join(dir, "a.yaml")}, "")
	d.AppID = "com.dev"
	call(t, s.save, map[string]any{"path": filepath.Join(dir, "b.yaml")}, "")
	call(t, s.save, map[string]any{"path": filepath.Join(dir, "c.yaml"), "app": "com.arg"}, "")
	got := []string{read("a.yaml"), read("b.yaml"), read("c.yaml")}
	if !reflect.DeepEqual(got, []string{"appId: com.fg", "appId: com.dev", "appId: com.arg"}) {
		t.Errorf("app ids %q", got)
	}
}

// save fails with exit 1 when the file cannot be written, exit 2 without a device.
func TestSaveErrors(t *testing.T) {
	s := New()
	if resp, _ := call(t, s.save, map[string]any{"path": "x.yaml"}, t.TempDir()); resp.Exit != 2 {
		t.Errorf("no device: %+v", resp)
	}
	addDev(s, newFake("e", "E", device.Android), "")
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	os.WriteFile(file, nil, 0o644)
	if resp, _ := call(t, s.save, map[string]any{"path": filepath.Join(file, "sub", "x.yaml")}, ""); resp.Exit != 1 {
		t.Errorf("folder is a file: %+v", resp)
	}
	os.Mkdir(filepath.Join(dir, "taken.yaml"), 0o755)
	if resp, _ := call(t, s.save, map[string]any{"path": filepath.Join(dir, "taken.yaml")}, ""); resp.Exit != 1 {
		t.Errorf("path is a folder: %+v", resp)
	}
}

// log and clear share the recorded steps with a running do (issue #63): log answers at
// once with what is recorded so far, without waiting for the do to end, and neither
// reads or writes the steps while do appends to them (the race detector reports it).
func TestLogAndClearDuringDo(t *testing.T) {
	s := New()
	f := newFake("e", "E", device.Android)
	addDev(s, f, "")
	inSecond, release := make(chan struct{}), make(chan struct{})
	f.onType = func(text string) {
		if text == "second" {
			close(inSecond)
			<-release
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		call(t, s.do, map[string]any{"commands": "- inputText: first\n- inputText: second\n- inputText: third"}, "")
	}()
	<-inSecond
	// do holds the device and is in its second step: log does not wait for it.
	logged := make(chan string, 1)
	go func() { resp, _ := call(t, s.log, nil, ""); logged <- resp.Text }()
	select {
	case text := <-logged:
		if text != "- inputText: first\n" {
			t.Errorf("log mid-do %q", text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("log waited for the running do")
	}
	// A log and a clear that run while do records its next steps.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); call(t, s.log, nil, "") }()
	go func() { defer wg.Done(); call(t, s.clear, nil, "") }()
	close(release)
	wg.Wait()
	<-done
	// clear is a device command: it waits for the do and forgets all of it.
	if resp, _ := call(t, s.log, nil, ""); resp.Text != "no commands recorded yet\n" {
		t.Errorf("log after clear %q", resp.Text)
	}
}
