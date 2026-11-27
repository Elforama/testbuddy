package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

const fakeSimctlBooted = `{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-26-0":[
{"udid":"AAAA-1111","name":"iPhone 17","state":"Booted"},
{"udid":"BBBB-2222","name":"iPhone 16","state":"Shutdown"}]}}`

func fakeDoctorEnv() doctorEnv {
	return doctorEnv{
		goos: "darwin",
		lookPath: func(p string) (string, error) {
			return p, nil
		},
		adb:    func() string { return "/sdk/platform-tools/adb" },
		chrome: func() string { return "/Applications/Chrome" },
		daemon: func() string { return daemonStopped },
		run: func(name string, args ...string) (string, error) {
			switch name + " " + strings.Join(args, " ") {
			case "/sdk/platform-tools/adb devices":
				return "List of devices attached\nemulator-5554\tdevice\n\n", nil
			case "xcode-select -p":
				return "/Applications/Xcode.app/Contents/Developer\n", nil
			case "xcrun simctl list devices booted -j":
				return fakeSimctlBooted, nil
			}
			return "", errors.New("unexpected command " + name)
		},
	}
}

func missing(names ...string) func(string) (string, error) {
	return func(p string) (string, error) {
		for _, n := range names {
			if p == n {
				return "", errors.New("not found")
			}
		}
		return p, nil
	}
}

func withOutput(e doctorEnv, command, out string, err error) doctorEnv {
	inner := e.run
	e.run = func(name string, args ...string) (string, error) {
		if name+" "+strings.Join(args, " ") == command {
			return out, err
		}
		return inner(name, args...)
	}
	return e
}

func doctorLines(t *testing.T, e doctorEnv) (string, int) {
	t.Helper()
	var b bytes.Buffer
	code := doctor(e, false, &b)
	return b.String(), code
}

func wantLines(t *testing.T, out string, lines ...string) {
	t.Helper()
	for _, l := range lines {
		if !strings.Contains("\n"+out, "\n"+l+"\n") {
			t.Errorf("missing line %q in:\n%s", l, out)
		}
	}
}

func TestDoctorAllOK(t *testing.T) {
	out, code := doctorLines(t, fakeDoctorEnv())
	want := `ok adb /sdk/platform-tools/adb
ok android emulator-5554 device
ok xcode /Applications/Xcode.app/Contents/Developer
ok ios simulator iPhone 17 (AAAA-1111) booted
ok chrome /Applications/Chrome
ok daemon not running (tb starts it when needed)
ready: android, ios, web
`
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
}

func TestDoctorAdbMissing(t *testing.T) {
	e := fakeDoctorEnv()
	e.lookPath = missing("/sdk/platform-tools/adb")
	out, code := doctorLines(t, e)
	wantLines(t, out,
		"problem adb not found: install the Android SDK platform-tools and put adb on PATH (or set ANDROID_HOME)",
		"ready: ios, web")
	if strings.Contains(out, "android ") {
		t.Errorf("adb is missing, yet devices are listed:\n%s", out)
	}
	if code != 0 {
		t.Errorf("exit %d, want 0: a platform that is not set up is not broken", code)
	}
}

func TestDoctorAndroidDeviceStates(t *testing.T) {
	e := withOutput(fakeDoctorEnv(), "/sdk/platform-tools/adb devices",
		"* daemon started successfully\nList of devices attached\nemulator-5554\tdevice\nR58M\tunauthorized\n10.0.0.5:5555\toffline\n", nil)
	out, code := doctorLines(t, e)
	wantLines(t, out,
		"ok android emulator-5554 device",
		`problem android R58M unauthorized: unlock it and accept its "Allow USB debugging" prompt (replug the cable if none shows)`,
		"problem android 10.0.0.5:5555 offline: reconnect it (adb reconnect offline), or restart the device",
		"ready: android, ios, web; fix: android R58M, android 10.0.0.5:5555")
	if code != 1 {
		t.Errorf("exit %d, want 1: a device adb cannot use is broken", code)
	}
}

