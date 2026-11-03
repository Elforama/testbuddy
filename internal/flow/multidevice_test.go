package flow

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// mdHdr declares two roles; commands written after it start on line 5.
const mdHdr = "devices:\n  alice: ios\n  bob: android\n---\n"

// TestDevicesForms pins both role forms (platform string, settings map) and their order.
func TestDevicesForms(t *testing.T) {
	f := mustParse(t, `devices:
  alice: ios
  bob: Android
  carol: web
  dave:
  erin:
    platform: IOS
    device: "iPhone 17"
    appId: com.example.chat
    env:
      USER: erin@example.com
      ROLE: admin
  frank: {}
---
- erin: back
`)
	ds := f.Config.Devices
	var got []string
	for _, d := range ds {
		got = append(got, d.Name+"="+d.Platform)
	}
	if want := []string{"alice=ios", "bob=android", "carol=web", "dave=", "erin=ios", "frank="}; !reflect.DeepEqual(got, want) {
		t.Fatalf("roles in order, platforms lower-cased: %v", got)
	}
	e := ds[4]
	if e.Device != "iPhone 17" || e.AppID != "com.example.chat" || !reflect.DeepEqual(e.Env, []KV{{"USER", "erin@example.com"}, {"ROLE", "admin"}}) {
		t.Errorf("erin: %+v", e)
	}
	if f.Config.AppID != "" {
		t.Errorf("a multi-device flow needs no top-level appId: %q", f.Config.AppID)
	}
}

// TestDevicesWithAppID pins that a top-level appId is kept next to devices.
func TestDevicesWithAppID(t *testing.T) {
	c := mustParse(t, "appId: com.shared\ndevices: {alice: ios}\n---\n- alice: launchApp\n").Config
	if c.AppID != "com.shared" || len(c.Devices) != 1 {
		t.Errorf("%+v", c)
	}
}

// TestDevicesErrors pins rejected device declarations.
func TestDevicesErrors(t *testing.T) {
	runErrCases(t, "", []errCase{
		{"scalar", "devices: ios\n---\n- back\n", "devices must map role names to device settings", 1},
		{"list", "devices: [alice, bob]\n---\n- back\n", "devices must map role names", 1},
		{"null", "devices:\n---\n- back\n", "devices must map role names", 1},
		{"unknown setting", "devices:\n  alice:\n    platform: ios\n    os: 17\n---\n- back\n", `unknown device setting "os" (use platform, device, appId, env)`, 4},
		{"bad platform string", "devices:\n  alice: windows\n---\n- back\n", `device "alice": platform must be android, ios or web`, 2},
		{"bad platform in map", "devices:\n  alice: {platform: tv}\n---\n- back\n", `device "alice": platform must be android, ios or web`, 2},
		{"list spec", "devices:\n  alice: [ios]\n---\n- back\n", `device "alice": expected a platform name or a map`, 2},
		{"bad env", "devices:\n  alice: {env: [A]}\n---\n- back\n", "env must be a map", 2},
		{"env without value", "devices:\n  alice:\n    env:\n      USER:\n---\n- back\n", "Environment variable USER has no value", 4},
	})
}

// TestRoleNamedLikeACommand pins that a role cannot shadow a command (or an extension keyword).
func TestRoleNamedLikeACommand(t *testing.T) {
	for _, name := range []string{"tapOn", "launchApp", "back", "assertVisible", "action", "runFlow", "parallel", "signal", "waitForSignal"} {
		t.Run(name, func(t *testing.T) {
			e := parseErr(t, "devices:\n  alice: ios\n  "+name+": android\n---\n- back\n")
			if !strings.Contains(e.Msg, `device role "`+name+`" has the name of a command`) || e.Line != 3 {
				t.Errorf("%v", e)
			}
		})
	}
	// Close-but-different names are fine.
	if f := mustParse(t, "devices:\n  tap: ios\n  on: android\n---\n- tap: back\n- on: back\n"); len(f.Commands) != 2 {
		t.Errorf("%v", names(f.Commands))
	}
}

// TestRoleBlocks pins `- role: ...` as a list, a single command map, or a bare command.
func TestRoleBlocks(t *testing.T) {
	f := mustParse(t, mdHdr+`- alice:
    - launchApp: {clearState: true}
    - tapOn: "New message"
    - inputText: ${MESSAGE}
- bob: {assertVisible: Hi}
- alice: back
- bob: []
`)
	if len(f.Commands) != 4 {
		t.Fatalf("%v", names(f.Commands))
	}
	a := f.Commands[0]
	if a.Name != "on" || a.On != "alice" || a.Line != 5 || a.File != f.Path || !reflect.DeepEqual(names(a.Commands), []string{"launchApp", "tapOn", "inputText"}) {
		t.Errorf("list block: %+v", a)
	}
	if a.Commands[1].Line != 7 {
		t.Errorf("inner commands keep their lines: %d", a.Commands[1].Line)
	}
	if b := f.Commands[1]; b.On != "bob" || len(b.Commands) != 1 || b.Commands[0].Selector.Text != "Hi" {
		t.Errorf("single map: %+v", b)
	}
	if c := f.Commands[2]; c.On != "alice" || c.Commands[0].Name != "back" {
		t.Errorf("bare command: %+v", c)
	}
	if d := f.Commands[3]; d.On != "bob" || len(d.Commands) != 0 {
		t.Errorf("empty block: %+v", d)
	}
}

