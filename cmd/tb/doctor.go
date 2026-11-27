package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver/web"
)

const doctorUsage = `tb doctor: check this machine's setup for tb, and say what to fix

  tb doctor [--json]

One line per check: "ok <what>", or "problem <what>: <the fix>", then "ready: <platforms>"
(with "; fix: <what is broken>" when something is) or "not ready: <why>". It checks adb and
each Android device's state, Xcode and the booted iOS simulators (on a Mac), the Chrome tb
would drive, and the tb daemon (running or not, and whether it is this tb's build; tb
replaces another build's by itself). It boots, stops and changes no device, browser or
daemon. It runs adb devices, which starts adb's own server when none is running.

  --json   one JSON object: ok, usable (platforms), checks (name, ok, detail, fix, broken)

Exit status: 0 at least one platform is usable and nothing is broken (a platform that is
not set up is a problem line, but not broken; a device adb cannot use, or a failing
adb devices, is broken), 1 otherwise, 2 bad input.
`

type doctorEnv struct {
	goos     string
	lookPath func(string) (string, error)
	run      func(name string, args ...string) (string, error)
	adb      func() string
	chrome   func() string
	daemon   func() string
}

const (
	daemonStopped    = "stopped"
	daemonThisBuild  = "this build"
	daemonOtherBuild = "another build"
	daemonBusy       = "busy"
)

func daemonState(mine, pinged string, err error) string {
	switch {
	case err != nil:
		return daemonBusy
	case pinged == mine:
		return daemonThisBuild
	}
	return daemonOtherBuild
}

type doctorCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
	Broken bool   `json:"broken,omitempty"`
}

type doctorReport struct {
	OK     bool          `json:"ok"`
	Usable []string      `json:"usable"`
	Checks []doctorCheck `json:"checks"`
}

func (r *doctorReport) ok(name, detail string) {
	r.Checks = append(r.Checks, doctorCheck{Name: name, OK: true, Detail: detail})
}

func (r *doctorReport) missing(name, detail, fix string) {
	r.Checks = append(r.Checks, doctorCheck{Name: name, Detail: detail, Fix: fix})
}

func (r *doctorReport) broken(name, detail, fix string) {
	r.Checks = append(r.Checks, doctorCheck{Name: name, Detail: detail, Fix: fix, Broken: true})
}

func (r *doctorReport) brokenNames() []string {
	var names []string
	for _, c := range r.Checks {
		if c.Broken {
			names = append(names, c.Name)
		}
	}
	return names
}

func runCommand(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	var failed *exec.ExitError
	if len(out) == 0 && errors.As(err, &failed) {
		out = failed.Stderr
	}
	return string(out), err
}

func systemDoctorEnv() doctorEnv {
	return doctorEnv{
		goos:     runtime.GOOS,
		lookPath: exec.LookPath,
		run:      runCommand,
		adb:      device.ADB,
		chrome:   web.ChromePath,
		daemon:   pingDaemon,
	}
}

func pingDaemon() string {
	c, err := net.DialTimeout("unix", daemon.SocketPath(), 300*time.Millisecond)
	if err != nil {
		return daemonStopped
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	var resp daemon.Response
	if _, err = c.Write([]byte(`{"cmd":"ping"}` + "\n")); err == nil {
		err = json.NewDecoder(c).Decode(&resp)
	}
	return daemonState(daemon.BuildID(), resp.Text, err)
}

func runDoctor(e doctorEnv) doctorReport {
	r := doctorReport{Usable: []string{}}
	if checkAndroid(e, &r) {
		r.Usable = append(r.Usable, "android")
	}
	if checkIOS(e, &r) {
		r.Usable = append(r.Usable, "ios")
	}
	if checkChrome(e, &r) {
		r.Usable = append(r.Usable, "web")
	}
	checkDaemon(e, &r)
	r.OK = len(r.Usable) > 0 && len(r.brokenNames()) == 0
	return r
}

func checkAndroid(e doctorEnv, r *doctorReport) bool {
	adb, err := e.lookPath(e.adb())
	if err != nil {
		r.missing("adb", "not found", "install the Android SDK platform-tools and put adb on PATH (or set ANDROID_HOME)")
		return false
	}
	r.ok("adb", adb)
	out, err := e.run(adb, "devices")
	if err != nil {
		r.broken("android devices", fmt.Sprintf("adb devices failed (%v)", err), "run adb kill-server, then adb devices")
		return false
	}
	usable, seen := false, false
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "List of devices") {
			continue
		}
		seen = true
		name, state := "android "+f[0], strings.Join(f[1:], " ")
		switch state {
		case "device":
			r.ok(name, state)
			usable = true
		case "unauthorized":
			r.broken(name, state, `unlock it and accept its "Allow USB debugging" prompt (replug the cable if none shows)`)
		case "offline":
			r.broken(name, state, "reconnect it (adb reconnect offline), or restart the device")
		default:
			r.broken(name, state, "replug or restart the device until adb devices lists it as device")
		}
	}
	if !seen {
		r.missing("android devices", "none connected",
			"start an emulator, or plug in a phone with USB debugging on (tb test -p android starts an emulator itself)")
	}
	return usable
}