func TestDoctorNoAndroidDevices(t *testing.T) {
	e := withOutput(fakeDoctorEnv(), "/sdk/platform-tools/adb devices", "List of devices attached\n\n", nil)
	out, code := doctorLines(t, e)
	wantLines(t, out,
		"problem android devices none connected: start an emulator, or plug in a phone with USB debugging on (tb test -p android starts an emulator itself)",
		"ready: ios, web")
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
}

func TestDoctorAdbFails(t *testing.T) {
	e := withOutput(fakeDoctorEnv(), "/sdk/platform-tools/adb devices", "", errors.New("exit status 1"))
	out, code := doctorLines(t, e)
	wantLines(t, out, "problem android devices adb devices failed (exit status 1): run adb kill-server, then adb devices")
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
}

func TestDoctorXcodeMissing(t *testing.T) {
	e := withOutput(fakeDoctorEnv(), "xcode-select -p", "", errors.New("exit status 2"))
	out, code := doctorLines(t, e)
	wantLines(t, out,
		"problem xcode not installed: install Xcode (simulators need it), then run sudo xcode-select -s /Applications/Xcode.app",
		"ready: android, web")
	if strings.Contains(out, "ios simulator") {
		t.Errorf("no Xcode, yet simulators are listed:\n%s", out)
	}
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
}

func TestDoctorOnlyCommandLineTools(t *testing.T) {
	e := withOutput(fakeDoctorEnv(), "xcode-select -p", "/Library/Developer/CommandLineTools\n", nil)
	e = withOutput(e, "xcrun simctl list devices booted -j", "", errors.New("simctl must not run"))
	out, code := doctorLines(t, e)
	wantLines(t, out,
		"problem xcode not installed (only the command line tools, /Library/Developer/CommandLineTools): install Xcode (simulators need it), then run sudo xcode-select -s /Applications/Xcode.app",
		"ready: android, web")
	if strings.Contains(out, "ok xcode") || strings.Contains(out, "ios simulator") {
		t.Errorf("the command line tools are not Xcode:\n%s", out)
	}
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
}

func TestDoctorSimctlFails(t *testing.T) {
	e := withOutput(fakeDoctorEnv(), "xcrun simctl list devices booted -j", "", errors.New("exit status 72"))
	out, _ := doctorLines(t, e)
	wantLines(t, out,
		"ok xcode /Applications/Xcode.app/Contents/Developer",
		"problem ios simulators xcrun simctl failed (exit status 72): install Xcode (simulators need it), then run sudo xcode-select -s /Applications/Xcode.app",
		"ready: android, web")
}

func TestDoctorNoBootedSimulator(t *testing.T) {
	e := withOutput(fakeDoctorEnv(), "xcrun simctl list devices booted -j", `{"devices":{}}`, nil)
	out, code := doctorLines(t, e)
	wantLines(t, out,
		`problem ios simulators none booted: boot one (xcrun simctl boot "<name>"; xcrun simctl list devices names them), or let tb test -p ios boot one`,
		"ready: android, web")
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
}

func TestDoctorIOSNeedsAMac(t *testing.T) {
	e := fakeDoctorEnv()
	e.goos = "linux"
	e.run = func(name string, args ...string) (string, error) {
		if name == "/sdk/platform-tools/adb" {
			return "List of devices attached\nemulator-5554\tdevice\n", nil
		}
		t.Errorf("ran %s %v on linux", name, args)
		return "", errors.New("no")
	}
	out, code := doctorLines(t, e)
	wantLines(t, out, "ok ios needs a Mac (this is linux)", "ready: android, web")
	if strings.Contains(out, "problem") {
		t.Errorf("iOS on linux is not a problem:\n%s", out)
	}
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
}

func TestDoctorChromeMissing(t *testing.T) {
	e := fakeDoctorEnv()
	e.chrome = func() string { return "" }
	out, code := doctorLines(t, e)
	wantLines(t, out,
		"problem chrome not found: install Google Chrome, or set TB_CHROME to a Chrome or Chromium binary",
		"ready: android, ios")
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
}