// TestRoleBlockErrors pins errors in role blocks, reported at the inner command.
func TestRoleBlockErrors(t *testing.T) {
	runErrCases(t, mdHdr, []errCase{
		{"null block", "- alice:\n", `Invalid Command: ""`, 5},
		{"bad inner command", "- alice:\n    - back\n    - tapOnn: X\n", "Did you mean tapOn", 7},
		{"bad single map", "- bob: {tapOn: {txt: X}}\n", `"txt" is not an option of tapOn`, 5},
		{"bare command needing a value", "- bob: tapOn\n", "Missing Command Options", 5},
		{"undeclared role", "- carol: [back]\n", `Invalid Command: "carol"`, 5},
		{"nested list", "- alice: [[back]]\n", "each command is a string or a single-key map", 5},
	})
	// Role blocks only exist in flows that declare devices.
	e := parseErr(t, hdr+"- alice: [back]\n")
	if !strings.Contains(e.Msg, `Invalid Command: "alice"`) {
		t.Errorf("single-device flow: %v", e)
	}
}

// TestParallel pins parallel blocks: per-role command lists, kept in written order.
func TestParallel(t *testing.T) {
	c := mustParse(t, mdHdr+`- parallel:
    bob:
      - launchApp
      - waitForSignal: ready
    alice:
      - launchApp: {clearState: true}
      - signal: ready
`).Commands[0]
	if c.Name != "parallel" || !reflect.DeepEqual(c.ParallelOrder, []string{"bob", "alice"}) {
		t.Fatalf("%+v", c)
	}
	if !reflect.DeepEqual(names(c.Parallel["bob"]), []string{"launchApp", "waitForSignal"}) || c.Parallel["alice"][1].Signal != "ready" {
		t.Errorf("blocks: %+v", c.Parallel)
	}
	if !c.Mutating() {
		t.Error("parallel blocks drive devices")
	}
	c = mustParse(t, mdHdr+"- parallel:\n    alice: {tapOn: X}\n    bob: back\n").Commands[0]
	if c.Parallel["alice"][0].Selector.Text != "X" || c.Parallel["bob"][0].Name != "back" {
		t.Errorf("single-command blocks: %+v", c.Parallel)
	}
}

// TestParallelErrors pins parallel's rejections.
func TestParallelErrors(t *testing.T) {
	runErrCases(t, mdHdr, []errCase{
		{"unknown role", "- parallel:\n    alice: [back]\n    zed: [back]\n", `unknown device role "zed"`, 7},
		{"scalar", "- parallel: alice\n", "parallel maps device roles to their commands", 5},
		{"list", "- parallel: [alice]\n", "parallel maps device roles to their commands", 5},
		{"bad inner command", "- parallel:\n    alice:\n      - frob\n", "Invalid Command", 7},
	})
	e := parseErr(t, hdr+"- parallel:\n    alice: [back]\n")
	if !strings.Contains(e.Msg, "parallel is only for multi-device flows (declare devices: in the config)") || e.Line != 3 {
		t.Errorf("single-device flow: %v", e)
	}
}

// TestSignals pins signal and waitForSignal forms.
func TestSignals(t *testing.T) {
	cs := mustParse(t, mdHdr+`- signal: code-ready
- waitForSignal: code-ready
- waitForSignal:
    name: other
    timeout: 30000
    label: wait for bob
    optional: true
`).Commands
	if cs[0].Signal != "code-ready" || cs[1].Signal != "code-ready" || cs[1].Timeout != "" {
		t.Errorf("scalar forms: %+v %+v", cs[0], cs[1])
	}
	if c := cs[2]; c.Signal != "other" || c.Timeout != "30000" || c.Label != "wait for bob" || !c.Optional {
		t.Errorf("map form: %+v", c)
	}
	for _, c := range cs {
		if c.Mutating() {
			t.Errorf("%s does not touch a device", c.Name)
		}
	}
	// Signals are ordinary commands: single-device flows may use them too.
	if c := step(t, "- signal: done\n"); c.Signal != "done" {
		t.Errorf("single-device signal: %+v", c)
	}
	runErrCases(t, mdHdr, []errCase{
		{"signal map", "- signal: {name: x}\n", "signal takes a name", 5},
		{"signal list", "- signal: [x]\n", "signal takes a name", 5},
		{"waitForSignal without name", "- waitForSignal: {timeout: 5}\n", "waitForSignal needs a signal name", 5},
		{"waitForSignal empty", "- waitForSignal: \"\"\n", "waitForSignal needs a signal name", 5},
		{"waitForSignal unknown key", "- waitForSignal: {name: x, after: 3}\n", `"after" is not an option of waitForSignal`, 5},
		{"waitForSignal bad optional", "- waitForSignal: {name: x, optional: z}\n", "expected true or false", 5},
	})
}