func checkIOS(e doctorEnv, r *doctorReport) bool {
	if e.goos != "darwin" {
		r.ok("ios", fmt.Sprintf("needs a Mac (this is %s)", e.goos))
		return false
	}
	const installXcode = "install Xcode (simulators need it), then run sudo xcode-select -s /Applications/Xcode.app"
	dir, err := e.run("xcode-select", "-p")
	dir = strings.TrimSpace(dir)
	if err != nil {
		r.missing("xcode", "not installed", installXcode)
		return false
	}
	if strings.Contains(dir, "CommandLineTools") {
		r.missing("xcode", fmt.Sprintf("not installed (only the command line tools, %s)", dir), installXcode)
		return false
	}
	r.ok("xcode", dir)
	out, err := e.run("xcrun", "simctl", "list", "devices", "booted", "-j")
	var listed struct {
		Devices map[string][]struct {
			UDID  string `json:"udid"`
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"devices"`
	}
	if err == nil {
		err = json.Unmarshal([]byte(out), &listed)
	}
	if err != nil {
		r.missing("ios simulators", fmt.Sprintf("xcrun simctl failed (%v)", err),
			installXcode)
		return false
	}
	runtimes := make([]string, 0, len(listed.Devices))
	for rt := range listed.Devices {
		runtimes = append(runtimes, rt)
	}
	sort.Strings(runtimes)
	booted := false
	for _, rt := range runtimes {
		for _, d := range listed.Devices[rt] {
			if d.State == "Booted" {
				r.ok(fmt.Sprintf("ios simulator %s (%s)", d.Name, d.UDID), "booted")
				booted = true
			}
		}
	}
	if !booted {
		r.missing("ios simulators", "none booted",
			`boot one (xcrun simctl boot "<name>"; xcrun simctl list devices names them), or let tb test -p ios boot one`)
	}
	return booted
}

func checkChrome(e doctorEnv, r *doctorReport) bool {
	const fix = "install Google Chrome, or set TB_CHROME to a Chrome or Chromium binary"
	p := e.chrome()
	if p == "" {
		r.missing("chrome", "not found", fix)
		return false
	}
	if _, err := e.lookPath(p); err != nil {
		r.missing("chrome", p+" cannot be run", fix)
		return false
	}
	r.ok("chrome", p)
	return true
}

func checkDaemon(e doctorEnv, r *doctorReport) {
	switch e.daemon() {
	case daemonStopped:
		r.ok("daemon", "not running (tb starts it when needed)")
	case daemonThisBuild:
		r.ok("daemon", "running, this tb's build")
	case daemonOtherBuild:
		r.ok("daemon", "running, another tb build (the next tb command replaces it and closes its sessions)")
	default:
		r.ok("daemon", "running, busy (it did not answer in time to say which build it is)")
	}
}

func (r doctorReport) print(w io.Writer) {
	for _, c := range r.Checks {
		if c.OK {
			fmt.Fprintf(w, "ok %s %s\n", c.Name, c.Detail)
		} else {
			fmt.Fprintf(w, "problem %s %s: %s\n", c.Name, c.Detail, c.Fix)
		}
	}
	switch broken := strings.Join(r.brokenNames(), ", "); {
	case broken != "" && len(r.Usable) > 0:
		fmt.Fprintf(w, "ready: %s; fix: %s\n", strings.Join(r.Usable, ", "), broken)
	case broken != "":
		fmt.Fprintf(w, "not ready: fix %s\n", broken)
	case len(r.Usable) == 0:
		fmt.Fprintln(w, "not ready: no Android device, booted iOS simulator or Chrome to drive")
	default:
		fmt.Fprintf(w, "ready: %s\n", strings.Join(r.Usable, ", "))
	}
}

func doctor(e doctorEnv, asJSON bool, w io.Writer) int {
	r := runDoctor(e)
	if asJSON {
		b, _ := json.MarshalIndent(r, "", "  ")
		fmt.Fprintln(w, string(b))
	} else {
		r.print(w)
	}
	if r.OK {
		return 0
	}
	return 1
}

func cmdDoctor(argv []string) int {
	a, err := parseArgs(argv, flagSpec{[]string{"json"}, boolFlag}, flagSpec{[]string{"help", "h"}, boolFlag})
	if err == nil {
		err = a.noPos()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n\n%s", err, doctorUsage)
		return 2
	}
	if a.flag("help") {
		fmt.Print(doctorUsage)
		return 0
	}
	return doctor(systemDoctorEnv(), a.flag("json"), os.Stdout)
}