func TestDoctorChromePathDoesNotExist(t *testing.T) {
	e := fakeDoctorEnv()
	e.chrome = func() string { return "/nowhere/chrome" }
	e.lookPath = missing("/nowhere/chrome")
	out, _ := doctorLines(t, e)
	wantLines(t, out, "problem chrome /nowhere/chrome cannot be run: install Google Chrome, or set TB_CHROME to a Chrome or Chromium binary")
}

func TestDoctorDaemon(t *testing.T) {
	for state, want := range map[string]string{
		daemonStopped:    "ok daemon not running (tb starts it when needed)",
		daemonThisBuild:  "ok daemon running, this tb's build",
		daemonOtherBuild: "ok daemon running, another tb build (the next tb command replaces it and closes its sessions)",
		daemonBusy:       "ok daemon running, busy (it did not answer in time to say which build it is)",
	} {
		e := fakeDoctorEnv()
		e.daemon = func() string { return state }
		out, code := doctorLines(t, e)
		wantLines(t, out, want, "ready: android, ios, web")
		if code != 0 {
			t.Errorf("%s: exit %d, want 0", state, code)
		}
	}
}

func TestDaemonStateFromPing(t *testing.T) {
	for _, c := range []struct {
		build string
		err   error
		want  string
	}{
		{"1-2", nil, daemonThisBuild},
		{"3-4", nil, daemonOtherBuild},
		{"", os.ErrDeadlineExceeded, daemonBusy},
		{"", errors.New("connection reset"), daemonBusy},
	} {
		if got := daemonState("1-2", c.build, c.err); got != c.want {
			t.Errorf("ping %q, %v: %s, want %s", c.build, c.err, got, c.want)
		}
	}
}

func TestDoctorBrokenAndNothingUsable(t *testing.T) {
	e := withOutput(fakeDoctorEnv(), "/sdk/platform-tools/adb devices", "List of devices attached\nR58M\tunauthorized\n", nil)
	e.chrome = func() string { return "" }
	e = withOutput(e, "xcode-select -p", "", errors.New("exit status 2"))
	out, code := doctorLines(t, e)
	wantLines(t, out, "not ready: fix android R58M")
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
}

func TestDoctorNothingUsable(t *testing.T) {
	e := fakeDoctorEnv()
	e.lookPath = missing("/sdk/platform-tools/adb")
	e.chrome = func() string { return "" }
	e = withOutput(e, "xcode-select -p", "", errors.New("exit status 2"))
	out, code := doctorLines(t, e)
	wantLines(t, out, "not ready: no Android device, booted iOS simulator or Chrome to drive")
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
}

func TestDoctorJSON(t *testing.T) {
	e := withOutput(fakeDoctorEnv(), "/sdk/platform-tools/adb devices", "List of devices attached\nR58M\tunauthorized\n", nil)
	var b bytes.Buffer
	if code := doctor(e, true, &b); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	var got struct {
		OK     bool     `json:"ok"`
		Usable []string `json:"usable"`
		Checks []struct {
			Name, Detail, Fix string
			OK, Broken        bool
		} `json:"checks"`
	}
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatalf("%v in %s", err, b.String())
	}
	if got.OK || strings.Join(got.Usable, ",") != "ios,web" || len(got.Checks) != 6 {
		t.Fatalf("got %+v", got)
	}
	c := got.Checks[1]
	if c.Name != "android R58M" || c.Detail != "unauthorized" || c.OK || !c.Broken || !strings.Contains(c.Fix, "Allow USB debugging") {
		t.Errorf("got %+v", c)
	}
	if c := got.Checks[0]; c.Name != "adb" || !c.OK || c.Fix != "" {
		t.Errorf("got %+v", c)
	}
}

func TestCmdDoctorRejectsBadInput(t *testing.T) {
	_, stderr := capture(t, func() {
		if code := cmdDoctor([]string{"extra"}); code != 2 {
			t.Errorf("exit %d, want 2", code)
		}
	})
	if !strings.Contains(stderr, "extra") {
		t.Errorf("stderr: %s", stderr)
	}
}