// TestCommandsOutsideRoles pins that plain commands parse in a multi-device flow; the
// run-time rule (which needs a role) is enforced by the orchestrator, not the parser.
func TestCommandsOutsideRoles(t *testing.T) {
	cs := mustParse(t, mdHdr+"- evalScript: ${output.x = 1}\n- tapOn: X\n- alice: back\n").Commands
	if cs[0].On != "" || cs[1].On != "" || cs[1].Name != "tapOn" || cs[2].On != "alice" {
		t.Errorf("%+v", cs)
	}
}

// TestMultiDeviceSubFlows pins that sub-flows are ordinary flows: signals work, roles do not.
func TestMultiDeviceSubFlows(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"login.yaml":    hdr + "- tapOn: Login\n- signal: logged-in\n- waitForSignal: {name: go, timeout: 1000}\n",
		"parallel.yaml": hdr + "- parallel:\n    alice: [back]\n",
		"role.yaml":     hdr + "- alice: [back]\n",
	})
	main := filepath.Join(dir, "main.yaml")
	f, err := ParseBytes(main, []byte(mdHdr+"- parallel:\n    alice:\n      - runFlow: login.yaml\n    bob:\n      - runFlow: login.yaml\n"))
	if err != nil {
		t.Fatal(err)
	}
	sub := f.Commands[0].Parallel["alice"][0].SubFlow
	if sub == nil || sub.Commands[1].Signal != "logged-in" || sub.Commands[2].Signal != "go" {
		t.Errorf("signals in a sub-flow: %+v", sub)
	}
	for _, tc := range []struct{ file, want string }{
		{"parallel.yaml", "parallel is only for multi-device flows"},
		{"role.yaml", `Invalid Command: "alice"`},
	} {
		_, err := ParseBytes(main, []byte(mdHdr+"- alice:\n    - runFlow: "+tc.file+"\n"))
		if e := asFlowErr(t, err); !strings.Contains(e.Msg, tc.want) || e.File != filepath.Join(dir, tc.file) {
			t.Errorf("%s: %v", tc.file, e)
		}
	}
}

// TestMultiDeviceDocExample parses the example from docs/multi-device.md.
func TestMultiDeviceDocExample(t *testing.T) {
	dir := writeFiles(t, map[string]string{"login.yaml": "appId: com.example.chat\n---\n- tapOn: Login\n"})
	f, err := ParseBytes(filepath.Join(dir, "chat.yaml"), []byte(`name: Alice messages Bob
devices:                        # this key makes it a multi-device flow
  alice:
    platform: ios
    appId: com.example.chat
    env: { USER: alice@example.com }
  bob:
    platform: android
    device: "Pixel 7"
    appId: com.example.chat
    env: { USER: bob@example.com }
env:
  MESSAGE: "Hi Bob"
---
- parallel:
    alice:
      - launchApp: { clearState: true }
      - runFlow: login.yaml
    bob:
      - launchApp: { clearState: true }
      - runFlow: login.yaml
- alice:
    - tapOn: "New message"
    - inputText: ${MESSAGE}
    - tapOn: "Send"
- bob:
    - assertVisible: ${MESSAGE}
- parallel:
    alice:
      - copyTextFrom: { id: invite_code }
      - evalScript: ${output.code = tb.copiedText}
      - signal: code-ready
    bob:
      - waitForSignal: code-ready
      - inputText: ${output.code}
`))
	if err != nil {
		t.Fatal(err)
	}
	if f.Name() != "Alice messages Bob" || len(f.Config.Devices) != 2 || f.Config.Devices[1].Device != "Pixel 7" {
		t.Errorf("config: %+v", f.Config)
	}
	if got := names(f.Commands); !reflect.DeepEqual(got, []string{"parallel", "on", "on", "parallel"}) {
		t.Errorf("steps: %v", got)
	}
	if f.Commands[0].Parallel["bob"][1].SubFlow == nil || f.Commands[3].Parallel["bob"][1].Text != "${output.code}" {
		t.Errorf("blocks: %+v", f.Commands[0].Parallel)
	}
}
