// tb is Test Buddy: a fast, event-driven mobile UI test runner that runs Maestro-compatible YAML flows,
// drives several devices at once, and is built for AI agents to use from a shell.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/look"
	"github.com/elforama/testbuddy/internal/session"
)

const version = "0.5.2"

const usage = `tb: fast UI tests for Android, iOS and the web (runs Maestro-compatible YAML flows)

Run tests:
  tb test <flows or folders> [options]   run flows (tb test -h for options)
  tb check <flows...>                    check flow syntax without a device
  tb a11y [paths] [--changed]            check app code for UI tb cannot detect or tap
                                         (tb a11y -h for options; runs in CI too)

Drive a device (for AI agents and people; the device stays connected between calls):
  tb devices                             list devices
  tb open [-d DEVICE] [--name alice] [--app ID]
                                         connect to a device, optionally naming it
  tb open -p web --headless --app URL    open a headless Chrome session
  tb look [-d alice] [--shot] [--all] [--json]
                                         what is on screen: one line per element, with @refs,
                                         indented where layout matters (rows, lists),
                                         what scroll areas hide, * on new elements
  tb do [-d alice] [--json] '<command>' ...  run flow commands and report the outcome
  tb do - < steps.yaml                   commands from stdin
  tb wait [-d alice] <text> [--gone] [--timeout ms]
                                         wait (event-driven) until text appears or goes
  tb locate [-d alice] [--json] <selector>
                                         inspect matches and the runner's pick on screen
  tb watch [-d alice] [--until text] [--timeout ms]
                                         stream screen changes as they happen
  tb shot [-d alice] [path] [--scale 0.5]
                                         save a screenshot
  tb save <file.yaml> [-d alice]         save the commands that passed as a flow
  tb save --preview [-d alice] [--json] preview the recorded YAML and replay warnings
  tb replay <file.yaml> -d DEVICE       verify a saved flow from its launchApp step
  tb log | tb clear [-d alice]           show or reset the recorded commands
  tb close [-d alice | all]              disconnect a device (and stop tb's agent on it)
  tb release [-d DEVICE | all]           free devices for other test tools: stop tb's agents
  tb hierarchy [-d DEVICE] [--json]      the screen as an outline (--json: every raw node)
  tb mcp                                 serve the same as MCP tools over stdio
  tb agent-guide                         portable CLI instructions for coding agents
  tb daemon stop                         stop the background process

-d takes a session name, serial/UDID or device name. With one device, -d is optional.
Exit status: 0 ok, 1 a command or flow failed, 2 bad input.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	cmd, rest := os.Args[1], os.Args[2:]
	var code int
	switch cmd {
	case "test":
		code = cmdTest(rest)
	case "check", "check-syntax":
		code = cmdCheck(rest)
	case "a11y", "accessibility":
		code = cmdA11y(rest)
	case "devices", "list-devices":
		code = cmdDevices(rest)
	case "open", "session":
		if cmd == "session" && len(rest) > 0 {
			switch rest[0] {
			case "start":
				rest = rest[1:]
			case "stop":
				code = agentCall("close", rest[1:], nil)
				os.Exit(code)
			case "save":
				code = cmdSave(rest[1:])
				os.Exit(code)
			case "log":
				code = agentCall("log", rest[1:], nil)
				os.Exit(code)
			}
		}
		code = agentCall("open", rest, []flagSpec{{[]string{"name", "n"}, true}, {[]string{"app", "a"}, true},
			{[]string{"headless"}, false}})
	case "close":
		code = agentCall("close", rest, nil)
	case "release":
		code = cmdRelease(rest)
	case "look":
		code = agentCall("look", rest, []flagSpec{{[]string{"shot"}, false}, {[]string{"all"}, false},
			{[]string{"json"}, false}, {[]string{"scale"}, true}, {[]string{"wait"}, false}})
	case "do", "run":
		code = cmdDo(rest)
	case "wait":
		code = cmdWait(rest)
	case "locate":
		code = agentCall("locate", rest, []flagSpec{{[]string{"json"}, false}})
	case "watch":
		code = agentCall("watch", rest, []flagSpec{{[]string{"until"}, true}, {[]string{"timeout"}, true}})
	case "shot", "screenshot":
		code = agentCall("shot", rest, []flagSpec{{[]string{"scale"}, true}})
	case "save":
		code = cmdSave(rest)
	case "replay":
		code = cmdReplay(rest)
	case "log":
		code = agentCall("log", rest, nil)
	case "clear":
		code = agentCall("clear", rest, nil)
	case "hierarchy":
		code = cmdHierarchy(rest)
	case "mcp":
		code = cmdMCP(rest)
	case "daemon":
		code = cmdDaemon(rest)
	case "agent-guide", "guide":
		code = cmdGuide(rest)
	case "version", "--version", "-v":
		fmt.Println("tb", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		code = 2
	}
	os.Exit(code)
}

func cmdCheck(argv []string) int {
	bad := 0
	for _, f := range checkFiles(argv) {
		if fl, err := flow.ParseFile(f); err != nil {
			fmt.Println(err)
			bad++
		} else {
			fmt.Println("ok", f)
			for _, note := range fl.Config.Ignored() {
				fmt.Println("  note:", note)
			}
		}
	}
	if bad > 0 {
		return 1
	}
	return 0
}

// checkFiles expands folders to the flow files in them, sub-folders included: every .yaml or
// .yml file but a workspace's config.yaml. A folder that cannot be read stays as given, so
// its error shows.
func checkFiles(argv []string) []string {
	var files []string
	for _, a := range argv {
		if st, err := os.Stat(a); err != nil || !st.IsDir() {
			files = append(files, a)
			continue
		}
		before := len(files)
		err := filepath.WalkDir(a, func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return err
			}
			ext, base := filepath.Ext(p), filepath.Base(p)
			if (ext == ".yaml" || ext == ".yml") && strings.TrimSuffix(base, ext) != "config" {
				files = append(files, p)
			}
			return nil
		})
		if err != nil && len(files) == before {
			files = append(files, a)
		}
	}
	return files
}

// listDevices finds the connected devices (a variable so tests can give their own).
var listDevices = device.List

func cmdDevices(argv []string) int {
	a, err := parseArgs(argv, flagSpec{[]string{"json"}, false}, flagSpec{[]string{"platform", "p"}, true})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	// Ask the daemon if it runs (it knows sessions); else list directly.
	if daemon.Running() {
		return agentCall("devices", argv, []flagSpec{{[]string{"json"}, false}})
	}
	devs := listDevices(context.Background())
	if a.flag("json") {
		b, _ := json.MarshalIndent(devs, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	if len(devs) == 0 {
		fmt.Println("no devices: start an Android emulator or boot an iOS simulator")
		return 0
	}
	for _, d := range devs {
		fmt.Println(d)
	}
	return 0
}

// agentCall sends a device command to the daemon. Common flags: -d/--device, -p/--platform.
func agentCall(cmd string, argv []string, extra []flagSpec) int {
	specs := append([]flagSpec{{[]string{"device", "d"}, true}, {[]string{"platform", "p"}, true}}, extra...)
	a, err := parseArgs(argv, specs...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	args := map[string]any{"device": a.str("device"), "platform": a.str("platform")}
	for _, s := range extra {
		name := s.names[0]
		if s.value {
			if v := a.str(name); v != "" {
				switch name {
				case "scale":
					var f float64
					fmt.Sscanf(v, "%g", &f)
					args[name] = f
				case "timeout":
					var n int
					fmt.Sscanf(v, "%d", &n)
					args[name] = n
				default:
					args[name] = v
				}
			}
		} else if a.flag(name) {
			args[name] = true
		}
	}
	switch cmd {
	case "locate":
		if len(a.pos) == 0 {
			fmt.Fprintln(os.Stderr, "usage: tb locate <text, @ref, or {selector}> [--json]")
			return 2
		}
		args["selector"] = strings.Join(a.pos, " ")
	case "shot":
		if len(a.pos) > 0 {
			args["path"] = a.pos[0]
		}
	case "close":
		if len(a.pos) > 0 && a.pos[0] == "all" {
			args["device"] = "all"
		}
	case "open":
		if len(a.pos) > 0 && args["device"] == "" {
			args["device"] = a.pos[0]
		}
	}
	return callDaemon(cmd, args)
}

func callDaemon(cmd string, args map[string]any) int {
	c, err := daemon.Connect()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer c.Close()
	raw, _ := json.Marshal(args)
	cwd, _ := os.Getwd()
	resp, err := c.Call(daemon.Request{Cmd: cmd, Args: raw, Cwd: cwd}, func(part daemon.Response) {
		fmt.Print(part.Text)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if wantJSON, _ := args["json"].(bool); wantJSON && len(resp.JSON) > 0 {
		os.Stdout.Write(resp.JSON)
		fmt.Println()
		return resp.Exit
	}
	if resp.Exit != 0 && !resp.OK {
		fmt.Fprint(os.Stderr, resp.Text)
		if !strings.HasSuffix(resp.Text, "\n") {
			fmt.Fprintln(os.Stderr)
		}
	} else {
		fmt.Print(resp.Text)
	}
	return resp.Exit
}

func cmdDo(argv []string) int {
	a, err := parseArgs(argv, flagSpec{[]string{"device", "d"}, true}, flagSpec{[]string{"platform", "p"}, true},
		flagSpec{[]string{"app", "a"}, true}, flagSpec{[]string{"env", "e"}, true}, flagSpec{[]string{"json"}, false})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	var parts []string
	for _, p := range a.pos {
		if p == "-" {
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 2
			}
			parts = append(parts, string(b))
			continue
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		fmt.Fprintln(os.Stderr, "usage: tb do '<command>' ...   e.g. tb do 'tapOn: Login' 'inputText: hi'")
		return 2
	}
	yaml := toCommandList(parts)
	env := map[string]string{}
	for _, kv := range a.vals["env"] {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	req := map[string]any{"device": a.str("device"), "platform": a.str("platform"),
		"commands": yaml, "app": a.str("app"), "env": env}
	if a.flag("json") {
		req["json"] = true
	}
	return callDaemon("do", req)
}

// toCommandList turns `tb do` arguments into one YAML command list. Each argument is a
// command (`tapOn: Login`, `back`) or already a list.
func toCommandList(parts []string) string {
	var b strings.Builder
	for _, p := range parts {
		p = strings.TrimRight(p, "\n")
		if strings.HasPrefix(strings.TrimSpace(p), "- ") || strings.Contains(p, "\n- ") {
			b.WriteString(p + "\n")
			continue
		}
		lines := strings.Split(p, "\n")
		b.WriteString("- " + lines[0] + "\n")
		for _, l := range lines[1:] {
			b.WriteString("  " + l + "\n")
		}
	}
	return b.String()
}

func cmdWait(argv []string) int {
	a, err := parseArgs(argv, flagSpec{[]string{"device", "d"}, true}, flagSpec{[]string{"platform", "p"}, true},
		flagSpec{[]string{"gone"}, false}, flagSpec{[]string{"timeout", "t"}, true})
	if err != nil || len(a.pos) == 0 {
		fmt.Fprintln(os.Stderr, "usage: tb wait <text or @ref or selector> [--gone] [--timeout ms]")
		return 2
	}
	target := strings.Join(a.pos, " ")
	sel := target
	if !strings.HasPrefix(target, "@") && !strings.HasPrefix(target, "{") {
		b, _ := json.Marshal(target)
		sel = string(b)
	}
	key := "visible"
	if a.flag("gone") {
		key = "notVisible"
	}
	timeout := a.num("timeout", 17000)
	yaml := fmt.Sprintf("- extendedWaitUntil:\n    %s: %s\n    timeout: %d\n", key, sel, timeout)
	return callDaemon("do", map[string]any{"device": a.str("device"), "platform": a.str("platform"), "commands": yaml})
}

func cmdSave(argv []string) int {
	const saveUsage = "usage: tb save <file.yaml> [-d device] [--app id]\n       tb save --preview [file.yaml] [-d device] [--app id] [--json]"
	a, err := parseArgs(argv, flagSpec{[]string{"device", "d"}, true}, flagSpec{[]string{"app", "a"}, true},
		flagSpec{[]string{"preview"}, false}, flagSpec{[]string{"json"}, false}, flagSpec{[]string{"help", "h"}, false})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, saveUsage)
		return 2
	}
	if a.flag("help") {
		fmt.Println(saveUsage)
		return 0
	}
	preview := a.flag("preview")
	if len(a.pos) > 1 || (!preview && (len(a.pos) != 1 || a.flag("json"))) {
		fmt.Fprintln(os.Stderr, saveUsage)
		return 2
	}
	req := map[string]any{"device": a.str("device"), "app": a.str("app")}
	if len(a.pos) == 1 {
		req["path"] = a.pos[0]
	}
	if preview {
		req["preview"] = true
		req["json"] = a.flag("json")
	}
	return callDaemon("save", req)
}

func cmdHierarchy(argv []string) int {
	a, err := parseArgs(argv, flagSpec{[]string{"device", "d"}, true}, flagSpec{[]string{"platform", "p"}, true},
		flagSpec{[]string{"json"}, false})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	ctx := context.Background()
	info, err := device.Pick(listDevices(ctx), device.Platform(a.str("platform")), a.str("device"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	drv, err := openDriver(ctx, info)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer drv.Close()
	s, err := drv.Screen(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !a.flag("json") {
		fmt.Print(look.Build(s, look.NewRefs()).Text())
		return 0
	}
	b, _ := json.MarshalIndent(hierarchyJSON(s), "", "  ")
	fmt.Println(string(b))
	return 0
}

func cmdDaemon(argv []string) int {
	if len(argv) > 0 {
		switch argv[0] {
		case "stop":
			if !daemon.Running() {
				fmt.Println("tb daemon is not running")
				return 0
			}
			return callDaemon("stop", nil)
		case "status":
			if daemon.Running() {
				fmt.Println("tb daemon is running:", daemon.SocketPath())
			} else {
				fmt.Println("tb daemon is not running")
			}
			return 0
		}
	}
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	srv := daemon.NewServer(30 * time.Minute)
	session.New().Register(srv)
	if err := srv.Serve(); err != nil {
		log.Println(err)
		return 1
	}
	return 0
}
